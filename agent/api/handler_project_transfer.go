// handler_project_transfer.go 实现项目归属转移的预检 HTTP 处理器。
//
// 职责：
//   - POST /api/projects/{id}/transfer/preflight：只读探测本机 git 状态、
//     该项目在 dev 环境下是否有运行中的部署、目标机 git 检出状态，汇总成
//     blockers（必须先处理）/ready（就绪确认项）两份清单
//   - 计算 target_dir 留空时的默认值
//
// 边界：
//   - 纯只读探测，不执行任何写操作——不 push/pull/clone/checkout、不停止
//     任何部署。真正执行转移（含停止 dev 部署、EnsureCheckout）是 Task 5
//     的 execute 端点职责，本文件只回答"现在能不能转、转之前要先处理什么"
//   - 不解析 target_dir 的 "~" 前缀，原样透传给目标机 shell 展开——本机
//     不知道目标机的 $HOME
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/gitinfo"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/process"
)

// transferRemoteRunner 是目标机 git 探测的测试注入 seam。
//
// 测试直接把它设为按 cmd 前缀返回 canned 输出的假件（见
// handler_project_transfer_test.go），绕开真实网络/SSH。生产路径下保持
// nil，resolveTransferRunner 会转而调用 buildTransferRemoteRunner 现场绑定
// 真实的 pipeline.RoutingRunner 适配器。
//
// 之所以是一个不带 host 参数的包级变量而不是「按 host 建 Runner 的工厂」：
// gitinfo.Runner 的签名本身就是 (ctx, cmd, workDir)，不携带目标 host——这是
// Task 3 定的契约，本任务不重新定义它。测试只关心按 cmd 前缀分发 canned
// 输出，不关心 host，直接赋值即可；生产路径的 host 绑定通过
// buildTransferRemoteRunner 在每次请求时现场闭包完成，两者互不冲突。
var transferRemoteRunner gitinfo.Runner

// transferPreflightRequest 是 POST .../transfer/preflight 的请求体。
type transferPreflightRequest struct {
	HostID    string `json:"host_id"`
	TargetDir string `json:"target_dir"`
}

// transferPreflightResponse 是转移预检的结果，Task 5（execute）/ Task 11
// （前端）消费。
type transferPreflightResponse struct {
	Blockers  []transferCheckItem `json:"blockers"`   // 需要先处理，任一存在则不可执行转移
	Ready     []transferCheckItem `json:"ready"`      // 就绪确认项（含需人审的复用提示）
	TargetDir string              `json:"target_dir"` // 回显实际生效的目标目录
	Branch    string              `json:"branch"`     // 本机当前分支（执行时目标机检出同一分支）
}

