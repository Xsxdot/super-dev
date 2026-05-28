// handler_deployments.go 实现 deployment 级进程控制 HTTP 处理器。
//
// 职责：
//   - 按 deployment ID 启动、停止、重启进程
//   - local deployment：用 deployment 自身的 command/workDir/env 启动
//   - remote deployment：IsReadOnly() 为 true 时返回 400
//
// 边界：
//   - 不直接操作子进程，通过 process.Manager.StartDeployment 系列方法
//   - 不感知 env 分组，路由层按 deploymentID 定位
package api

import (
	"net/http"

	"github.com/superdev/agent/model"
)

// startDeployment 处理 POST /api/deployments/{id}/start。
func (a *App) startDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, p, ok := a.findDeployment(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.IsReadOnly() {
		jsonError(w, http.StatusBadRequest, "deployment is read-only (no start command)")
		return
	}
	mgr := a.getOrCreateManager(p.ID)
	if err := mgr.StartDeployment(dep); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to start deployment: "+err.Error())
		return
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	_ = a.pidStore.Flush()
	jsonOK(w, map[string]string{"status": "starting"})
}

// stopDeployment 处理 POST /api/deployments/{id}/stop。
func (a *App) stopDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, p, ok := a.findDeployment(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	mgr := a.getOrCreateManager(p.ID)
	mgr.StopDeployment(dep.ID)
	a.pidStore.Remove(dep.ID)
	_ = a.pidStore.Flush()
	jsonOK(w, map[string]string{"status": "stopped"})
}

// restartDeployment 处理 POST /api/deployments/{id}/restart。
func (a *App) restartDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, p, ok := a.findDeployment(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	if dep.IsReadOnly() {
		jsonError(w, http.StatusBadRequest, "deployment is read-only (no start command)")
		return
	}
	mgr := a.getOrCreateManager(p.ID)
	if err := mgr.RestartDeployment(dep); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to restart deployment: "+err.Error())
		return
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	_ = a.pidStore.Flush()
	jsonOK(w, map[string]string{"status": "starting"})
}

// findDeployment 在所有项目的所有服务中按 deployment ID 查找。
//
// 注意：调用方无需持锁，此函数内部持有 RLock。
func (a *App) findDeployment(depID string) (model.Deployment, model.Project, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.projects {
		for _, svc := range p.Services {
			for _, dep := range svc.Deployments {
				if dep.ID == depID {
					return dep, p, true
				}
			}
		}
	}
	return model.Deployment{}, model.Project{}, false
}
