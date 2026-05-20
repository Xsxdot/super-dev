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

	// 确保返回空数组而非 null
	if projects == nil {
		projects = []model.Project{}
	}
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

// deleteProject 处理 DELETE /api/projects/{id}，从内存和注册表中移除项目。
func (a *App) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	a.mu.Lock()
	var rootPath string
	newProjects := make([]model.Project, 0, len(a.projects))
	for _, p := range a.projects {
		if p.ID == id {
			rootPath = p.RootPath
		} else {
			newProjects = append(newProjects, p)
		}
	}
	a.projects = newProjects
	a.mu.Unlock()

	if rootPath == "" {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	if err := a.registry.Remove(rootPath); err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to remove project from registry: "+err.Error())
		return
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