// transferCheckItem 是预检的单条检查结果。
//
// Code 是稳定码，供前端/上游按类型分支处理，取值见各构造点注释：
// uncommitted / unpushed / running_dev / no_upstream / not_a_git_repo /
// no_git_binary / checkout_reuse / checkout_clone / remote_url_mismatch。
type transferCheckItem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// transferPreflight 处理 POST /api/projects/{id}/transfer/preflight。
//
// 参数：
//   - 路径参数 id：项目 ID
//   - 请求体 transferPreflightRequest：host_id 必填；target_dir 留空时按
//     "~/workspace/<项目目录名>" 取默认值
//
// 返回：
//   - 200: transferPreflightResponse
//   - 400: host_id 缺失，或目标 host 是本机自身 / 未开启 DevMachineMode
//   - 404: 项目或 host 不存在
//   - 500: 本机/目标机 git 探测发生基础设施故障（ctx 超时、Runner 传输层错误等）
//
// 注意：
//   - 纯只读：不停止部署、不 push/pull/clone，只如实报告现状
func (a *App) transferPreflight(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	var req transferPreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.HostID) == "" {
		jsonError(w, http.StatusBadRequest, "host_id is required")
		return
	}

	// 单次 RLock 覆盖项目快照 + manager 查询，避免 TOCTOU 窗口（同 listServices 的做法）。
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	mgr, hasMgr := a.managers[projectID]
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	host, ok := a.resolveTransferTargetHost(w, req.HostID)
	if !ok {
		return
	}

	timeout := a.runtimeStatusRequestTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	local, err := gitinfo.Inspect(ctx, project.RootPath)
	if err != nil {
		log.Printf("[SuperDev] transfer preflight: 本机 git 探测失败 project=%s err=%v", projectID, err)
		jsonError(w, http.StatusInternalServerError, "local git inspect failed: "+err.Error())
		return
	}

	// 显式初始化为非 nil 空切片（而非 var 声明的 nil 切片）：一切正常（无阻塞项）
	// 是预检最常见的"全绿"结果，此时若用 nil 切片，json.Marshal 会编码成
	// `null` 而不是 `[]`。前端 TS 类型把 blockers/ready 声明为非空数组
	// （TransferPreflightResponse），拿到 null 后 `.length`/`.map(...)` 会直接
	// TypeError 崩掉——这恰恰是最常见的"仓库干净、无 dev 部署运行"路径，
	// 属于 happy path 崩溃，必须从根上保证空集合也编码成 `[]`。
	blockers := []transferCheckItem{}
	ready := []transferCheckItem{}
	blockers = append(blockers, localGitBlockers(local)...)
	blockers = append(blockers, runningDevBlockers(project, mgr, hasMgr)...)

	targetDir := strings.TrimSpace(req.TargetDir)
	if targetDir == "" {
		targetDir = defaultTransferTargetDir(project.RootPath)
	}

	target := pipeline.Target{HostID: host.ID, HostName: host.Name}
	runner := a.resolveTransferRunner(target)
	if runner == nil {
		// 理论上不会发生：resolveTransferRunner 生产路径总能现场构造一个闭包；
		// 留这道防线只是为了不让 nil Runner 直接 panic 到 InspectRemote 内部。
		log.Printf("[SuperDev] transfer preflight: 远端探测 Runner 未装配 project=%s host=%s", projectID, host.ID)
		jsonError(w, http.StatusInternalServerError, "remote runner not configured")
		return
	}
	remoteProbe, err := gitinfo.InspectRemote(ctx, runner, targetDir)
	if err != nil {
		log.Printf("[SuperDev] transfer preflight: 目标机 git 探测失败 project=%s host=%s err=%v", projectID, host.ID, err)
		jsonError(w, http.StatusInternalServerError, "remote git inspect failed: "+err.Error())
		return
	}

	switch {
	case !remoteProbe.DirExists:
		// 只有本机确实有 RemoteURL 可供 clone 时才报 ready=checkout_clone——
		// 本机非仓库（not_a_git_repo）或没配上游（no_upstream）时 local.RemoteURL
		// 同样可能为空，此时已有对应的硬 blocker 覆盖根因，若还照样报
		// "目标可以直接 clone" 会自相矛盾（一边红一边绿，Task 5 也没有
		// URL 可 clone）。此处刻意不落入 default 追加 remote_url_mismatch：
		// 目标目录本就不存在，谈不上"地址不一致"，只是"当前不能判定 ready"。
		if local.RemoteURL != "" {
			ready = append(ready, transferCheckItem{
				Code:   "checkout_clone",
				Detail: "目标目录不存在，转移执行时将 git clone 到该路径",
			})
		}
	case remoteProbe.IsRepo && local.RemoteURL != "" && remoteProbe.RemoteURL == local.RemoteURL:
		// 要求本机/目标机的 RemoteURL 都非空才判定"同源"：两边都为空时无法证明
		// 目标目录检出的就是同一个仓库，冒着复用错误代码库的风险不如让人工确认，
		// 因此落入下面的 default 分支报 remote_url_mismatch。
		ready = append(ready, transferCheckItem{
			Code:   "checkout_reuse",
			Detail: "目标目录已是本机仓库的同源检出（远端地址一致），转移执行时将 fetch + pull 到最新提交",
		})
	default:
		// 秘密红线：RemoteURL 可能内嵌凭据（https://user:token@host/... 或
		// https://ghp_xxx@host/...），这条 Detail 会原样进入 HTTP 响应体，
		// 绝不能把凭据原文吐给调用方——用 stripURLCredentials（Task 5 在
		// project_transfer_engine.go 里已为 clone 路径加过同款红线摘除）
		// 把两个 URL 都摘除 userinfo 后再拼进人读文案。
		blockers = append(blockers, transferCheckItem{
			Code:   "remote_url_mismatch",
			Detail: fmt.Sprintf("目标目录已存在但不是本机仓库的同源检出（目标远端=%q，本机远端=%q）", stripURLCredentials(remoteProbe.RemoteURL), stripURLCredentials(local.RemoteURL)),
		})
	}

	// 只打一条摘要日志（数量 + host），不逐项打——预检结果本身已经通过 HTTP 响应体
	// 完整返回，逐项打日志只会在高频探测下刷屏。
	log.Printf("[SuperDev] transfer preflight: project=%s host=%s blockers=%d ready=%d", projectID, host.ID, len(blockers), len(ready))

	jsonOK(w, transferPreflightResponse{
		Blockers:  blockers,
		Ready:     ready,
		TargetDir: targetDir,
		Branch:    local.Branch,
	})
}

