// handler_port_mirrors.go 暴露端口镜像状态查询、重试与冲突占用者处理的 HTTP/WS 接口。
//
// 职责：
//   - GET /api/port-mirrors：返回 portmirror.Manager 当前全部镜像状态快照
//   - GET /ws/port-mirrors：状态变更时推送全量快照
//   - POST /api/port-mirrors/retry：清除指定 host+port 的冲突/失败冷却记忆并立即重试
//   - POST /api/port-mirrors/stop-occupier：停止占用本机端口的进程以解除镜像冲突，
//     区分托管/非托管两条路径，全程写 operation 审计
//
// 边界：
//   - 不做镜像期望态计算或收敛决策——那是 portmirror.Manager 的职责，本文件只读它
//     的快照、调它的 Retry/ReconcileNow
//   - stop-occupier 不建审批门：spec 裁定「用户在冲突卡片上点击停止」本身就是一次
//     人工审查动作，与 tunnel 失效审计（operation/policy.go 的 PlanTunnelInvalidation）
//     是同一先例——跳过审批门但强制写审计，保证事后可追溯
//   - 非托管占用者只发 SIGTERM，从不 SIGKILL；托管占用者走既有 runDeploymentRuntimeAction
//     的 stop 语义，不在本文件重复实现进程停止逻辑
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/portmirror"
	"github.com/xsxdot/super-dev/agent/process"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// portMirrorTerminateGrace 是非托管占用者收到 SIGTERM 后的存活等待宽限期。
//
// 注意：
//   - 生产恒为 spec 固定值 3s；测试可临时覆写为更短值，避免真实等待拖慢用例
//     （见 handler_port_mirrors_test.go 的失败路径用例）
var portMirrorTerminateGrace = 3 * time.Second

// listPortMirrors 处理 GET /api/port-mirrors，返回当前全部镜像状态快照。
func (a *App) listPortMirrors(w http.ResponseWriter, r *http.Request) {
	if a.mirrorManager == nil {
		jsonOK(w, []portmirror.MirrorStatus{})
		return
	}
	jsonOK(w, a.mirrorManager.Statuses())
}

// wsPortMirrors 处理 GET /ws/port-mirrors，向桌面端推送镜像状态全量快照。
//
// 模式抄 wsNodes（handler_node_status.go）：升级后立即收到一次基线快照，此后每次
// 状态变化都收到完整快照；连接断开或订阅 channel 关闭即退出，日志密度与 wsNodes
// 一致（wsNodes 本身不打连接建立/断开日志，这里同样不打）。
func (a *App) wsPortMirrors(w http.ResponseWriter, r *http.Request) {
	if a.mirrorManager == nil {
		jsonError(w, http.StatusServiceUnavailable, "port mirror manager unavailable")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 读 pump：镜像快照流是稀疏的（可能数小时零帧），没有写失败可借以发现断连；
	// WS 升级（hijack）后 r.Context() 也不会因客户端断开而 Done。必须主动读连接，
	// 读出错即退出主循环，及时回收断开客户端占用的 goroutine/fd/订阅项。
	// （wsNodes 无此问题：它的流是 ≤5s 心跳级高频，写失败很快暴露。）
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ch, unsubscribe := a.mirrorManager.Subscribe()
	defer unsubscribe()
	for {
		select {
		case snapshot, ok := <-ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(snapshot); err != nil {
				return
			}
		case <-readDone:
			return
		case <-r.Context().Done():
			return
		}
	}
}

// portMirrorTargetRequest 是 retry/stop-occupier 共用的请求体：定位一条「host × 端口」镜像。
type portMirrorTargetRequest struct {
	HostID string `json:"host_id"`
	Port   int    `json:"port"`
}

// decodePortMirrorTargetRequest 解析并校验 {host_id, port} 请求体；
// 失败时已写好 400 响应，调用方直接 return。
func decodePortMirrorTargetRequest(w http.ResponseWriter, r *http.Request) (portMirrorTargetRequest, bool) {
	var req portMirrorTargetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return portMirrorTargetRequest{}, false
	}
	req.HostID = strings.TrimSpace(req.HostID)
	if req.HostID == "" || req.Port <= 0 {
		jsonError(w, http.StatusBadRequest, "host_id and port are required")
		return portMirrorTargetRequest{}, false
	}
	return req, true
}

// retryPortMirror 处理 POST /api/port-mirrors/retry：清除冷却记忆并立即重试。
func (a *App) retryPortMirror(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePortMirrorTargetRequest(w, r)
	if !ok {
		return
	}
	if a.mirrorManager != nil {
		a.mirrorManager.Retry(req.HostID, req.Port)
	}
	jsonOK(w, map[string]string{"status": "retrying"})
}

