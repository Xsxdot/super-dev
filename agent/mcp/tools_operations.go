// tools_operations.go 实现 operation 安全预检和审批查询 MCP 工具。
//
// 职责：
//   - 将 MCP 参数转为 agent operation API 请求
//   - 展示审批状态、一次性 token 和审计记录
//
// 边界：
//   - 不在 MCP 层计算风险
//   - 不直接读写 approval/audit 文件
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/superdev/agent/model"
)

func (s *Server) previewOperationTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req OperationRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.Kind = strings.TrimSpace(req.Kind)
	if req.Kind == "" {
		return toolError("invalid_arguments", "kind is required", nil), nil
	}
	plan, err := s.client.PreviewOperation(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("operation preflight completed", map[string]any{"plan": plan}, nil, nil), nil
}

func (s *Server) listOperationApprovalsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		Status      string `json:"status"`
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		Limit       int    `json:"limit"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	q, result, err := s.operationQueryWithProject(ctx, req.ProjectID, req.ProjectName)
	if result.IsError || err != nil {
		return result, err
	}
	if strings.TrimSpace(req.Status) != "" {
		q.Set("status", strings.TrimSpace(req.Status))
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	approvals, err := s.client.ListOperationApprovals(ctx, q)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("operation approvals loaded", map[string]any{"approvals": approvals, "count": len(approvals)}, nil, nil), nil
}

func (s *Server) getOperationApprovalTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ApprovalID string `json:"approval_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.ApprovalID = strings.TrimSpace(req.ApprovalID)
	if req.ApprovalID == "" {
		return toolError("invalid_arguments", "approval_id is required", nil), nil
	}
	detail, err := s.client.GetOperationApproval(ctx, req.ApprovalID)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("operation approval loaded", map[string]any{"approval": detail.Approval, "approval_token": detail.ApprovalToken}, nil, nil), nil
}

func (s *Server) listOperationAuditTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		Kind        string `json:"kind"`
		ApprovalID  string `json:"approval_id"`
		Since       string `json:"since"`
		Limit       int    `json:"limit"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	q, result, err := s.operationQueryWithProject(ctx, req.ProjectID, req.ProjectName)
	if result.IsError || err != nil {
		return result, err
	}
	if strings.TrimSpace(req.Kind) != "" {
		q.Set("kind", strings.TrimSpace(req.Kind))
	}
	if strings.TrimSpace(req.ApprovalID) != "" {
		q.Set("approval_id", strings.TrimSpace(req.ApprovalID))
	}
	if strings.TrimSpace(req.Since) != "" {
		q.Set("since", strings.TrimSpace(req.Since))
	}
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	audit, err := s.client.ListOperationAudit(ctx, q)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("operation audit loaded", map[string]any{"events": audit.Events, "count": audit.Count}, nil, nil), nil
}

func (s *Server) operationQueryWithProject(ctx context.Context, projectID string, projectName string) (url.Values, CallToolResult, error) {
	q := url.Values{}
	projectID = strings.TrimSpace(projectID)
	projectName = strings.TrimSpace(projectName)
	if projectID != "" {
		q.Set("project_id", projectID)
		return q, CallToolResult{}, nil
	}
	if projectName == "" {
		return q, CallToolResult{}, nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return nil, clientToolError(err), nil
	}
	matches := make([]model.Project, 0, 1)
	for _, project := range projects {
		if project.Name == projectName {
			matches = append(matches, sanitizeProject(project))
		}
	}
	if len(matches) == 0 {
		return nil, toolError("project_not_found", "project not found", nil), nil
	}
	if len(matches) > 1 {
		return nil, toolError("ambiguous_project", "multiple projects matched; specify project_id", matches), nil
	}
	q.Set("project_id", matches[0].ID)
	return q, CallToolResult{}, nil
}
