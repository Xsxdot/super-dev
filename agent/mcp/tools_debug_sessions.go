// tools_debug_sessions.go 实现 debug session MCP 工具。
//
// 职责：
//   - 将 MCP 参数解析为 agent debug session API 请求
//   - 复用项目解析和脱敏逻辑
//   - 返回适合 AI 继续排障的结构化会话结果
//
// 边界：
//   - 不直接读写 debug session 文件
//   - 不查询日志
//   - 不启停服务或修改配置
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/superdev/agent/model"
)

func (s *Server) createDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req DebugSessionCreateRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req = normalizeDebugSessionCreateRequest(req)
	if req.Title == "" || req.Question == "" {
		return toolError("invalid_arguments", "title and question are required", nil), nil
	}
	project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return result, nil
	}
	req.ProjectID = project.ID
	req.ProjectName = project.Name

	resolvedReq, result, ok := resolveDebugSessionTarget(project, req)
	if !ok {
		return result, nil
	}

	created, err := s.client.CreateDebugSession(ctx, resolvedReq)
	if err != nil {
		return clientToolError(err), nil
	}
	data := map[string]any{"session": created.Session, "event": created.Event}
	return toolSuccess("debug session created", data, nil, []string{"Append observations with append_debug_session_note as evidence is collected."}), nil
}

func (s *Server) listDebugSessionsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		Status      string `json:"status"`
		Limit       int    `json:"limit"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.Status = strings.TrimSpace(req.Status)

	query := url.Values{}
	if req.ProjectName != "" {
		project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
		if !ok {
			return result, nil
		}
		query.Set("project_id", project.ID)
	} else if req.ProjectID != "" {
		query.Set("project_id", req.ProjectID)
	}
	if req.Status != "" {
		query.Set("status", req.Status)
	}
	if req.Limit > 0 {
		query.Set("limit", strconv.Itoa(req.Limit))
	}

	sessions, err := s.client.ListDebugSessions(ctx, query)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("debug sessions listed", map[string]any{"sessions": sessions, "count": len(sessions)}, nil, nil), nil
}

func (s *Server) getDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
		Limit     int    `json:"limit"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	detail, err := s.client.GetDebugSession(ctx, req.SessionID, req.Limit)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("debug session loaded", map[string]any{"session": detail.Session, "events": detail.Events, "count": detail.Count, "truncated": detail.Truncated}, nil, nil), nil
}

func (s *Server) appendDebugSessionNoteTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string         `json:"session_id"`
		Type      string         `json:"type"`
		Actor     string         `json:"actor"`
		Summary   string         `json:"summary"`
		Data      map[string]any `json:"data"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Type = strings.TrimSpace(req.Type)
	req.Actor = strings.TrimSpace(req.Actor)
	req.Summary = strings.TrimSpace(req.Summary)
	if req.SessionID == "" || req.Type == "" || req.Actor == "" || req.Summary == "" {
		return toolError("invalid_arguments", "session_id, type, actor, and summary are required", nil), nil
	}

	eventReq := DebugSessionAppendEventRequest{
		Type:    req.Type,
		Actor:   req.Actor,
		Summary: req.Summary,
	}
	if req.Data != nil {
		eventReq.Data = redactAny(req.Data).(map[string]any)
	}
	event, err := s.client.AppendDebugSessionEvent(ctx, req.SessionID, eventReq)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("debug session event appended", map[string]any{"event": event}, nil, nil), nil
}

func (s *Server) closeDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
		Summary   string `json:"summary"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Summary = strings.TrimSpace(req.Summary)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	session, err := s.client.CloseDebugSession(ctx, req.SessionID, req.Summary)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("debug session closed", map[string]any{"session": session}, nil, nil), nil
}

func normalizeDebugSessionCreateRequest(req DebugSessionCreateRequest) DebugSessionCreateRequest {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.EnvName = strings.TrimSpace(req.EnvName)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.ServiceName = strings.TrimSpace(req.ServiceName)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	req.Title = strings.TrimSpace(req.Title)
	req.Question = strings.TrimSpace(req.Question)
	return req
}

func resolveDebugSessionTarget(project model.Project, req DebugSessionCreateRequest) (DebugSessionCreateRequest, CallToolResult, bool) {
	if req.DeploymentID != "" {
		svc, dep, ok := findDebugSessionDeployment(project, req.DeploymentID)
		if !ok {
			return req, toolError("deployment_not_found", "deployment does not belong to project", nil), false
		}
		if req.ServiceID != "" && req.ServiceID != svc.ID {
			return req, toolError("invalid_arguments", "service_id does not match deployment", nil), false
		}
		if req.ServiceName != "" && req.ServiceName != svc.Name {
			return req, toolError("invalid_arguments", "service_name does not match deployment", nil), false
		}
		if req.EnvName != "" && req.EnvName != dep.EnvName {
			return req, toolError("invalid_arguments", "env_name does not match deployment", nil), false
		}
		req.ServiceID = svc.ID
		req.ServiceName = svc.Name
		req.EnvName = dep.EnvName
		return req, CallToolResult{}, true
	}

	if req.ServiceID == "" && req.ServiceName == "" {
		return req, CallToolResult{}, true
	}
	svc, ok := findDebugSessionService(project, req.ServiceID, req.ServiceName)
	if !ok {
		return req, toolError("service_not_found", "service does not belong to project", nil), false
	}
	req.ServiceID = svc.ID
	req.ServiceName = svc.Name
	if req.EnvName != "" {
		dep, ok := findDebugSessionDeploymentByEnv(svc, req.EnvName)
		if !ok {
			return req, toolError("deployment_not_found", "env_name does not match service deployment", nil), false
		}
		req.DeploymentID = dep.ID
	}
	return req, CallToolResult{}, true
}

func findDebugSessionDeployment(project model.Project, deploymentID string) (model.Service, model.Deployment, bool) {
	for _, svc := range project.Services {
		for _, dep := range svc.Deployments {
			if dep.ID == deploymentID {
				return svc, dep, true
			}
		}
	}
	return model.Service{}, model.Deployment{}, false
}

func findDebugSessionService(project model.Project, serviceID, serviceName string) (model.Service, bool) {
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

func findDebugSessionDeploymentByEnv(svc model.Service, envName string) (model.Deployment, bool) {
	for _, dep := range svc.Deployments {
		if dep.EnvName == envName {
			return dep, true
		}
	}
	return model.Deployment{}, false
}

func redactAny(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			if isSecretKey(k) {
				out[k] = "[redacted]"
				continue
			}
			out[k] = redactAny(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redactAny(child)
		}
		return out
	default:
		return value
	}
}
