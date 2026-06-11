// tools_code_debug_composite.go 实现代码调试复合 MCP 工具。
//
// 职责：
//   - 提供 AI 默认使用的高层调试入口
//   - 将打开 session、断点、继续、调用栈、作用域和变量读取压成少量工具调用
//
// 边界：
//   - 不直接连接 DAP adapter
//   - 不绕过 agent 的 approval 和 evaluate 审计
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *Server) debugCaptureAtTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req DebugCaptureAtRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	req.Source = strings.TrimSpace(req.Source)
	if req.SessionID == "" && req.DeploymentID == "" {
		return toolError("invalid_arguments", "session_id or deployment_id is required", nil), nil
	}
	if req.Source == "" || req.Line <= 0 {
		return toolError("invalid_arguments", "source and line are required", nil), nil
	}
	if req.ApprovalToken != "" {
		result, err := s.client.CodeDebugCaptureAt(ctx, req, req.ApprovalToken)
		if err != nil {
			return clientToolError(err), nil
		}
		return toolSuccess("code debug capture completed", result, nil, []string{"Use debug_inspect for follow-up reads while the session remains paused."}), nil
	}
	wait := boundedApprovalWait(req.ApprovalWaitSeconds)
	var result map[string]any
	err := s.callWithApproval(ctx, wait, func(ctx context.Context, token string) error {
		var err error
		result, err = s.client.CodeDebugCaptureAt(ctx, req, token)
		return err
	})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug capture completed", result, nil, []string{"Prefer logs first; this capture is for runtime state that logs could not explain."}), nil
}

func (s *Server) debugInspectTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req DebugInspectRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	result, err := s.client.CodeDebugInspect(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug inspect completed", result, nil, nil), nil
}
