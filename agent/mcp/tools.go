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

func targetInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":    map[string]any{"type": "string"},
			"project_name":  map[string]any{"type": "string"},
			"env_name":      map[string]any{"type": "string"},
			"service_id":    map[string]any{"type": "string"},
			"service_name":  map[string]any{"type": "string"},
			"deployment_id": map[string]any{"type": "string"},
		},
	}
}

func projectInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
		},
	}
}

func defaultTools(s *Server) []registeredTool {
	return []registeredTool{
		{
			Tool: Tool{
				Name:        "list_projects",
				Title:       "List projects",
				Description: "Return SuperDev projects registered in the local agent.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listProjectsTool,
		},
		{
			Tool: Tool{
				Name:        "get_project",
				Title:       "Get project",
				Description: "Return one SuperDev project by ID or name.",
				InputSchema: projectInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getProjectTool,
		},
		{
			Tool: Tool{
				Name:        "get_runtime_snapshot",
				Title:       "Get runtime snapshot",
				Description: "Return a SuperDev-wide runtime snapshot for projects, services, and deployments.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.runtimeSnapshotTool,
		},
		{
			Tool: Tool{
				Name:        "list_services",
				Title:       "List services",
				Description: "Return services and deployment runtime state from the local SuperDev agent.",
				InputSchema: projectInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listServicesTool,
		},
		{
			Tool: Tool{
				Name:        "start_service",
				Title:       "Start service",
				Description: "Start one resolved deployment through the local SuperDev agent.",
				InputSchema: targetInputSchema(),
			},
			Handler: s.startServiceTool,
		},
		{
			Tool: Tool{
				Name:        "stop_service",
				Title:       "Stop service",
				Description: "Stop one resolved deployment through the local SuperDev agent.",
				InputSchema: targetInputSchema(),
				Annotations: map[string]any{"destructiveHint": true},
			},
			Handler: s.stopServiceTool,
		},
		{
			Tool: Tool{
				Name:        "restart_service",
				Title:       "Restart service",
				Description: "Restart one resolved deployment through the local SuperDev agent.",
				InputSchema: targetInputSchema(),
				Annotations: map[string]any{"destructiveHint": true},
			},
			Handler: s.restartServiceTool,
		},
	}
}
