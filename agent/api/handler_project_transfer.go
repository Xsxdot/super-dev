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

	var blockers, ready []transferCheckItem
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
		ready = append(ready, transferCheckItem{
			Code:   "checkout_clone",
			Detail: "目标目录不存在，转移执行时将 git clone 到该路径",
		})
	case remoteProbe.IsRepo && local.RemoteURL != "" && remoteProbe.RemoteURL == local.RemoteURL:
		// 要求本机/目标机的 RemoteURL 都非空才判定"同源"：两边都为空时无法证明
		// 目标目录检出的就是同一个仓库，冒着复用错误代码库的风险不如让人工确认，
		// 因此落入下面的 default 分支报 remote_url_mismatch。
		ready = append(ready, transferCheckItem{
			Code:   "checkout_reuse",
			Detail: "目标目录已是本机仓库的同源检出（远端地址一致），转移执行时将 fetch + pull 到最新提交",
		})
	default:
		blockers = append(blockers, transferCheckItem{
			Code:   "remote_url_mismatch",
			Detail: fmt.Sprintf("目标目录已存在但不是本机仓库的同源检出（目标远端=%q，本机远端=%q）", remoteProbe.RemoteURL, local.RemoteURL),
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

// runningDevBlockers 汇总项目在 dev 环境下仍在运行的 deployment。
//
// 转移执行阶段会自动先停止这些 deployment 再切换归属，预检只负责如实报告
// 现状供人工确认，不在预检阶段做任何停止操作（纯只读契约）。
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
			if mgr.DeploymentStatus(dep.ID) == model.StatusRunning {
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
