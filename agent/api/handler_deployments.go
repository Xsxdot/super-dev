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
	"fmt"
	"net/http"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

// startDeployment 处理 POST /api/deployments/{id}/start。
func (a *App) startDeployment(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStart, "starting", "")
}

// stopDeployment 处理 POST /api/deployments/{id}/stop。
func (a *App) stopDeployment(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStop, "stopped", "")
}

// restartDeployment 处理 POST /api/deployments/{id}/restart。
func (a *App) restartDeployment(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeRestart, "starting", "")
}

// startDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/start。
func (a *App) startDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStart, "starting", r.PathValue("host_id"))
}

// stopDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/stop。
func (a *App) stopDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeStop, "stopped", r.PathValue("host_id"))
}

// restartDeploymentHost 处理 POST /api/deployments/{id}/hosts/{host_id}/restart。
func (a *App) restartDeploymentHost(w http.ResponseWriter, r *http.Request) {
	a.controlDeploymentRuntime(w, r, operation.OperationRuntimeRestart, "starting", r.PathValue("host_id"))
}

func (a *App) controlDeploymentRuntime(w http.ResponseWriter, r *http.Request, kind string, okStatus string, hostID string) {
	depID := r.PathValue("id")
	dep, svc, p, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	runDep := dep
	var err error
	var plan operation.Plan
	if strings.TrimSpace(hostID) == "" {
		plan, err = operation.PlanRuntime(kind, p, svc, dep)
	} else {
		runDep, err = deploymentScopedToHost(dep, hostID)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		plan, err = operation.PlanRuntimeOnHost(kind, p, svc, runDep, hostID)
	}
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := a.runDeploymentRuntimeAction(r.Context(), p.ID, runDep, kind); err != nil {
		action := runtimeActionLabel(kind)
		a.appendOperationExecutionFailure(r, plan, approval, "failed to "+action+" deployment: "+err.Error())
		jsonError(w, http.StatusInternalServerError, "failed to "+action+" deployment: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": okStatus})
}

func (a *App) runDeploymentRuntimeAction(ctx context.Context, projectID string, dep model.Deployment, kind string) error {
	switch kind {
	case operation.OperationRuntimeStart:
		return a.startDeploymentRuntime(ctx, projectID, dep)
	case operation.OperationRuntimeStop:
		return a.stopDeploymentRuntime(ctx, projectID, dep)
	case operation.OperationRuntimeRestart:
		return a.restartDeploymentRuntime(ctx, projectID, dep)
	default:
		return operation.ErrInvalidOperation
	}
}

func runtimeActionLabel(kind string) string {
	switch kind {
	case operation.OperationRuntimeStart:
		return "start"
	case operation.OperationRuntimeStop:
		return "stop"
	case operation.OperationRuntimeRestart:
		return "restart"
	default:
		return "control"
	}
}

func deploymentScopedToHost(dep model.Deployment, hostID string) (model.Deployment, error) {
	hostID = strings.TrimSpace(hostID)
	if hostID == "" {
		return model.Deployment{}, fmt.Errorf("host_id is required")
	}
	if dep.Location != model.LocationRemote {
		return model.Deployment{}, fmt.Errorf("host-scoped runtime control requires remote deployment")
	}
	for _, candidate := range dep.HostIDs {
		if strings.TrimSpace(candidate) == hostID {
			scoped := dep
			scoped.HostIDs = []string{hostID}
			return scoped, nil
		}
	}
	return model.Deployment{}, fmt.Errorf("host %s is not configured for deployment %s", hostID, dep.ID)
}

func (a *App) startDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	if dep.Location == model.LocationRemote {
		return a.newRemoteRuntimeController().Start(ctx, dep)
	}
	mgr := a.getOrCreateManager(projectID)
	a.reconcileLocalDeployment(projectID, dep.ID)
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
	a.reconcileLocalDeployment(projectID, dep.ID)
	mgr.StopDeployment(dep.ID)
	a.pidStore.Remove(dep.ID)
	return a.pidStore.Flush()
}

func (a *App) restartDeploymentRuntime(ctx context.Context, projectID string, dep model.Deployment) error {
	if dep.Location == model.LocationRemote {
		return a.newRemoteRuntimeController().Restart(ctx, dep)
	}
	mgr := a.getOrCreateManager(projectID)
	a.reconcileLocalDeployment(projectID, dep.ID)
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