// transferExecTimeout 是一次转移/迁回后台执行的整体超时上限。
// clone 大仓库可能很慢，但也不能永久挂起——30 分钟是「长命令」与「卡死」的分界。
const transferExecTimeout = 30 * time.Minute

// startProjectTransfer 处理 POST /api/projects/{id}/transfer：校验后异步启动
// 一次正向转移，立即 202 返回初始状态；同项目已有进行中的转移则 409。
//
// 请求体同 preflight（host_id 必填、target_dir 可选）。分支与 clone 源地址
// 由本机 git 快照现场解析，不信任客户端传入，避免检出到错误分支/仓库。
func (a *App) startProjectTransfer(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	var req transferPreflightRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.HostID) == "" {
		jsonError(w, http.StatusBadRequest, "host_id is required")
		return
	}

	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	host, ok := a.resolveTransferTargetHost(w, req.HostID)
	if !ok {
		return
	}

	targetDir := strings.TrimSpace(req.TargetDir)
	if targetDir == "" {
		targetDir = defaultTransferTargetDir(project.RootPath)
	}

	// 本机 git 快照：确定检出分支与 clone 源地址。这两项是执行的硬前提，
	// preflight 已把 not_a_git_repo / no_upstream 报成 blocker，此处再兜一次底，
	// 避免绕过 preflight 直接 execute 时把无效转移放进后台。
	timeout := a.runtimeStatusRequestTimeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	local, err := gitinfo.Inspect(ctx, project.RootPath)
	if err != nil {
		log.Printf("[SuperDev] transfer execute: 本机 git 探测失败 project=%s err=%v", projectID, err)
		jsonError(w, http.StatusInternalServerError, "local git inspect failed: "+err.Error())
		return
	}
	if !local.IsRepo {
		jsonError(w, http.StatusBadRequest, "项目根目录不是 git 仓库，无法转移")
		return
	}
	if local.RemoteURL == "" {
		jsonError(w, http.StatusBadRequest, "本机未配置 origin，目标机无法从远端 clone，无法转移")
		return
	}
	if local.Branch == "" {
		jsonError(w, http.StatusBadRequest, "本机处于 detached HEAD，无法确定要检出的分支")
		return
	}

	// 摘除 origin URL 上内嵌的凭据：目标机用它自己的 git 访问权限 clone，控制面的
	// 凭据绝不能随 clone URL 搬到目标机（详见 stripURLCredentials 的红线说明）。
	cleanRepoURL := stripURLCredentials(local.RemoteURL)
	if cleanRepoURL != local.RemoteURL {
		// 只提示发生了摘除，绝不打印任何凭据值。
		log.Printf("[SuperDev] transfer execute: 已从 origin URL 摘除内嵌凭据 project=%s", projectID)
	}

	run := newTransferRun(projectID, host.ID, host.Name, targetDir, local.Branch, cleanRepoURL)
	if !a.beginTransfer(projectID, run) {
		jsonError(w, http.StatusConflict, "该项目已有进行中的转移")
		return
	}

	bg, bgCancel := context.WithTimeout(context.Background(), transferExecTimeout)
	go func() {
		defer bgCancel()
		a.executeProjectTransfer(bg, run)
	}()

	jsonWrite(w, http.StatusAccepted, run.snapshot())
}

