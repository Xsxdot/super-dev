// handler_debug_sessions.go 实现本机排障会话 HTTP API。
//
// 职责：
//   - 创建、查询、追加和关闭 debug session
//   - 校验 session 绑定的 project/deployment 边界
//   - 将持久化委托给 debugsession.Store
//
// 边界：
//   - 不查询日志
//   - 不推断根因
//   - 不修改项目配置或服务运行态
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/superdev/agent/debugsession"
	"github.com/superdev/agent/model"
)

type debugSessionDetailResponse struct {
	Session debugsession.Session `json:"session"`
	Events  []debugsession.Event `json:"events"`
	Count   int                  `json:"count"`
	// Truncated 表示事件列表因 limit 被截断，避免调用方误以为拿到了完整历史。
	Truncated bool `json:"truncated"`
}

type debugSessionCreateResponse struct {
	Session debugsession.Session `json:"session"`
	Event   debugsession.Event   `json:"event"`
}

// listDebugSessions 处理 GET /api/debug-sessions。
func (a *App) listDebugSessions(w http.ResponseWriter, r *http.Request) {
	filter := debugsession.ListFilter{
		ProjectID: strings.TrimSpace(r.URL.Query().Get("project_id")),
		Status:    strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 0 {
			jsonError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		filter.Limit = limit
	}

	sessions, err := a.debugSessions.List(r.Context(), filter)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, sessions)
}

// createDebugSession 处理 POST /api/debug-sessions。
func (a *App) createDebugSession(w http.ResponseWriter, r *http.Request) {
	var req debugsession.CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	resolved, status, msg := a.resolveDebugSessionCreateRequest(req)
	if status != 0 {
		jsonError(w, status, msg)
		return
	}

	session, event, err := a.debugSessions.Create(r.Context(), resolved)
	if err != nil {
		writeDebugSessionStoreError(w, err)
		return
	}
	jsonOK(w, debugSessionCreateResponse{Session: session, Event: event})
}

// getDebugSession 处理 GET /api/debug-sessions/{id}。
func (a *App) getDebugSession(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 0 {
			jsonError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	session, events, err := a.debugSessions.Get(r.Context(), r.PathValue("id"), 0)
	if err != nil {
		writeDebugSessionStoreError(w, err)
		return
	}
	count := len(events)
	truncated := false
	if limit > 0 && len(events) > limit {
		events = events[:limit]
		truncated = true
	}
	jsonOK(w, debugSessionDetailResponse{Session: session, Events: events, Count: count, Truncated: truncated})
}

// appendDebugSessionEvent 处理 POST /api/debug-sessions/{id}/events。
func (a *App) appendDebugSessionEvent(w http.ResponseWriter, r *http.Request) {
	var req debugsession.AppendEventRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	event, err := a.debugSessions.AppendEvent(r.Context(), r.PathValue("id"), req)
	if err != nil {
		writeDebugSessionStoreError(w, err)
		return
	}
	jsonOK(w, event)
}

// closeDebugSession 处理 POST /api/debug-sessions/{id}/close。
func (a *App) closeDebugSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Summary string `json:"summary"`
	}
	if err := decodeOptionalJSON(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	session, _, err := a.debugSessions.Close(r.Context(), r.PathValue("id"), req.Summary)
	if err != nil {
		writeDebugSessionStoreError(w, err)
		return
	}
	jsonOK(w, session)
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func writeDebugSessionStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, debugsession.ErrSessionNotFound):
		jsonError(w, http.StatusNotFound, "debug session not found")
	case errors.Is(err, debugsession.ErrSessionClosed):
		jsonError(w, http.StatusBadRequest, "debug session is closed")
	case errors.Is(err, debugsession.ErrInvalidEvent), errors.Is(err, debugsession.ErrEventTooLarge):
		jsonError(w, http.StatusBadRequest, err.Error())
	default:
		jsonError(w, http.StatusInternalServerError, err.Error())
	}
}

