// handler_services.go 实现服务进程管理相关的 HTTP 处理器。
//
// 职责：
//   - 列出所有项目下所有服务的运行时状态（Status、PID）
//   - 启动、停止、重启单个服务
//   - 按项目批量启动选中的服务（start-selected）
//
// 边界：
//   - 不直接操作子进程，通过 process.Manager 间接管理
//   - SelectedServiceIDs 存储的是服务名称（Name），不是 ID，匹配时按 Name 查找
package api

import (
	"net/http"

	"github.com/google/uuid"
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
			}
			result = append(result, svc)
		}
	}
	a.mu.RUnlock()

	jsonOK(w, result)
}

// startService 处理 POST /api/services/{id}/start，启动指定服务。
func (a *App) startService(w http.ResponseWriter, r *http.Request) {
	svcID := r.PathValue("id")

	svc, p, ok := a.findService(svcID)
	if !ok {
		jsonError(w, http.StatusNotFound, "service not found")
		return
	}

	mgr := a.getOrCreateManager(p.ID)
	if err := mgr.Start(svc); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to start service: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{"status": "starting"})
}

// stopService 处理 POST /api/services/{id}/stop，停止指定服务。
func (a *App) stopService(w http.ResponseWriter, r *http.Request) {
	svcID := r.PathValue("id")

	svc, p, ok := a.findService(svcID)
	if !ok {
		jsonError(w, http.StatusNotFound, "service not found")
		return
	}

	mgr := a.getOrCreateManager(p.ID)
	mgr.Stop(svc.ID)

	jsonOK(w, map[string]string{"status": "stopped"})
}

// restartService 处理 POST /api/services/{id}/restart，重启指定服务。
func (a *App) restartService(w http.ResponseWriter, r *http.Request) {
	svcID := r.PathValue("id")

	svc, p, ok := a.findService(svcID)
	if !ok {
		jsonError(w, http.StatusNotFound, "service not found")
		return
	}

	mgr := a.getOrCreateManager(p.ID)
	if err := mgr.Restart(svc); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to restart service: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{"status": "starting"})
}

// startSelected 处理 POST /api/projects/{id}/start-selected。
//
// 启动策略：
//   - 所有 Required=true 的服务必须启动
//   - SelectedServiceIDs 中列出的服务名称对应的服务也需启动
//   - 注意：SelectedServiceIDs 存的是服务名称（Name），不是 ID
func (a *App) startSelected(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")

	a.mu.RLock()
	p, ok := a.findProject(projectID)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	// 构建需要启动的服务集合：Required 服务 + SelectedServiceIDs 中的服务
	selectedNames := map[string]struct{}{}
	for _, name := range p.SelectedServiceIDs {
		selectedNames[name] = struct{}{}
	}

	var toStart []model.Service
	for _, svc := range p.Services {
		if svc.Required {
			toStart = append(toStart, svc)
			continue
		}
		// SelectedServiceIDs 存的是服务名称
		if _, selected := selectedNames[svc.Name]; selected {
			toStart = append(toStart, svc)
		}
	}

	mgr := a.getOrCreateManager(projectID)
	var toStartFiltered []model.Service
	for _, svc := range toStart {
		if !mgr.IsActive(svc.ID) {
			toStartFiltered = append(toStartFiltered, svc)
		}
	}
	if len(toStartFiltered) == 0 {
		jsonOK(w, map[string]string{"status": "already_running"})
		return
	}

	mgr.SetRunID(uuid.NewString())
	if err := mgr.StartGroup(toStartFiltered); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to start services: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{"status": "starting"})
}

// findService 在所有项目中按服务 ID 查找服务及其所属项目。
//
// 返回：
//   - 找到的服务、所属项目、是否找到
//
// 注意：调用方无需持锁，此函数内部持有 RLock。
func (a *App) findService(svcID string) (model.Service, model.Project, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, p := range a.projects {
		for _, svc := range p.Services {
			if svc.ID == svcID {
				return svc, p, true
			}
		}
	}
	return model.Service{}, model.Project{}, false
}
