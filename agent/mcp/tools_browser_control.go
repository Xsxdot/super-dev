// tools_browser_control.go 实现浏览器页面控制 MCP 工具。
//
// 职责：
//   - 将 MCP 参数校验后转发给 agent browser-control HTTP API
//   - 返回 AI 可消费的页面快照、动作结果、截图和脚本结果
//
// 边界：
//   - 不直接连接 Playwright 或 CDP
//   - 不创建 browser session
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *Server) browserSnapshotTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserSnapshotRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	snapshot, err := s.client.BrowserSnapshot(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser snapshot captured", map[string]any{"snapshot": snapshot}, nil, nil), nil
}

func (s *Server) browserClickTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserClickRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Selector = strings.TrimSpace(req.Selector)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Selector == "" {
		return toolError("invalid_arguments", "selector is required", nil), nil
	}
	result, err := s.client.BrowserClick(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser click completed", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserTypeTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserTypeRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Selector = strings.TrimSpace(req.Selector)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Selector == "" {
		return toolError("invalid_arguments", "selector is required", nil), nil
	}
	result, err := s.client.BrowserType(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser type completed", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserScreenshotTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserScreenshotRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	screenshot, err := s.client.BrowserScreenshot(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser screenshot captured", map[string]any{"screenshot": screenshot}, nil, nil), nil
}

func (s *Server) browserEvaluateTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserEvaluateRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Expression = strings.TrimSpace(req.Expression)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Expression == "" {
		return toolError("invalid_arguments", "expression is required", nil), nil
	}
	result, err := s.client.BrowserEvaluate(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser evaluate completed", map[string]any{"result": result}, nil, nil), nil
}
