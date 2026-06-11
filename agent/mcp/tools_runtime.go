// tools_runtime.go 实现运行态查询和服务控制 MCP 工具。
//
// 职责：
//   - 查询项目、服务和 deployment 运行态
//   - 通过本机 agent 对唯一解析出的 deployment 执行启停
//
// 边界：
//   - 不直接读取项目配置或 SQLite
//   - 不直接操作进程
//   - 不在目标不唯一时猜测用户意图
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	defaultApprovalWaitSeconds = 60
	approvalPollInterval       = 500 * time.Millisecond
)

type deploymentControlFunc func(context.Context, string, string) error

func (s *Server) listProjectsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	projects = sanitizeProjects(projects)
	return toolSuccess(
		fmt.Sprintf("%d project(s) registered", len(projects)),
		map[string]any{"projects": projects, "count": len(projects)},
		nil,
		nil,
	), nil
}

func (s *Server) getProjectTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	matches := make([]model.Project, 0, 1)
	for _, project := range projects {
		if req.ProjectID != "" && project.ID != req.ProjectID {
			continue
		}
		if req.ProjectName != "" && project.Name != req.ProjectName {
			continue
		}
		matches = append(matches, sanitizeProject(project))
	}
	if len(matches) == 0 {
		return toolError("project_not_found", "project not found", nil), nil
	}
	if len(matches) > 1 {
		return toolError("ambiguous_project", "multiple projects matched; specify project_id", matches), nil
	}
	return toolSuccess("project found", map[string]any{"project": matches[0]}, nil, nil), nil
}

func (s *Server) runtimeSnapshotTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	services, err := s.client.ListServices(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	statusCounts := map[string]int{}
	for _, svc := range services {
		statusCounts[serviceStatusKey(svc.Status)]++
		for _, dep := range svc.Deployments {
			statusCounts["deployment:"+serviceStatusKey(dep.Status)]++
		}
	}
	data := map[string]any{
		"projects":      sanitizeProjects(projects),
		"services":      sanitizeServices(services),
		"project_count": len(projects),
		"service_count": len(services),
		"status_counts": statusCounts,
	}
	return toolSuccess(
		fmt.Sprintf("%d project(s), %d service(s)", len(projects), len(services)),
		data,
		nil,
		[]string{"Use list_services for detailed runtime state or diagnose_service for one failing deployment."},
	), nil
}

func (s *Server) listServicesTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	services, err := s.client.ListServices(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	projectIDs := map[string]bool{}
	for _, project := range projects {
		if req.ProjectID != "" && project.ID != req.ProjectID {
			continue
		}
		if req.ProjectName != "" && project.Name != req.ProjectName {
			continue
		}
		projectIDs[project.ID] = true
	}
	if req.ProjectID != "" || req.ProjectName != "" {
		filtered := make([]model.Service, 0, len(services))
		for _, svc := range services {
			if projectIDs[svc.ProjectID] {
				filtered = append(filtered, svc)
			}
		}
		services = filtered
	}
	services = sanitizeServices(services)
	return toolSuccess(
		fmt.Sprintf("%d service(s)", len(services)),
		map[string]any{"services": services, "count": len(services)},
		nil,
		nil,
	), nil
}

func (s *Server) startServiceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	target, req, result, err := s.resolveControlTarget(ctx, args)
	if result.IsError || err != nil {
		return result, err
	}
	if err := s.runDeploymentControl(ctx, target, req, s.client.StartDeployment); err != nil {
		s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.start", "start operation failed", err)
		return clientToolError(err), nil
	}
	s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.start", "start operation requested", nil)
	return toolSuccess("start requested", map[string]any{"target": sanitizeTarget(target), "action": "start"}, nil, nil), nil
}

func (s *Server) stopServiceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	target, req, result, err := s.resolveControlTarget(ctx, args)
	if result.IsError || err != nil {
		return result, err
	}
	if err := s.runDeploymentControl(ctx, target, req, s.client.StopDeployment); err != nil {
		s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.stop", "stop operation failed", err)
		return clientToolError(err), nil
	}
	s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.stop", "stop operation requested", nil)
	return toolSuccess("stop requested", map[string]any{"target": sanitizeTarget(target), "action": "stop"}, nil, nil), nil
}

func (s *Server) restartServiceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	target, req, result, err := s.resolveControlTarget(ctx, args)
	if result.IsError || err != nil {
		return result, err
	}
	if err := s.runDeploymentControl(ctx, target, req, s.client.RestartDeployment); err != nil {
		s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.restart", "restart operation failed", err)
		return clientToolError(err), nil
	}
	s.appendOperationToolObservation(ctx, req.DebugSessionID, "runtime.restart", "restart operation requested", nil)
	return toolSuccess("restart requested", map[string]any{"target": sanitizeTarget(target), "action": "restart"}, nil, nil), nil
}

func (s *Server) runDeploymentControl(ctx context.Context, target resolvedTarget, req targetArgs, control deploymentControlFunc) error {
	// 已显式带 token 时，直接执行一次，不进入等待（保持原语义）。
	if strings.TrimSpace(req.ApprovalToken) != "" {
		return control(ctx, target.Deployment.ID, req.ApprovalToken)
	}
	wait := approvalWaitDuration(req)
	return s.callWithApproval(ctx, wait, func(ctx context.Context, token string) error {
		return control(ctx, target.Deployment.ID, token)
	})
}

