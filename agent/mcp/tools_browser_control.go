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

func (s *Server) browserNavigateTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserNavigateRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.URL = strings.TrimSpace(req.URL)
	req.Path = strings.TrimSpace(req.Path)
	req.WaitUntil = strings.TrimSpace(req.WaitUntil)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.URL == "" && req.Path == "" {
		return toolError("invalid_arguments", "url or path is required", nil), nil
	}
	result, err := s.client.BrowserNavigate(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser navigation completed", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserReloadTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserReloadRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.WaitUntil = strings.TrimSpace(req.WaitUntil)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	result, err := s.client.BrowserReload(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser reload completed", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserWaitForSelectorTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserWaitForSelectorRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Selector = strings.TrimSpace(req.Selector)
	req.State = strings.TrimSpace(req.State)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Selector == "" {
		return toolError("invalid_arguments", "selector is required", nil), nil
	}
	result, err := s.client.BrowserWaitForSelector(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser selector matched", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserPressKeyTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserPressKeyRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Selector = strings.TrimSpace(req.Selector)
	req.Key = strings.TrimSpace(req.Key)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Key == "" {
		return toolError("invalid_arguments", "key is required", nil), nil
	}
	result, err := s.client.BrowserPressKey(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser key press completed", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserSelectOptionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserSelectOptionRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Selector = strings.TrimSpace(req.Selector)
	req.Value = strings.TrimSpace(req.Value)
	req.Label = strings.TrimSpace(req.Label)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Selector == "" {
		return toolError("invalid_arguments", "selector is required", nil), nil
	}
	if req.Value == "" && req.Label == "" {
		return toolError("invalid_arguments", "value or label is required", nil), nil
	}
	result, err := s.client.BrowserSelectOption(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser option selected", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserConsoleLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserConsoleLogsRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Level = strings.ToLower(strings.TrimSpace(req.Level))
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Level != "" && req.Level != "log" && req.Level != "info" && req.Level != "warning" && req.Level != "error" {
		return toolError("invalid_arguments", "level must be log, info, warning, or error", nil), nil
	}
	if req.Limit < 0 || req.Limit > 200 {
		return toolError("invalid_arguments", "limit must be between 1 and 200", nil), nil
	}
	result, err := s.client.BrowserConsoleLogs(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser console logs captured", map[string]any{"result": result}, nil, nil), nil
}

func (s *Server) browserNetworkRequestsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req BrowserNetworkRequestsRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Filter = strings.TrimSpace(req.Filter)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.Limit < 0 || req.Limit > 200 {
		return toolError("invalid_arguments", "limit must be between 1 and 200", nil), nil
	}
	result, err := s.client.BrowserNetworkRequests(ctx, req)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser network requests captured", map[string]any{"result": result}, nil, nil), nil
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
