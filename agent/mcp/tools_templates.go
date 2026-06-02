// tools_templates.go 实现流水线模板 MCP 工具。
//
// 职责：
//   - 预览模板 YAML 或文件
//   - 导入用户模板到 agent 模板库
//
// 边界：
//   - 不修改项目 deployment
//   - 不执行流水线
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *Server) previewPipelineTemplateTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		Path string `json:"path"`
		YAML string `json:"yaml"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.Path = strings.TrimSpace(req.Path)
	hasYAML := strings.TrimSpace(req.YAML) != ""
	if req.Path == "" && !hasYAML {
		return toolError("invalid_arguments", "path or yaml is required", nil), nil
	}
	if req.Path != "" && hasYAML {
		return toolError("invalid_arguments", "path and yaml are mutually exclusive", nil), nil
	}
	preview, err := s.client.PreviewPipelineTemplate(ctx, req.Path, req.YAML)
	if err != nil {
		return clientToolError(err), nil
	}
	if len(preview.Errors) > 0 {
		return toolError("template_validation_failed", strings.Join(preview.Errors, "; "), preview), nil
	}
	return toolSuccess("pipeline template preview succeeded", map[string]any{"preview": preview}, nil, nil), nil
}

func (s *Server) importPipelineTemplateTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		Path           string `json:"path"`
		ApprovalToken  string `json:"approval_token"`
		DebugSessionID string `json:"debug_session_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.Path = strings.TrimSpace(req.Path)
	if req.Path == "" {
		return toolError("invalid_arguments", "path is required", nil), nil
	}
	summary, err := s.client.ImportPipelineTemplate(ctx, req.Path, req.ApprovalToken)
	if err != nil {
		s.appendOperationToolObservation(ctx, req.DebugSessionID, "template.import", "template import failed", err)
		return clientToolError(err), nil
	}
	s.appendOperationToolObservation(ctx, req.DebugSessionID, "template.import", "pipeline template imported", nil)
	return toolSuccess("pipeline template imported", map[string]any{"template": summary}, nil, nil), nil
}