// getProjectTransferStatus 处理 GET /api/projects/{id}/transfer/status：
// 返回内存中进行中/最近一次转移的状态；无任何记录（含进程重启后）→ 404。
func (a *App) getProjectTransferStatus(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	a.transferMu.Lock()
	run, ok := a.projectTransfers[projectID]
	a.transferMu.Unlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "no transfer in progress or history")
		return
	}
	jsonOK(w, run.snapshot())
}

// startProjectTransferBack 处理 POST /api/projects/{id}/transfer-back：异步启动
// 一次迁回（归属机 → 本机）。归属机由 projectHomeStore 反查得到；项目已归属
// 本机则 400。请求体可选 target_dir（归属机侧项目路径，留空取默认）。
func (a *App) startProjectTransferBack(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	// 请求体可选：只用 target_dir（归属机侧路径）；host 由归属反查，不由客户端指定。
	var req transferPreflightRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	homeHostID := ""
	if a.projectHomeStore != nil {
		homeHostID = a.projectHomeStore.HomeOf(projectID)
	}
	if homeHostID == "" {
		jsonError(w, http.StatusBadRequest, "项目已归属本机，无需迁回")
		return
	}

	// 归属机展示名尽力而为：记录已被删除时留空，不阻断迁回（仍能探测/清除归属）。
	hostName := homeHostID
	if host, found, err := hostByID(a.remoteStore, homeHostID); err == nil && found {
		hostName = host.Name
	}

	targetDir := strings.TrimSpace(req.TargetDir)
	if targetDir == "" {
		targetDir = defaultTransferTargetDir(project.RootPath)
	}

	run := newTransferRun(projectID, homeHostID, hostName, targetDir, "", "")
	if !a.beginTransfer(projectID, run) {
		jsonError(w, http.StatusConflict, "该项目已有进行中的转移")
		return
	}

	bg, bgCancel := context.WithTimeout(context.Background(), transferExecTimeout)
	go func() {
		defer bgCancel()
		a.executeProjectTransferBack(bg, run)
	}()

	jsonWrite(w, http.StatusAccepted, run.snapshot())
}

// beginTransfer 登记一次转移的内存态：同项目已有进行中的转移（state==running）
// 时返回 false（调用方回 409）；否则登记（覆盖上一次已结束的记录）并返回 true。
func (a *App) beginTransfer(projectID string, run *transferRun) bool {
	a.transferMu.Lock()
	defer a.transferMu.Unlock()
	if existing, ok := a.projectTransfers[projectID]; ok {
		if existing.snapshot().State == transferStateRunning {
			return false
		}
	}
	a.projectTransfers[projectID] = run
	return true
}

// resolveTransferTargetHost 解析并校验转移目标 host，校验失败时直接写入
// HTTP 错误响应并返回 ok=false。
//
// 目标 host 必须：
//   - 不是本机自身（hostID == a.identity.NodeID）——转移的意义就是切到
//     另一台开发机，切到本机没有意义
//   - 存在于 remoteStore
//   - 已开启 DevMachineMode——转移后项目由目标机作为"开发机"消费，未开启
//     该开关的主机不参与端口镜像等下游链路，转过去也无法正常工作
func (a *App) resolveTransferTargetHost(w http.ResponseWriter, hostID string) (model.Host, bool) {
	if hostID == a.identity.NodeID {
		jsonError(w, http.StatusBadRequest, "转移目标不能是本机")
		return model.Host{}, false
	}
	host, found, err := hostByID(a.remoteStore, hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "读取 host 列表失败: "+err.Error())
		return model.Host{}, false
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return model.Host{}, false
	}
	if !host.DevMachineMode {
		jsonError(w, http.StatusBadRequest, "目标 host 未开启 DevMachineMode，不能作为转移目标")
		return model.Host{}, false
	}
	return host, true
}

