// tools_browser_debug.go 实现本机前端浏览器调试 MCP 工具。
//
// 职责：
//   - 列出 SuperDev 可用调试浏览器和本机前端目标
//   - 通过 agent API 创建和关闭浏览器调试会话
//
// 边界：
//   - 不直接启动浏览器进程
//   - 不解析项目配置文件
package mcp

import (
	"context"
	"encoding/json"
	"strings"
)

func (s *Server) listDebugBrowsersTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	browsers, err := s.client.ListDebugBrowsers(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("debug browsers listed", map[string]any{"browsers": browsers, "count": len(browsers)}, nil, nil), nil
}

func (s *Server) listBrowserTargetsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	if err := decodeToolArgs(args, &struct{}{}); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	targets, err := s.client.ListBrowserTargets(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser targets listed", map[string]any{"targets": targets, "count": len(targets)}, nil, nil), nil
}

func (s *Server) openBrowserDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req OpenBrowserSessionRequest
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	if req.DeploymentID == "" {
		return toolError("invalid_arguments", "deployment_id is required", nil), nil
	}
	if err := validateOptionalViewport(req.ViewportWidth, req.ViewportHeight); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	if req.ApprovalToken != "" {
		session, err := s.client.OpenBrowserSession(ctx, req, req.ApprovalToken)
		if err != nil {
			return clientToolError(err), nil
		}
		return toolSuccess("browser debug session opened", map[string]any{"session": session}, nil, nil), nil
	}
	wait := boundedApprovalWait(req.ApprovalWaitSeconds)
	var session BrowserSession
	err := s.callWithApproval(ctx, wait, func(ctx context.Context, token string) error {
		var err error
		session, err = s.client.OpenBrowserSession(ctx, req, token)
		return err
	})
	if err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess(
		"browser debug session opened",
		map[string]any{"session": session},
		nil,
		[]string{"Use page_ws for target-specific CDP clients or devtools_url for manual inspection."},
	), nil
}

func (s *Server) closeBrowserDebugSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
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
	if err := s.client.CloseBrowserSession(ctx, req.SessionID); err != nil {
		return clientToolError(err), nil
	}
	return toolSuccess("browser debug session closed", map[string]any{"session_id": req.SessionID}, nil, nil), nil
}
