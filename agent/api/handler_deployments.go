// handler_deployments.go 实现 deployment 级进程控制 HTTP 处理器。
//
// 职责：
//   - 按 deployment ID 启动、停止、重启进程
//   - local deployment：用 deployment 自身的 command/workDir/env 启动
//   - 运行态写操作先经过 operation 安全门禁授权
//
// 边界：
//   - 不直接操作子进程，通过 process.Manager.StartDeployment 系列方法
//   - 不感知 env 分组，路由层按 deploymentID 定位
package api

import (
	"context"
	"net/http"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

// startDeployment 处理 POST /api/deployments/{id}/start。
func (a *App) startDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, svc, p, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	plan, err := operation.PlanRuntime(operation.OperationRuntimeStart, p, svc, dep)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := a.startDeploymentRuntime(r.Context(), p.ID, dep); err != nil {
		a.appendOperationExecutionFailure(r, plan, approval, "failed to start deployment: "+err.Error())
		jsonError(w, http.StatusInternalServerError, "failed to start deployment: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "starting"})
}

// stopDeployment 处理 POST /api/deployments/{id}/stop。
func (a *App) stopDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, svc, p, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	plan, err := operation.PlanRuntime(operation.OperationRuntimeStop, p, svc, dep)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := a.stopDeploymentRuntime(r.Context(), p.ID, dep); err != nil {
		a.appendOperationExecutionFailure(r, plan, approval, "failed to stop deployment: "+err.Error())
		jsonError(w, http.StatusInternalServerError, "failed to stop deployment: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "stopped"})
}

// restartDeployment 处理 POST /api/deployments/{id}/restart。
func (a *App) restartDeployment(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	dep, svc, p, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	plan, err := operation.PlanRuntime(operation.OperationRuntimeRestart, p, svc, dep)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := a.restartDeploymentRuntime(r.Context(), p.ID, dep); err != nil {
		a.appendOperationExecutionFailure(r, plan, approval, "failed to restart deployment: "+err.Error())
		jsonError(w, http.StatusInternalServerError, "failed to restart deployment: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "starting"})
}

func (a *App) startDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	if dep.Location == model.LocationRemote {
		return a.newRemoteRuntimeController().Start(ctx, dep)
	}
	mgr := a.getOrCreateManager(projectID)
	if err := mgr.StartDeployment(dep); err != nil {
		return err
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	return a.pidStore.Flush()
}

func (a *App) stopDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	if dep.Location == model.LocationRemote {
		if err := a.newRemoteRuntimeController().Stop(ctx, dep); err != nil {
			return err
		}
		a.pidStore.Remove(dep.ID)
		return a.pidStore.Flush()
	}
	mgr := a.getOrCreateManager(projectID)
	mgr.StopDeployment(dep.ID)
	a.pidStore.Remove(dep.ID)
	return a.pidStore.Flush()
}

func (a *App) restartDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	if dep.Location == model.LocationRemote {
		return a.newRemoteRuntimeController().Restart(ctx, dep)
	}
	mgr := a.getOrCreateManager(projectID)
	if err := mgr.RestartDeployment(dep); err != nil {
		return err
	}
	a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	return a.pidStore.Flush()
}

// findDeployment 在所有项目的所有服务中按 deployment ID 查找。
//
// 注意：调用方无需持锁，此函数内部持有 RLock。
func (a *App) findDeployment(depID string) (model.Deployment, model.Project, bool) {
	dep, _, project, ok := a.findDeploymentWithService(depID)
	return dep, project, ok
}

func (a *App) findDeploymentWithService(depID string) (model.Deployment, model.Service, model.Project, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.projects {
		for _, svc := range p.Services {
			for _, dep := range svc.Deployments {
				if dep.ID == depID {
					return dep, svc, p, true
				}
			}
		}
	}
	return model.Deployment{}, model.Service{}, model.Project{}, false
}
