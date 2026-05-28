// handler_projects.go 实现项目管理相关的 HTTP 处理器。
//
// 职责：
//   - 列出所有已注册项目
//   - 添加新项目（加载配置、分配 ID、写注册表）
//   - 删除项目（从内存和注册表移除）
//   - 读写项目的日志过滤规则
//
// 边界：
//   - 不直接操作进程，仅管理项目元数据
//   - 项目路径合法性由 config.Loader 验证（ErrNotFound）
package api

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/superdev/agent/config"
	"github.com/superdev/agent/model"
)

// jsonOK 将 v 序列化为 JSON 并以 200 状态码响应。
func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// jsonError 以指定状态码返回 JSON 错误信息。
func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// listProjects 处理 GET /api/projects，返回所有已注册项目列表。
func (a *App) listProjects(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	projects := make([]model.Project, len(a.projects))
	copy(projects, a.projects)
	a.mu.RUnlock()

	jsonOK(w, projects)
}

// addProject 处理 POST /api/projects，从请求体中读取 root_path，加载并注册项目。
//
// 请求体：{"root_path": "/path/to/project"}
// 成功响应：完整的 model.Project（含分配的 ID）
func (a *App) addProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RootPath string `json:"root_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RootPath == "" {
		jsonError(w, http.StatusBadRequest, "root_path is required")
		return
	}

	loader := config.NewLoader(req.RootPath)
	p, err := loader.Load()
	if err != nil {
		jsonError(w, http.StatusBadRequest, "failed to load project config: "+err.Error())
		return
	}

	// 分配 UUID（Loader 不负责 ID 分配）
	assignIDs(&p)

	// 持久化 ID，避免 agent 重启后 service ID 变化导致重复启动
	if err := loader.Save(p); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save project config: "+err.Error())
		return
	}

	// 写入注册表
	if err := a.registry.Add(req.RootPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to register project: "+err.Error())
		return
	}

	a.mu.Lock()
	a.projects = append(a.projects, p)
	a.mu.Unlock()

	jsonOK(w, p)
}

// deleteProject 处理 DELETE /api/projects/{id}，从注册表和内存中移除项目。
//
// 操作顺序：先持久化删除（registry.Remove），成功后再修改内存状态，
// 避免 registry 写失败时内存与磁盘状态不一致。
func (a *App) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// 先在读锁下找到 rootPath，不修改内存状态
	a.mu.RLock()
	var rootPath string
	for _, p := range a.projects {
		if p.ID == id {
			rootPath = p.RootPath
			break
		}
	}
	a.mu.RUnlock()

	if rootPath == "" {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	// 先执行持久化删除；若失败则内存状态保持不变，不产生 desync
	if err := a.registry.Remove(rootPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to remove project from registry: "+err.Error())
		return
	}

	// 持久化成功后，再修改内存状态并清理 manager
	a.mu.Lock()
	newProjects := make([]model.Project, 0, len(a.projects))
	for _, p := range a.projects {
		if p.ID != id {
			newProjects = append(newProjects, p)
		}
	}
	a.projects = newProjects
	mgr, hasMgr := a.managers[id]
	if hasMgr {
		delete(a.managers, id)
	}
	a.mu.Unlock()

	// 在锁外停止 manager 的所有 goroutine，避免长时间持锁
	if hasMgr {
		mgr.StopAll()
	}

	jsonOK(w, map[string]string{"status": "deleted"})
}

// getProjectRules 处理 GET /api/projects/{id}/rules，返回项目的日志过滤规则列表。
func (a *App) getProjectRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	a.mu.RLock()
	p, ok := a.findProject(id)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	loader := config.NewLoader(p.RootPath)
	rules, err := loader.LoadLogRules()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load rules: "+err.Error())
		return
	}

	// 确保返回空数组而非 null
	if rules == nil {
		rules = []model.LogRule{}
	}
	jsonOK(w, rules)
}

// putProjectRules 处理 PUT /api/projects/{id}/rules，覆写项目的日志过滤规则。
//
// 请求体：[]model.LogRule
func (a *App) putProjectRules(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	a.mu.RLock()
	p, ok := a.findProject(id)
	a.mu.RUnlock()

	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	var rules []model.LogRule
	if err := json.NewDecoder(r.Body).Decode(&rules); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	loader := config.NewLoader(p.RootPath)
	if err := loader.SaveLogRules(rules); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save rules: "+err.Error())
		return
	}

	jsonOK(w, rules)
}

// putSelected 处理 PUT /api/projects/{id}/selected，更新项目的待启动服务选中列表。
//
// 请求体：{"names": ["api", "web"]} 或 {"selected_service_ids": ["api", "web"]}
// 注意：names 为服务名称列表（不是 ID），与 SelectedServiceIDs 字段对应。
func (a *App) putSelected(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		Names               []string `json:"names"`
		SelectedServiceIDs  []string `json:"selected_service_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	names := req.Names
	if names == nil {
		names = req.SelectedServiceIDs
	}
	if names == nil {
		names = []string{}
	}

	a.mu.Lock()
	var project model.Project
	found := false
	for i, p := range a.projects {
		if p.ID == id {
			a.projects[i].SelectedServiceIDs = names
			project = a.projects[i]
			found = true
			break
		}
	}
	a.mu.Unlock()

	if !found {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	loader := config.NewLoader(project.RootPath)
	if err := loader.Save(project); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save selection: "+err.Error())
		return
	}

	jsonOK(w, map[string]string{"status": "ok"})
}