// localGitBlockers 依据本机 git 探测结果构造预检阻塞项。
//
// 为什么 no_upstream 是 blocker：转移依赖"推送→目标机拉取"这一条通路——
// 本机代码必须先推到 origin，目标机才能从 origin 拉到最新代码；一旦当前
// 分支没有配置上游（Ahead==-1），连"往哪推"都不明确，这条通路从源头就不
// 成立，必须先在本机执行一次 git push -u 配置好上游，不能指望转移执行阶段
// 替用户兜底猜测目标分支。
func localGitBlockers(local gitinfo.Snapshot) []transferCheckItem {
	if !gitBinaryAvailable() {
		// git 二进制缺失和"目录不是仓库"是两种截然不同的处置方式（装 git vs
		// 初始化仓库），gitinfo.Inspect 内部把两者都降级成 IsRepo=false，
		// 这里额外做一次独立探测才能把这两种情况在预检响应里区分开。
		return []transferCheckItem{{
			Code:   "no_git_binary",
			Detail: "本机未找到 git 可执行文件，无法探测仓库状态，请先安装 git",
		}}
	}
	if !local.IsRepo {
		return []transferCheckItem{{
			Code:   "not_a_git_repo",
			Detail: "项目根目录不是 git 仓库，无法转移",
		}}
	}

	var blockers []transferCheckItem
	if local.Dirty {
		blockers = append(blockers, transferCheckItem{
			Code:   "uncommitted",
			Detail: "本机存在未提交的变更，请先提交或暂存后再转移",
		})
	}
	switch {
	case local.Ahead > 0:
		blockers = append(blockers, transferCheckItem{
			Code:   "unpushed",
			Detail: fmt.Sprintf("本机有 %d 个提交尚未推送到远端", local.Ahead),
		})
	case local.Ahead == -1:
		blockers = append(blockers, transferCheckItem{
			Code:   "no_upstream",
			Detail: "当前分支未配置上游分支，无法确认代码可从远端拉取",
		})
	}
	return blockers
}

// runningDevBlockers 汇总项目在 dev 环境下仍处于活跃态（运行中或刚启动、
// 就绪探测尚未走完）的 deployment。
//
// 用 mgr.IsDeploymentActive 而非字面的 DeploymentStatus==running：
// StatusStarting 是"进程已经拉起、就绪探测还没过"的中间态，此时进程已经
// 占用端口/资源，转移执行阶段一样需要先停掉它——若只认 StatusRunning，
// 一个刚启动尚在探测中的 dev 部署会被预检漏报，用户会在"预检通过"之后
// 才发现执行阶段替他停了一个他不知道在跑的东西。宁可预检多报一个即将
// 转正的活跃部署（安全方向），也不要漏报一个真实占用中的进程
// （IsDeploymentActive 语义见 agent/process/manager.go:IsActive：
// status 为 starting/running，或 runner 仍在 runners 表里即视为活跃，
// 与 handler_services.go 用于状态展示归一化的同一个信号来源一致）。
func runningDevBlockers(project model.Project, mgr *process.Manager, hasMgr bool) []transferCheckItem {
	if !hasMgr {
		return nil
	}
	devEnvs := make(map[string]bool, len(project.Environments))
	for _, env := range project.Environments {
		if env.IsDev {
			devEnvs[env.Name] = true
		}
	}
	if len(devEnvs) == 0 {
		return nil
	}

	var running []string
	for _, svc := range project.Services {
		for _, dep := range svc.Deployments {
			if !devEnvs[dep.EnvName] {
				continue
			}
			if mgr.IsDeploymentActive(dep.ID) {
				running = append(running, svc.Name+"("+dep.EnvName+")")
			}
		}
	}
	if len(running) == 0 {
		return nil
	}
	return []transferCheckItem{{
		Code:   "running_dev",
		Detail: fmt.Sprintf("%d 个 dev 环境部署正在运行：%s，转移执行时会先停止", len(running), strings.Join(running, "、")),
	}}
}

