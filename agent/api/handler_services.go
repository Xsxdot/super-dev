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

	"github.com/superdev/agent/model"
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
			if hasMgr {
				st := mgr.Status(svc.ID)
				// 后台化命令会使 sh 退出、status 为空，但 session 内仍视为已启动
				if mgr.IsActive(svc.ID) && st != model.StatusStarting && st != model.StatusFailed {
					st = model.StatusRunning
				}
				svc.Status = st
				svc.PID = mgr.PID(svc.ID)
				// 补全每个 deployment 的运行时状态
				for j := range svc.Deployments {
					depID := svc.Deployments[j].ID
					dst := mgr.DeploymentStatus(depID)
					if mgr.IsDeploymentActive(depID) && dst != model.StatusStarting && dst != model.StatusFailed {
						dst = model.StatusRunning
					}
					svc.Deployments[j].Status = dst
					svc.Deployments[j].PID = mgr.DeploymentPID(depID)
				}
			}
			result = append(result, svc)
		}
	}
	a.mu.RUnlock()

	jsonOK(w, result)
}
