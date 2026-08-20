// handler_node_status.go 暴露节点状态中心和远端 agent 状态上报 WebSocket。
//
// 职责：
//   - /ws/node-status：远端 agent 周期上报本机 managed runtime/collector 状态
//   - /api/nodes：桌面端读取 NodeRegistry 当前快照
//   - /ws/nodes：桌面端订阅 NodeRegistry 快照变化
//
// 边界：
//   - 不建立 SSH 隧道，传输生命周期由 NodeTransport/TunnelTransport 管理
//   - 不持久化节点状态
//   - 不替代 deployment 日志 WebSocket
package api

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

const nodeStatusReportInterval = 5 * time.Second

// listNodes 处理 GET /api/nodes。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - Registry 尚未装配时返回空数组，保持桌面端启动兼容
func (a *App) listNodes(w http.ResponseWriter, r *http.Request) {
	if a.nodeRegistry == nil {
		jsonOK(w, []nodetransport.NodeStatus{})
		return
	}
	jsonOK(w, a.nodeRegistry.Snapshot())
}

// wsNodes 处理 GET /ws/nodes，向桌面端推送 NodeRegistry 全量快照。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - 每次 Registry 变化都发送全量快照，前端只需要按 host_id 覆盖缓存
//   - 本连接数还驱动 DesktopOnline 信号（见 incLocalWSClients），因此断连检测
//     不能只靠"下次广播时 WriteJSON 失败"——若本机没有配置任何远端节点，
//     Registry 可能长期零广播，写失败永远不会发生。必须像 wsOperationApprovals/
//     wsPortMirrors 一样起读 pump 主动探测断连（同一教训，见那两处头注释）。
func (a *App) wsNodes(w http.ResponseWriter, r *http.Request) {
	if a.nodeRegistry == nil {
		jsonError(w, http.StatusServiceUnavailable, "node registry unavailable")
		return
	}
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// 计数进入/退出必须成对：defer 保证连接以任何方式退出（含下方读 pump 探测到
	// 的网络级断连）时计数依然会被减回，否则 DesktopOnline 会永久卡在 true。
	a.incLocalWSClients()
	defer a.decLocalWSClients()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	ch, unsubscribe := a.nodeRegistry.Subscribe()
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

// incLocalWSClients 增加本机 /ws/nodes 活跃连接计数，在 wsNodes handler
// 建立连接后调用；与 decLocalWSClients 成对出现，供 nodeStatusSnapshot
// 派生 DesktopOnline 信号。
func (a *App) incLocalWSClients() {
	a.localWSClientsMu.Lock()
	a.localWSClients++
	a.localWSClientsMu.Unlock()
}

// decLocalWSClients 减少本机 /ws/nodes 活跃连接计数，由 wsNodes handler 的
// defer 调用，保证连接以任何方式退出（正常关闭/异常断开/handler panic 前的
// defer 链）都会执行。计数出现负数说明 inc/dec 未成对，属于编程错误——不静默
// 吞掉，打 warn 暴露问题，同时把计数钳制回 0 避免污染后续判定。
func (a *App) decLocalWSClients() {
	a.localWSClientsMu.Lock()
	a.localWSClients--
	if a.localWSClients < 0 {
		log.Printf("[SuperDev] warn: localWSClients 计数为负 count=%d，钳制为 0（inc/dec 未成对，请检查 wsNodes 生命周期）", a.localWSClients)
		a.localWSClients = 0
	}
	a.localWSClientsMu.Unlock()
}

// localDesktopOnline 返回本机当前是否有活跃 /ws/nodes 订阅——即桌面端在场信号。
func (a *App) localDesktopOnline() bool {
	a.localWSClientsMu.Lock()
	defer a.localWSClientsMu.Unlock()
	return a.localWSClients > 0
}

// wsNodeStatus 处理 GET /ws/node-status，供远端 agent 周期上报自身状态。
//
// 参数：
//   - w: HTTP 响应写入器
//   - r: HTTP 请求
//
// 注意：
//   - host_id/host_name 来自 TunnelTransport 查询参数，用于绑定桌面端 host
func (a *App) wsNodeStatus(w http.ResponseWriter, r *http.Request) {
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	hostID := r.URL.Query().Get("host_id")
	hostName := r.URL.Query().Get("host_name")
	if hostID == "" {
		hostID = a.identity.NodeID
	}
	if hostID == "" {
		hostID = uuid.NewString()
	}
	if hostName == "" {
		// identity.DisplayName 可能来自操作系统 hostname，不能作为安全系统身份的隐式退化。
		hostName = hostID
	}

	publisher := newNodeStatusPublisher(a, hostID, hostName, nodeStatusReportInterval)
	unregister := a.registerNodeStatusPublisher(publisher)
	defer unregister()
	ch := publisher.Subscribe(r.Context())
	for batch := range ch {
		if err := conn.WriteJSON(batch); err != nil {
			return
		}
	}
}

func (a *App) nodeStatusSnapshot(ctx context.Context, hostID, hostName string) nodetransport.NodeStatus {
	now := time.Now().UTC()
	var systemFacts *remoteobservation.SystemFacts
	if a.remoteObservation != nil {
		facts := a.remoteObservation.LocalSystemFacts(ctx)
		systemFacts = &facts
	}
	return nodetransport.NodeStatus{
		HostID:    hostID,
		Name:      hostName,
		Reachable: true,
		Agent: model.AgentRuntime{
			Installed: true,
			Version:   agentAPIVersion,
			Health:    model.AgentHealthHealthy,
			Reachable: true,
		},
		Deployments: a.managedRuntimeInstances(ctx, hostID, hostName),
		Managed:     a.managedDeploymentStatusSnapshot(),
		System:      systemFacts,
		// DesktopOnline 反映本机是否也开着桌面端（见 nodetransport.NodeStatus 字段注释）。
		DesktopOnline: a.localDesktopOnline(),
		UpdatedAt:     now,
	}
}

// managedRuntimeInstances 组装本 agent 本机全部已注册项目的实例事实。
//
// 边界：本函数只报告本机观察到的实例和端口，不做 deployment 的归属或管辖判断；
// 关系判断由持有关系的控制面在 portmirror.computeExpected 中完成。
func (a *App) managedRuntimeInstances(ctx context.Context, hostID, hostName string) []model.InstanceStatus {
	projects := a.localProjectsSnapshot()
	out := []model.InstanceStatus{}
	service := a.runtimeStatusService()
	withPorts := 0
	for _, project := range projects {
		resp := service.Snapshot(ctx, project)
		for _, env := range resp.Environments {
			for _, inst := range env.Instances {
				if len(inst.Ports) > 0 {
					withPorts++
				}
				inst.NodeID = hostID
				inst.NodeName = hostName
				inst.IsLocal = false
				out = append(out, inst)
			}
		}
	}
	// 高频路径（随节点状态帧每 5s 一次），故降到 Debug：
	// 排查「端口镜像建立不起来」时，这一条区分「帧里没有这个实例」与
	// 「帧里有但端口为空」——两者的修复方向完全不同。
	logger.GetLogger().WithEntryName("NodeStatus").WithFields(map[string]any{
		"instance_count": len(out),
		"with_ports":     withPorts,
	}).Debug("节点帧实例组装完成")
	return out
}

// localProjectsSnapshot 返回本 agent 已注册的全部项目快照。
//
// 为什么不再只返回 managedProjectIDs（控制面下发合成的那批）：节点帧的语义是
// 「我本机在跑什么、监听哪些端口」，而不是「我替某个控制面跑什么」。归属转移
// 把项目在目标机注册为普通本地项目，旧口径下它连帧都进不去，端口镜像对
// 「归属式开发机」因此完全失效（F2 根因之二）。
//
// 「这条实例归谁管」是关系判断，由持有关系的一侧（控制面）在
// portmirror.computeExpected 里过滤，不在这里做。
func (a *App) localProjectsSnapshot() []model.Project {
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]model.Project, 0, len(a.projects))
	out = append(out, a.projects...)
	return out
}

func (a *App) managedDeploymentStatusSnapshot() *model.ManagedDeploymentStatus {
	a.mu.RLock()
	status := a.managedStatus
	a.mu.RUnlock()
	status.ActiveCollectorCount = remoteobservation.CountActiveCollectors(a.collector.List())
	if status.Collectors == nil {
		status.Collectors = []model.ManagedCollectorStatus{}
	}
	return &status
}
