// tools_debug_credentials.go 实现 get_debug_credentials 只读工具。
//
// 职责：
//   - 经 agent 端点返回项目/服务合并后的调试凭据明文，供 AI 调试合法登录/鉴权
//
// 边界：
//   - 只读，不走逐次审批（授信体现在“配置里填没填”）
//   - 是调试凭据明文的唯一出口；快照工具一律剥除该字段
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

func (s *Server) getDebugCredentialsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ProjectID   string `json:"project_id"`
		ProjectName string `json:"project_name"`
		ServiceID   string `json:"service_id"`
		ServiceName string `json:"service_name"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	if req.ProjectID == "" && req.ProjectName == "" {
		return toolError("invalid_arguments", "project_id or project_name is required", nil), nil
	}

	q := url.Values{}
	if req.ProjectID != "" {
		q.Set("project_id", req.ProjectID)
	}
	if req.ProjectName != "" {
		q.Set("project_name", req.ProjectName)
	}
	if req.ServiceID != "" {
		q.Set("service_id", req.ServiceID)
	}
	if req.ServiceName != "" {
		q.Set("service_name", req.ServiceName)
	}

	creds, err := s.client.GetDebugCredentials(ctx, q)
	if err != nil {
		return clientToolError(err), nil
	}

	return toolSuccess(
		fmt.Sprintf("%d debug credential(s)", len(creds)),
		map[string]any{"credentials": creds, "count": len(creds)},
		nil,
		nil,
	), nil
}