func (a *App) resolveDebugSessionCreateRequest(req debugsession.CreateRequest) (debugsession.CreateRequest, int, string) {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.EnvName = strings.TrimSpace(req.EnvName)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.ServiceName = strings.TrimSpace(req.ServiceName)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	req.Title = strings.TrimSpace(req.Title)
	req.Question = strings.TrimSpace(req.Question)

	if req.Title == "" || req.Question == "" {
		return req, http.StatusBadRequest, "title and question are required"
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	project, status, msg := a.resolveDebugSessionProjectLocked(req.ProjectID, req.ProjectName)
	if status != 0 {
		return req, status, msg
	}
	req.ProjectID = project.ID
	req.ProjectName = project.Name

	if req.DeploymentID != "" {
		svc, dep, ok := findDeploymentInProject(project, req.DeploymentID)
		if !ok {
			// deployment 必须属于当前 project，避免 AI 把两个项目的证据串在一起。
			return req, http.StatusBadRequest, "deployment does not belong to project"
		}
		if req.ServiceID != "" && req.ServiceID != svc.ID {
			return req, http.StatusBadRequest, "service_id does not match deployment"
		}
		if req.ServiceName != "" && req.ServiceName != svc.Name {
			return req, http.StatusBadRequest, "service_name does not match deployment"
		}
		if req.EnvName != "" && req.EnvName != dep.EnvName {
			return req, http.StatusBadRequest, "env_name does not match deployment"
		}
		req.ServiceID = svc.ID
		req.ServiceName = svc.Name
		req.EnvName = dep.EnvName
		return req, 0, ""
	}

	if req.ServiceID != "" || req.ServiceName != "" {
		svc, ok := findServiceInProject(project, req.ServiceID, req.ServiceName)
		if !ok {
			return req, http.StatusBadRequest, "service does not belong to project"
		}
		req.ServiceID = svc.ID
		req.ServiceName = svc.Name
		if req.EnvName != "" {
			dep, ok := findDeploymentByEnv(svc, req.EnvName)
			if !ok {
				return req, http.StatusBadRequest, "env_name does not match service deployment"
			}
			req.DeploymentID = dep.ID
		}
	}

	return req, 0, ""
}

func (a *App) resolveDebugSessionProjectLocked(projectID, projectName string) (model.Project, int, string) {
	if projectID != "" {
		project, ok := a.findProject(projectID)
		if !ok {
			return model.Project{}, http.StatusNotFound, "project not found"
		}
		if projectName != "" && projectName != project.Name {
			return model.Project{}, http.StatusBadRequest, "project_name does not match project_id"
		}
		return project, 0, ""
	}
	if projectName == "" {
		return model.Project{}, http.StatusBadRequest, "project_id or project_name is required"
	}

	var matches []model.Project
	for _, project := range a.projects {
		if project.Name == projectName {
			matches = append(matches, project)
		}
	}
	if len(matches) == 0 {
		return model.Project{}, http.StatusNotFound, "project not found"
	}
	if len(matches) > 1 {
		return model.Project{}, http.StatusBadRequest, "project_name is ambiguous; use project_id"
	}
	return matches[0], 0, ""
}

func findDeploymentInProject(project model.Project, deploymentID string) (model.Service, model.Deployment, bool) {
	for _, svc := range project.Services {
		for _, dep := range svc.Deployments {
			if dep.ID == deploymentID {
				return svc, dep, true
			}
		}
	}
	return model.Service{}, model.Deployment{}, false
}

func findServiceInProject(project model.Project, serviceID, serviceName string) (model.Service, bool) {
	for _, svc := range project.Services {
		if serviceID != "" && svc.ID != serviceID {
			continue
		}
		if serviceName != "" && svc.Name != serviceName {
			continue
		}
		return svc, true
	}
	return model.Service{}, false
}

func findDeploymentByEnv(svc model.Service, envName string) (model.Deployment, bool) {
	for _, dep := range svc.Deployments {
		if dep.EnvName == envName {
			return dep, true
		}
	}
	return model.Deployment{}, false
}
