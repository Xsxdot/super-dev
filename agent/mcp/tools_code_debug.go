// tools_code_debug.go 实现本机代码调试 MCP 工具。
//
// 职责：
//   - 将 MCP 参数校验后转发给 agent code-debug HTTP API
//   - 返回 AI 可消费的 session、断点、调用栈、变量和求值结果
//
// 边界：
//   - 不直接连接 DAP adapter
//   - 不启动普通 deployment
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *Server) listCodeDebugTargetsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	targets, err := s.client.ListCodeDebugTargets(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	summary := "code debug targets listed"
	for _, target := range targets {
		if target.Experimental {
			summary = "code debug targets listed; experimental targets present"
			break
		}
	}
	return toolSuccess(summary, map[string]any{"targets": targets, "count": len(targets)}, nil, nil), nil
}

func (s *Server) openCodeDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req OpenCodeDebugSessionRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	if req.DeploymentID == "" {
		return toolError("invalid_arguments", "deployment_id is required", nil), nil
	}
	if req.ApprovalToken != "" {
		session, err := s.client.OpenCodeDebugSession(ctx, req, req.ApprovalToken)
		if err != nil {
			return clientToolError(err), nil
		}
		return toolSuccess("code debug session opened", map[string]any{"session": session}, nil, nil), nil
	}
	wait := boundedApprovalWait(req.ApprovalWaitSeconds)
	var session CodeDebugSession
	err := s.callWithApproval(ctx, wait, func(ctx context.Context, token string) error {
		var err error
		session, err = s.client.OpenCodeDebugSession(ctx, req, token)
		return err
	})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug session opened", map[string]any{"session": session}, nil, []string{"Set breakpoints before continuing when stop_on_entry is enabled."}), nil
}

func (s *Server) closeCodeDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if err := s.client.CloseCodeDebugSession(ctx, req.SessionID); err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug session closed", map[string]any{"session_id": req.SessionID}, nil, nil), nil
}

func (s *Server) setDebugBreakpointsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req DebugBreakpointRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Source = strings.TrimSpace(req.Source)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Source == "" {
		return toolError("invalid_arguments", "source is required", nil), nil
	}
	if len(req.Lines) == 0 {
		return toolError("invalid_arguments", "lines is required", nil), nil
	}
	result, err := s.client.SetCodeDebugBreakpoints(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug breakpoints set", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) debugContinueTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return s.debugThreadActionTool(ctx, args, "continue", "code debug continued")
}

func (s *Server) debugPauseTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return s.debugThreadActionTool(ctx, args, "pause", "code debug paused")
}

func (s *Server) debugStepOverTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return s.debugThreadActionTool(ctx, args, "step-over", "code debug stepped over")
}

func (s *Server) debugStepInTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return s.debugThreadActionTool(ctx, args, "step-in", "code debug stepped in")
}

func (s *Server) debugStepOutTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	return s.debugThreadActionTool(ctx, args, "step-out", "code debug stepped out")
}

func (s *Server) debugThreadActionTool(ctx context.Context, args json.RawMessage, action string, message string) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
		ThreadID  int    `json:"thread_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.ThreadID <= 0 {
		return toolError("invalid_arguments", "thread_id is required", nil), nil
	}
	result, err := s.client.CodeDebugAction(ctx, req.SessionID, action, map[string]any{"thread_id": req.ThreadID})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess(message, map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) debugStackTraceTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
		ThreadID  int    `json:"thread_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.ThreadID <= 0 {
		return toolError("invalid_arguments", "thread_id is required", nil), nil
	}
	result, err := s.client.CodeDebugAction(ctx, req.SessionID, "stack", map[string]any{"thread_id": req.ThreadID})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug stack trace read", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) debugScopesTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID string `json:"session_id"`
		FrameID   int    `json:"frame_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.FrameID <= 0 {
		return toolError("invalid_arguments", "frame_id is required", nil), nil
	}
	result, err := s.client.CodeDebugAction(ctx, req.SessionID, "scopes", map[string]any{"frame_id": req.FrameID})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug scopes read", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) debugVariablesTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		SessionID          string `json:"session_id"`
		VariablesReference int    `json:"variables_reference"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.VariablesReference <= 0 {
		return toolError("invalid_arguments", "variables_reference is required", nil), nil
	}
	result, err := s.client.CodeDebugAction(ctx, req.SessionID, "variables", map[string]any{"variables_reference": req.VariablesReference})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug variables read", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) debugEvaluateTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req DebugEvaluateRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Expression = strings.TrimSpace(req.Expression)
	req.Source = "debug_evaluate"
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Expression == "" {
		return toolError("invalid_arguments", "expression is required", nil), nil
	}
	if req.ApprovalToken != "" {
		result, err := s.client.CodeDebugEvaluate(ctx, req, req.ApprovalToken)
		if err != nil {
			return clientToolError(err), nil
		}
		return toolSuccess("code debug evaluate completed", map[string]any{"result": result}, nil, nil), nil
	}
	wait := boundedApprovalWait(req.ApprovalWaitSeconds)
	var result map[string]any
	err := s.callWithApproval(ctx, wait, func(ctx context.Context, token string) error {
		var err error
		result, err = s.client.CodeDebugEvaluate(ctx, req, token)
		return err
	})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("code debug evaluate completed", map[string]any{"result": result}, nil, nil), nil
}
