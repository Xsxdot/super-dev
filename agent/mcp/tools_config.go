// tools_config.go 实现项目、服务和项目级流水线配置 upsert MCP 工具。
//
// 职责：
//   - 将 MCP 请求转发给 agent config change API
//   - 保留 preview、diff、approval 的结构化结果
//   - 将配置写入结果按需追加到本机 debug session
//
// 边界：
//   - 不直接读取或写入 .superdev/config.yaml
//   - 不导入 agent/config、agent/operation 或 agent/store
//   - 不支持删除
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

func (s *Server) probeProjectConfigTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		RootPath string `json:"root_path"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.RootPath = strings.TrimSpace(req.RootPath)
	if req.RootPath == "" {
		return toolError("invalid_arguments", "root_path is required", nil), nil
	}
	project, err := s.client.ProbeProjectConfig(ctx, req.RootPath)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("project config probed", map[string]any{"project": sanitizeProject(project)}, nil, nil), nil
}

func (s *Server) getProjectConfigTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	if req.ProjectID == "" {
		return toolError("invalid_arguments", "project_id is required", nil), nil
	}
	project, err := s.client.GetProjectConfig(ctx, req.ProjectID)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("project config loaded", map[string]any{"project": sanitizeProject(project)}, nil, nil), nil
}

func (s *Server) previewConfigChangeTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeConfigChangeArgs(args)
	if !ok {
		return result, nil
	}
	if req.Kind == "" {
		return toolError("invalid_arguments", "kind is required", nil), nil
	}
	preview, err := s.client.PreviewConfigChange(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	preview = sanitizeConfigChangePreview(preview)
	return toolSuccess(
		"config change previewed",
		map[string]any{"preview": preview},
		nil,
		[]string{"Call apply_config_change with an approval token after the user approves if approval is required."},
	), nil
}

func (s *Server) applyConfigChangeTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeConfigChangeArgs(args)
	if !ok {
		return result, nil
	}
	if req.Kind == "" {
		return toolError("invalid_arguments", "kind is required", nil), nil
	}
	return s.applyConfigChangeFromRequest(ctx, req)
}

func (s *Server) upsertProjectConfigTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeConfigChangeArgs(args)
	if !ok {
		return result, nil
	}
	req.Kind = "config.project.upsert"
	return s.applyConfigChangeFromRequest(ctx, req)
}

func (s *Server) upsertServiceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeConfigChangeArgs(args)
	if !ok {
		return result, nil
	}
	req.Kind = "config.service.upsert"
	return s.applyConfigChangeFromRequest(ctx, req)
}

func (s *Server) upsertProjectPipelineTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	req, result, ok := decodeConfigChangeArgs(args)
	if !ok {
		return result, nil
	}
	req.Kind = "config.pipeline.upsert"
	return s.applyConfigChangeFromRequest(ctx, req)
}

func (s *Server) applyConfigChangeFromRequest(ctx context.Context, req ConfigChangeRequest) (CallToolResult, error) {
	var preview ConfigChangePreview
	apply := func(ctx context.Context, token string) error {
		p, err := s.client.ApplyConfigChange(ctx, req, token)
		if err != nil {
			return err
		}
		preview = p
		return nil
	}
	var err error
	if strings.TrimSpace(req.ApprovalToken) != "" {
		err = apply(ctx, req.ApprovalToken)
	} else {
		err = s.callWithApproval(ctx, boundedApprovalWait(req.ApprovalWaitSeconds), apply)
	}
	if err != nil {
		s.appendConfigToolObservation(ctx, req.DebugSessionID, req.Kind, "config change failed", err)
		return clientToolError(err), nil
	}
	preview = sanitizeConfigChangePreview(preview)
	s.appendConfigToolObservation(ctx, req.DebugSessionID, req.Kind, "config change applied", nil)
	return toolSuccess("config change applied", map[string]any{"preview": preview}, nil, nil), nil
}

func sanitizeConfigChangePreview(preview ConfigChangePreview) ConfigChangePreview {
	// preview.Project 会进入 MCP structured content，复用普通快照边界避免泄漏调试凭据明文。
	preview.Project = sanitizeProject(preview.Project)
	return preview
}

func decodeConfigChangeArgs(args json.RawMessage) (ConfigChangeRequest, CallToolResult, bool) {
	var req ConfigChangeRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return ConfigChangeRequest{}, toolError("invalid_arguments", err.Error(), nil), false
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.RootPath = strings.TrimSpace(req.RootPath)
	req.ApprovalToken = strings.TrimSpace(req.ApprovalToken)
	req.DebugSessionID = strings.TrimSpace(req.DebugSessionID)
	return req, CallToolResult{}, true
}

func (s *Server) appendConfigToolObservation(ctx context.Context, sessionID string, kind string, summary string, err error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	data := map[string]any{"operation_kind": kind}
	var agentErr AgentError
	if err != nil && errors.As(err, &agentErr) {
		data["error_code"] = agentErr.Code
		if agentErr.Plan.Kind != "" {
			data["operation_kind"] = agentErr.Plan.Kind
		}
		if agentErr.Approval.ID != "" {
			data["approval_id"] = agentErr.Approval.ID
		}
	}
	_, _ = s.client.AppendDebugSessionEvent(ctx, sessionID, DebugSessionAppendEventRequest{
		Type:    "observation",
		Actor:   "assistant",
		Summary: summary,
		Data:    data,
	})
}