// lookupPortMirrorOccupierFresh 是执行停止前重新识别端口占用者的入口；
// var 形式供测试覆写，模拟「占用者已变化/端口已释放」的竞态场景。
var lookupPortMirrorOccupierFresh = portmirror.LookupOccupier

// portMirrorOccupierVerdict 表示 stop-occupier 执行前复核的裁决结果。
type portMirrorOccupierVerdict int

const (
	// occupierVerdictProceed 表示实时占用者与快照一致，可以执行停止。
	occupierVerdictProceed portMirrorOccupierVerdict = iota
	// occupierVerdictAlreadyFreed 表示端口已无人监听，冲突已自行消解。
	occupierVerdictAlreadyFreed
	// occupierVerdictChanged 表示占用者 pid 已变化，按旧快照执行会打到无辜进程。
	occupierVerdictChanged
)

// verdictForOccupierRecheck 比对快照占用者与执行时实时占用者，产出裁决。
//
// 为什么必须复核：快照来自 reconcile 时的 lsof 结果并受 30s 冷却记忆约束，用户点击
// "停止"时这份 pid 可能已陈旧——原进程退出、OS 复用 pid 后，SIGTERM 会送达一个与
// 端口无关的进程。展示身份用快照（与 UI 冲突卡片一致），执行授权用实时复核。
func verdictForOccupierRecheck(snapshot, fresh *portmirror.Occupier) portMirrorOccupierVerdict {
	switch {
	case fresh == nil:
		return occupierVerdictAlreadyFreed
	case fresh.PID != snapshot.PID:
		return occupierVerdictChanged
	default:
		return occupierVerdictProceed
	}
}

// stopPortMirrorOccupier 处理 POST /api/port-mirrors/stop-occupier：解析冲突占用者，
// 执行前实时复核占用者未变化，再按托管/非托管分流停止（裁决见 resolvePortMirrorConflict）。
func (a *App) stopPortMirrorOccupier(w http.ResponseWriter, r *http.Request) {
	req, ok := decodePortMirrorTargetRequest(w, r)
	if !ok {
		return
	}
	occ, found := a.findPortMirrorOccupier(req.HostID, req.Port)
	if !found {
		jsonError(w, http.StatusNotFound, "no port mirror conflict found for host/port")
		return
	}
	fresh, err := lookupPortMirrorOccupierFresh(req.Port, a.resolveManagedPID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, fmt.Sprintf("复核端口占用者失败: %v", err))
		return
	}
	switch verdictForOccupierRecheck(occ, fresh) {
	case occupierVerdictAlreadyFreed:
		// 端口已释放：无需停止任何进程，立即重试镜像让它收敛。
		log.Printf("[SuperDev] portmirror: stop-occupier 复核发现端口 %d 已释放，跳过停止并重试镜像", req.Port)
		if a.mirrorManager != nil {
			a.mirrorManager.Retry(req.HostID, req.Port)
		}
		jsonOK(w, map[string]string{"status": "already_freed"})
		return
	case occupierVerdictChanged:
		log.Printf("[SuperDev] portmirror: stop-occupier 复核发现占用者已变化 port=%d snapshot_pid=%d fresh_pid=%d，拒绝执行",
			req.Port, occ.PID, fresh.PID)
		jsonError(w, http.StatusConflict, "port occupier has changed; refresh conflict details and retry")
		return
	}
	if err := a.resolvePortMirrorConflict(r.Context(), req.HostID, req.Port, occ); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "stopped"})
}

// findPortMirrorOccupier 从当前镜像状态快照里找到 {host_id, port} 冲突条目的占用者。
//
// 注意：
//   - 占用者信息由 portmirror.Manager 在 reconcile 时通过 LookupOccupier 解析并缓存
//     进 MirrorStatus——与 UI 冲突卡片展示的是同一份数据（身份展示用快照）
//   - 但快照 pid 可能陈旧（冷却记忆 ≥30s），执行停止前必须经
//     verdictForOccupierRecheck 实时复核，防 pid 复用后误伤无辜进程
func (a *App) findPortMirrorOccupier(hostID string, port int) (*portmirror.Occupier, bool) {
	if a.mirrorManager == nil {
		return nil, false
	}
	for _, st := range a.mirrorManager.Statuses() {
		if st.HostID == hostID && st.Port == port && st.State == portmirror.MirrorStateConflict && st.Occupier != nil {
			return st.Occupier, true
		}
	}
	return nil, false
}