// putEnvSelected 处理 PUT /api/projects/{id}/env-selected。
//
// 请求体：{"env_name": "dev", "names": ["api", "worker"]}
// 更新指定环境下用户选中的服务名列表，持久化到配置文件。
func (a *App) putEnvSelected(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req struct {
		EnvName string   `json:"env_name"`
		Names   []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.EnvName == "" {
		jsonError(w, http.StatusBadRequest, "env_name is required")
		return
	}
	if req.Names == nil {
		req.Names = []string{}
	}

	a.mu.Lock()
	var project model.Project
	found := false
	for i, p := range a.projects {
		if p.ID == id {
			if a.projects[i].EnvSelectedServiceIDs == nil {
				a.projects[i].EnvSelectedServiceIDs = map[string][]string{}
			}
			a.projects[i].EnvSelectedServiceIDs[req.EnvName] = req.Names
			project = a.projects[i]
			found = true
			break
		}
	}
	a.mu.Unlock()

	if !found {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	loader := config.NewLoader(project.RootPath)
	if err := loader.Save(project); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to save env selection: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok"})
}

// startEnvSelected 处理 POST /api/projects/{id}/envs/{envName}/start-selected。
//
// 启动策略：
//   - 该 env 下 Required=true 的服务的 deployment 必须启动
//   - 该 env 的 EnvSelectedServiceIDs 中列出的服务名对应的 deployment 也启动
//   - 已运行的 deployment 跳过
func (a *App) startEnvSelected(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	envName := r.PathValue("envName")

	a.mu.RLock()
	p, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	selectedNames := map[string]struct{}{}
	if p.EnvSelectedServiceIDs != nil {
		for _, name := range p.EnvSelectedServiceIDs[envName] {
			selectedNames[name] = struct{}{}
		}
	}

	mgr := a.getOrCreateManager(projectID)
	mgr.SetRunID(uuid.NewString())

	var toStart []model.Deployment
	for _, svc := range p.Services {
		if !svc.Required {
			if _, ok := selectedNames[svc.Name]; !ok {
				continue
			}
		}
		dep := findDepByEnvName(svc.Deployments, envName)
		if dep == nil || dep.IsReadOnly() {
			continue
		}
		if mgr.IsDeploymentActive(dep.ID) {
			continue
		}
		toStart = append(toStart, *dep)
	}

	if len(toStart) == 0 {
		jsonOK(w, map[string]string{"status": "already_running"})
		return
	}

	for _, dep := range toStart {
		dep := dep
		if err := mgr.StartDeployment(dep); err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to start deployment "+dep.ID+": "+err.Error())
			return
		}
		a.pidStore.Set(dep.ID, mgr.DeploymentPID(dep.ID))
	}
	_ = a.pidStore.Flush()

	jsonOK(w, map[string]string{"status": "starting"})
}

// findDepByEnvName 在 deployments 中按 EnvName 查找，未找到返回 nil。
func findDepByEnvName(deps []model.Deployment, envName string) *model.Deployment {
	for i := range deps {
		if deps[i].EnvName == envName {
			return &deps[i]
		}
	}
	return nil
}