// defaultTransferTargetDir 计算 target_dir 留空时的默认值。
//
// 波浪号原样保留，不在本机展开——本机不知道目标机的 $HOME 是什么，展开
// 语义必须交给目标机 shell 在实际执行阶段完成。
func defaultTransferTargetDir(projectRootPath string) string {
	return "~/workspace/" + filepath.Base(projectRootPath)
}

// gitBinaryAvailable 检查本机 PATH 上是否存在 git 可执行文件。
// 与 gitinfo 包内部的同名私有检查逻辑一致，但预检需要把这个信号单独暴露出来
// 才能构造 no_git_binary 这个独立于 not_a_git_repo 的稳定码。
func gitBinaryAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// resolveTransferRunner 返回本次预检要使用的远端 Runner。
//
// transferRemoteRunner 非 nil 时优先使用（测试注入路径，见文件顶部变量注释）；
// 否则现场绑定 pipeline.RoutingRunner，构造一个只对 target 这一台 host 生效
// 的生产闭包。
func (a *App) resolveTransferRunner(target pipeline.Target) gitinfo.Runner {
	if transferRemoteRunner != nil {
		return transferRemoteRunner
	}
	return a.buildTransferRemoteRunner(target)
}

// buildTransferRemoteRunner 把 pipeline.RoutingRunner.RunRemote 适配成
// gitinfo.Runner 的三态契约（stdout/exitCode/err）。
//
// 参数：
//   - target: 已解析好的目标 host（HostID/HostName），由 transferPreflight
//     按请求的 host_id 解析得到；每次调用都现场构造 RoutingRunner，与
//     newPipelineEngine/newRemoteRuntimeController 的既有装配方式一致
//
// 注意：
//   - RunRemote 只用一个 error 表达结果，不直接暴露退出码；命令已在目标机
//     跑完但非零退出时，err 的动态类型会实现 ExitCode() int（如
//     pipeline.CommandExitError），必须用 errors.As 从错误链上取出这个退出码
//     并把 err 归零——这是"探测到的确凿事实"（如目录非仓库），不是传输层故障。
//   - 其余非 nil err（SSH/隧道连不通等）原样透传，交给 InspectRemote 整体
//     上抛，不能被误判成"目录不存在/不是仓库"这类确凿的探测事实。
func (a *App) buildTransferRemoteRunner(target pipeline.Target) gitinfo.Runner {
	return func(ctx context.Context, cmd, _ string) (string, int, error) {
		sshExecutor := pipeline.NewSSHExecutor(func(hostID string) (model.Host, bool) {
			host, found, err := hostByID(a.remoteStore, hostID)
			if err != nil {
				return model.Host{}, false
			}
			return host, found
		})
		agentRunner := a.pipelineAgentRunner
		if agentRunner == nil {
			agentRunner = pipeline.NewAgentRunner(a.nodeTransport)
		}
		runner := pipeline.NewRoutingRunner(a.agentHealth, agentRunner, sshExecutor)

		var lines []string
		runErr := runner.RunRemote(ctx, target, cmd, "", func(line, stream string) {
			if stream == "stdout" {
				lines = append(lines, line)
			}
		})
		stdout := strings.Join(lines, "\n")

		var exitErr interface{ ExitCode() int }
		if errors.As(runErr, &exitErr) {
			return stdout, exitErr.ExitCode(), nil
		}
		return stdout, 0, runErr
	}
}