// resolvePortMirrorConflict 是 stop-occupier 的核心裁决：按占用者是否为 SuperDev 托管
// deployment 选择停止路径，全程写 operation 审计（prepared → executed/failed）。
//
// 参数：
//   - ctx: 审计写入与托管路径运行态操作共用的上下文
//   - hostID/port: 冲突镜像目标，写入审计 Data，供事后追溯
//   - occ: 已识别的占用者（PID/Name/ManagedDeploymentID）
//
// 返回：
//   - nil 表示占用者已停止；非 nil 时占用者可能仍存活，调用方不得认为端口已释放
//
// 注意：
//   - 不建审批门：spec 裁定「UI 点击即人审」，同 PlanTunnelInvalidation 免审批+
//     强制审计的先例（operation/policy.go）
//   - prepared 审计写入失败时直接拒绝执行——审计是这条动作合法性的前提，不是事后补充
//   - 无论停止成功还是失败都会落终态审计（executed/failed），保证冲突处理动作的
//     事后可追溯；终态审计写入失败会附加进返回错误，但不影响已经生效的停止结果
func (a *App) resolvePortMirrorConflict(ctx context.Context, hostID string, port int, occ *portmirror.Occupier) error {
	if occ == nil {
		return fmt.Errorf("occupier is required")
	}
	managed := occ.ManagedDeploymentID != ""
	plan := portMirrorStopOccupierPlan(hostID, port, occ, managed)

	if _, err := a.operationAudit.Append(ctx, operation.AuditEvent{
		Kind:    operation.KindPortMirrorStopOccupier,
		Action:  operation.AuditPrepared,
		Plan:    plan,
		Summary: "port mirror conflict resolution prepared before stopping occupier",
		Data:    portMirrorStopOccupierAuditData(hostID, port, occ, managed),
	}); err != nil {
		return fmt.Errorf("写入停止占用进程 prepared 审计: %w", err)
	}

	var stopErr error
	if managed {
		stopErr = a.stopManagedPortMirrorOccupier(ctx, occ.ManagedDeploymentID)
	} else {
		stopErr = terminatePortMirrorOccupier(occ.PID)
	}
	// 停止占用进程是敏感动作，成功与失败都必须打日志（脱敏：只带 pid/name/port，无凭据）。
	log.Printf("[SuperDev] portmirror: 停止占用进程 pid=%d name=%s port=%d 结果=%v", occ.PID, occ.Name, port, stopErr)

	action := operation.AuditExecuted
	summary := "port mirror occupier stopped"
	terminalData := portMirrorStopOccupierAuditData(hostID, port, occ, managed)
	if stopErr != nil {
		action = operation.AuditFailed
		summary = "port mirror occupier stop failed: " + stopErr.Error()
		terminalData["error"] = stopErr.Error()
	}
	if _, auditErr := a.operationAudit.Append(ctx, operation.AuditEvent{
		Kind:    operation.KindPortMirrorStopOccupier,
		Action:  action,
		Plan:    plan,
		Summary: summary,
		Data:    terminalData,
	}); auditErr != nil {
		log.Printf("[SuperDev] portmirror: 停止占用进程终态审计写入失败 pid=%d port=%d err=%v", occ.PID, port, auditErr)
		if stopErr == nil {
			return fmt.Errorf("停止占用进程成功但终态审计写入失败: %w", auditErr)
		}
	}

	if stopErr == nil && a.mirrorManager != nil {
		// 两条路径成功后都立即重试：托管/非托管都可能让本机端口刚刚释放，
		// 不主动 Retry 会让用户点了"停止"之后还要再等一次 30s 冷却窗口才收敛。
		a.mirrorManager.Retry(hostID, port)
	}
	return stopErr
}

// stopManagedPortMirrorOccupier 走既有 runDeploymentRuntimeAction 的 stop 语义停止
// 托管占用者，不在此重复实现进程终止逻辑；不经过 controlDeploymentRuntime 的
// authorizeOperation 审批门（该门本就只存在于 HTTP 入口层，直接调用天然跳过）。
func (a *App) stopManagedPortMirrorOccupier(ctx context.Context, deploymentID string) error {
	dep, project, ok := a.findDeployment(deploymentID)
	if !ok {
		return fmt.Errorf("managed deployment %s not found", deploymentID)
	}
	return a.runDeploymentRuntimeAction(ctx, project.ID, dep, operation.OperationRuntimeStop, intentStartNormal)
}

