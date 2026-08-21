// handler_services.go 实现服务运行时状态查询的 HTTP 处理器。
//
// 职责：
//   - 列出所有项目下所有服务及其 deployment 的运行时状态（Status、PID）
//
// 边界：
//   - 不直接操作子进程，通过 process.Manager 间接管理
//   - service 级启停/选择已下线，进程启停统一走 deployment 级接口
package api

import (
	"net/http"

	"github.com/xsxdot/super-dev/agent/model"
)

// listServices 处理 GET /api/services，返回所有项目的所有服务及其运行时状态。
//
// 使用单次 RLock 覆盖整个读取过程（服务快照 + manager 状态查询），
// 消除两次 RLock 之间的 TOCTOU 窗口。
func (a *App) listServices(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	result := make([]model.Service, 0)
	for _, p := range a.projects {
		mgr, hasMgr := a.managers[p.ID]
		for _, svc := range p.Services {
			legacyStatus := model.StatusStopped
			if hasMgr {
				st := mgr.Status(svc.ID)
				// 后台化命令会使 sh 退出、status 为空，但 session 内仍视为已启动
				if mgr.IsActive(svc.ID) && st != model.StatusStarting && st != model.StatusFailed {
					st = model.StatusRunning
				}
				legacyStatus = st
				svc.PID = mgr.PID(svc.ID)
			}
			// 补全每个 deployment 的运行时状态。归属他机的 dev deployment 必须
			// 先于本机采样判定：它的进程不在本机，问本机进程管理器只会得到
			// stopped，而侧边栏的启停按钮、底栏、服务级聚合读的都是这个字段。
			for j := range svc.Deployments {
				if st, ok := a.homedDeploymentStatus(p, svc.Deployments[j]); ok {
					svc.Deployments[j].Status = st
					// PID 是本机进程号，对不在本机的进程没有意义，留零而不是
					// 填一个会被误当成本机 PID 的值。
					svc.Deployments[j].PID = 0
					continue
				}
				if !hasMgr {
					continue
				}
				depID := svc.Deployments[j].ID
				dst := mgr.DeploymentStatus(depID)
				if mgr.IsDeploymentActive(depID) && dst != model.StatusStarting && dst != model.StatusFailed {
					dst = model.StatusRunning
				}
				svc.Deployments[j].Status = dst
				svc.Deployments[j].PID = mgr.DeploymentPID(depID)
			}
			if len(svc.Deployments) > 0 {
				svc.Status = aggregateDeploymentStatus(svc.Deployments)
			} else {
				svc.Status = legacyStatus
			}
			result = append(result, svc)
		}
	}
	a.mu.RUnlock()

	jsonOK(w, result)
}

// homedDeploymentStatus 返回归属他机的 dev deployment 的运行态——取自归属机
// 上报的节点帧，而不是本机进程管理器。
//
// 参数：
//   - project: deployment 所属项目（查归属 + dev 环境口径）
//   - dep: 待判定的 deployment
//
// 返回：
//   - status: 归属机帧里该实例的运行态映射结果
//   - ok: false 表示该 deployment 归属本机（或不是 dev 环境），调用方应走本机采样
//
// 为什么必须有这一支：location:local 说的是「跑在持有这个项目 dev 环境的那台
// 机器上」，归属转移之后那台机器已经不是本机。真机实测——服务在 linux-01 上
// 跑着、节点帧里 health=running，侧边栏却一直显示未运行、按钮停在「启动」，
// 因为这里问的是本机进程管理器，而本机根本没有这个进程。
//
// 复用 homeRouteTargetForDeployment：与 start/stop 转发、runtime-status 快照
// 共用同一套归属口径（只认 dev 环境，prod 拓扑由自身 Location/HostIDs 钉死）。
// 这已经是回答「这个 deployment 归谁跑」的第三处，三处必须同源，否则会漂移成
// 「能启动、能看运行态，唯独侧边栏说没起」。
//
// 归属机帧拿不到时（节点未上报/不可达）返回 unknown 对应的 stopped：调用方
// 拿到的是「本机看不到它在跑」，这与事实一致——本机确实看不到。
func (a *App) homedDeploymentStatus(project model.Project, dep model.Deployment) (model.ServiceStatus, bool) {
	homeHostID := a.homeRouteTargetForDeployment(project, dep.EnvName)
	if homeHostID == "" {
		return "", false
	}
	if a.nodeRegistry == nil {
		return model.StatusStopped, true
	}
	node, ok := a.nodeRegistry.SnapshotOf(homeHostID)
	if !ok || !node.Reachable {
		return model.StatusStopped, true
	}
	for _, inst := range node.Deployments {
		if inst.DeploymentID == dep.ID {
			return runtimeHealthToServiceStatus(inst.Metrics.Health), true
		}
	}
	return model.StatusStopped, true
}

// runtimeHealthToServiceStatus 把节点帧的 Health 映射成 deployment.Status。
//
// 注意：restarting 归到 starting 而不是 running——界面据此展示「启动中」的
// 中间态，把它说成 running 会让一次重启看起来从没发生过。
func runtimeHealthToServiceStatus(health model.Health) model.ServiceStatus {
	switch health {
	case model.HealthRunning, model.HealthHealthy:
		return model.StatusRunning
	case model.HealthRestarting:
		return model.StatusStarting
	case model.HealthFailed:
		return model.StatusFailed
	default:
		return model.StatusStopped
	}
}

func aggregateDeploymentStatus(deployments []model.Deployment) model.ServiceStatus {
	hasRunning := false
	hasStarting := false
	for _, dep := range deployments {
		switch dep.Status {
		case model.StatusFailed:
			return model.StatusFailed
		case model.StatusRunning:
			hasRunning = true
		case model.StatusStarting:
			hasStarting = true
		}
	}
	if hasRunning {
		return model.StatusRunning
	}
	if hasStarting {
		return model.StatusStarting
	}
	return model.StatusStopped
}