func approvalRequiredAgentError(err error) (AgentError, bool) {
	var agentErr AgentError
	if !errors.As(err, &agentErr) {
		return AgentError{}, false
	}
	return agentErr, agentErr.Code == "approval_required" && agentErr.Approval.ID != ""
}

func approvalWaitDuration(req targetArgs) time.Duration {
	return boundedApprovalWait(req.ApprovalWaitSeconds)
}

func (s *Server) waitForApprovalToken(ctx context.Context, approvalID string, wait time.Duration) (string, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(approvalPollInterval)
	defer ticker.Stop()

	for {
		token, err := s.readApprovedToken(ctx, approvalID)
		if err != nil || token != "" {
			return token, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", nil
		case <-ticker.C:
		}
	}
}

func (s *Server) readApprovedToken(ctx context.Context, approvalID string) (string, error) {
	detail, err := s.client.GetOperationApproval(ctx, approvalID)
	if err != nil {
		return "", err
	}
	switch detail.Approval.Status {
	case "approved":
		if strings.TrimSpace(detail.ApprovalToken) == "" {
			return "", AgentError{Code: "approval_token_missing", Message: "approval token missing", Approval: detail.Approval}
		}
		return strings.TrimSpace(detail.ApprovalToken), nil
	case "rejected":
		return "", AgentError{Code: "approval_rejected", Message: "approval rejected", Approval: detail.Approval}
	case "expired":
		return "", AgentError{Code: "approval_expired", Message: "approval expired", Approval: detail.Approval}
	case "used":
		return "", AgentError{Code: "approval_token_consumed", Message: "approval token already used", Approval: detail.Approval}
	default:
		return "", nil
	}
}

func (s *Server) resolveControlTarget(ctx context.Context, args json.RawMessage) (resolvedTarget, targetArgs, CallToolResult, error) {
	var req targetArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return resolvedTarget{}, targetArgs{}, toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return resolvedTarget{}, req, clientToolError(err), nil
	}
	target, errResp := resolveDeploymentTarget(projects, req)
	if errResp != nil {
		return resolvedTarget{}, req, toolError(errResp.Code, errResp.Message, errResp), nil
	}
	return target, req, CallToolResult{}, nil
}

func decodeToolArgs(args json.RawMessage, out any) error {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if err := json.Unmarshal(args, out); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func clientToolError(err error) CallToolResult {
	if strings.Contains(err.Error(), "agent unavailable") {
		return toolError("agent_unavailable", err.Error(), nil)
	}
	var agentErr AgentError
	if errors.As(err, &agentErr) {
		code := agentErr.Code
		if code == "" {
			code = "operation_failed"
		}
		data := agentErr.Data
		if agentErr.Plan.ID != "" || agentErr.Approval.ID != "" {
			payload := map[string]any{
				"plan":     agentErr.Plan,
				"approval": agentErr.Approval,
			}
			if agentErr.Data != nil {
				payload["data"] = agentErr.Data
			}
			data = payload
		}
		return toolError(code, agentErr.Message, data)
	}
	return toolError("operation_failed", err.Error(), nil)
}

func (s *Server) appendOperationToolObservation(ctx context.Context, sessionID string, operationKind string, summary string, err error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	data := map[string]any{"operation_kind": operationKind}
	var agentErr AgentError
	if err != nil && errors.As(err, &agentErr) {
		if agentErr.Plan.Kind != "" {
			data["operation_kind"] = agentErr.Plan.Kind
		}
		if agentErr.Approval.ID != "" {
			data["approval_id"] = agentErr.Approval.ID
		}
		data["error_code"] = agentErr.Code
	}
	_, _ = s.client.AppendDebugSessionEvent(ctx, sessionID, DebugSessionAppendEventRequest{
		Type:    "observation",
		Actor:   "assistant",
		Summary: summary,
		Data:    data,
	})
}

func sanitizeProjects(projects []model.Project) []model.Project {
	out := make([]model.Project, len(projects))
	for i, project := range projects {
		out[i] = sanitizeProject(project)
	}
	return out
}

func sanitizeProject(project model.Project) model.Project {
	project.Variables = redactSecretMap(project.Variables)
	for i, svc := range project.Services {
		project.Services[i] = sanitizeService(svc)
	}
	return project
}

func sanitizeServices(services []model.Service) []model.Service {
	out := make([]model.Service, len(services))
	for i, service := range services {
		out[i] = sanitizeService(service)
	}
	return out
}

func sanitizeService(service model.Service) model.Service {
	for i, dep := range service.Deployments {
		service.Deployments[i] = sanitizeDeployment(dep)
	}
	return service
}

func sanitizeDeployment(dep model.Deployment) model.Deployment {
	dep.Env = redactSecretMap(dep.Env)
	if dep.Runtime != nil {
		runtime := *dep.Runtime
		runtime.EnvVars = redactSecretMap(runtime.EnvVars)
		dep.Runtime = &runtime
	}
	return dep
}

func sanitizeTarget(target resolvedTarget) resolvedTarget {
	target.Project = sanitizeProject(target.Project)
	target.Service = sanitizeService(target.Service)
	target.Deployment = sanitizeDeployment(target.Deployment)
	return target
}

func serviceStatusKey(status model.ServiceStatus) string {
	if status == "" {
		return "stopped"
	}
	return string(status)
}
