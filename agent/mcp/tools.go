// Package mcp exposes SuperDev agent capabilities through Model Context Protocol tools.
//
// 职责：
//   - 定义 MCP tool registry
//   - 将工具调用分发到运行态、日志、诊断和模板处理函数
//
// 边界：
//   - 不直接读取配置文件或 SQLite
//   - 不直接管理进程
//   - 业务数据全部来自 AgentClient
package mcp

import (
	"context"
	"encoding/json"
)

// Tool 是 MCP tools/list 返回的工具定义。
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

type toolHandler func(context.Context, json.RawMessage) (CallToolResult, error)

type registeredTool struct {
	Tool    Tool
	Handler toolHandler
}

func emptyInputSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false}
}

func defaultTools(s *Server) []registeredTool {
	return []registeredTool{
		{
			Tool: Tool{
				Name:        "get_runtime_snapshot",
				Title:       "Get runtime snapshot",
				Description: "Return a SuperDev-wide runtime snapshot for projects, services, and deployments.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.toolNotImplemented,
		},
	}
}