// terminatePortMirrorOccupier 向非托管占用者发送 SIGTERM 并等待其在宽限期内退出。
//
// 为什么不 SIGKILL：占用者可能是用户自己手起的进程或另一个工具，不属于 SuperDev
// 的管理范围——端口镜像冲突只说明「这个本机端口被占了」，不构成强杀别人进程的授权。
// SIGTERM 给它一个优雅退出的机会；宽限期内仍存活就返回错误，交回用户自行处理。
func terminatePortMirrorOccupier(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("查找占用进程失败: %w", err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		if !processAlivePID(pid) {
			return nil // 发送时已退出（竞态），视为成功
		}
		return fmt.Errorf("发送 SIGTERM 失败: %w", err)
	}
	deadline := time.Now().Add(portMirrorTerminateGrace)
	for time.Now().Before(deadline) {
		if !processAlivePID(pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !processAlivePID(pid) {
		return nil
	}
	return fmt.Errorf("occupier pid %d 在 SIGTERM 后 %s 仍存活", pid, portMirrorTerminateGrace)
}

// processAlivePID 用 signal 0 探测进程是否仍存活（不实际发送信号，是 unix 存活探测惯例）。
func processAlivePID(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// portMirrorStopOccupierPlan 构造 stop-occupier 的审计专用 Plan——RequiresApproval
// 恒为 false（不建审批门），只承载 Kind/Target/Fingerprint 供审计事件引用。
func portMirrorStopOccupierPlan(hostID string, port int, occ *portmirror.Occupier, managed bool) operation.Plan {
	now := time.Now().UTC()
	effect := fmt.Sprintf("SIGTERM non-managed occupier pid %d on host %s port %d", occ.PID, hostID, port)
	if managed {
		effect = fmt.Sprintf("stop managed deployment %s occupying host %s port %d", occ.ManagedDeploymentID, hostID, port)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%v", operation.KindPortMirrorStopOccupier, hostID, port, managed)))
	return operation.Plan{
		ID:               "op_" + uuid.NewString(),
		Kind:             operation.KindPortMirrorStopOccupier,
		Target:           operation.Target{HostID: hostID, DeploymentID: occ.ManagedDeploymentID},
		TargetSummary:    fmt.Sprintf("port mirror conflict on %s:%d", hostID, port),
		RiskLevel:        operation.RiskLow,
		RequiresApproval: false,
		ExpectedEffects:  []string{effect},
		Checks: []operation.Check{
			{Name: "occupier_resolved", Status: "passed", Message: "occupier identified via port mirror conflict detection"},
		},
		Fingerprint: "sha256:" + hex.EncodeToString(sum[:]),
		CreatedAt:   now,
		ExpiresAt:   now.Add(operation.DefaultPlanTTL),
	}
}

// portMirrorStopOccupierAuditData 构造审计 Data：{host_id, port, occupier_pid, occupier_name, managed}。
func portMirrorStopOccupierAuditData(hostID string, port int, occ *portmirror.Occupier, managed bool) map[string]any {
	return map[string]any{
		"host_id":       hostID,
		"port":          port,
		"occupier_pid":  occ.PID,
		"occupier_name": occ.Name,
		"managed":       managed,
	}
}

// portMirrorTarget 把 host_id 解析为端口镜像用的 SSH 隧道目标，供 portmirror.Manager
// 的 Deps.Target 使用（装配见 server.go 的 NewApp）。
func (a *App) portMirrorTarget(hostID string) (tunnel.Target, error) {
	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		return tunnel.Target{}, err
	}
	if !found {
		return tunnel.Target{}, fmt.Errorf("agent not configured for host %s", hostID)
	}
	return nodetransport.TunnelTargetFromNodeTarget(nodetransport.NodeTarget{Host: host, Agent: agent}), nil
}

// resolveManagedPID 反查本机 pid 是否属于某个正在运行的托管 deployment，
// 供 portmirror.Manager 的 Deps.Resolve 使用（端口冲突时判断占用者能否安全停止）。
//
// 注意：
//   - 只扫描 a.managers（项目级 deployment manager），不含 a.procMgr
//     （远端 collector 复用的进程管理器，与 deployment 无关）
//   - 只在端口冲突识别路径调用（该路径本身已经在做 lsof/ps 同步调用），
//     线性扫描全部 manager 的量级可接受
func (a *App) resolveManagedPID(pid int) (string, bool) {
	a.mu.RLock()
	managers := make([]*process.Manager, 0, len(a.managers))
	for _, mgr := range a.managers {
		if mgr != nil {
			managers = append(managers, mgr)
		}
	}
	a.mu.RUnlock()
	for _, mgr := range managers {
		for depID, running := range mgr.RunningPIDs() {
			if running == pid {
				return depID, true
			}
		}
	}
	return "", false
}
