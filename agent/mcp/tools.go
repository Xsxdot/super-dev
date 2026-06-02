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

func tailLogsInputSchema() map[string]any {
	schema := targetInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1}
	properties["run_id"] = map[string]any{"type": "string"}
	properties["before"] = map[string]any{"type": "integer", "minimum": 1}
	properties["level"] = map[string]any{"type": "string"}
	properties["since"] = map[string]any{"type": "string"}
	properties["apply_project_rules"] = map[string]any{"type": "boolean"}
	return schema
}

func searchLogsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"q":             map[string]any{"type": "string"},
			"project_id":    map[string]any{"type": "string"},
			"project_name":  map[string]any{"type": "string"},
			"deployment_id": map[string]any{"type": "string"},
			"limit":         map[string]any{"type": "integer", "minimum": 1},
			"cursor_time":   map[string]any{"type": "string"},
			"cursor_id":     map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"q"},
	}
}

func logContextInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"id":           map[string]any{"type": "integer", "minimum": 1},
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"deployment_id": map[string]any{
				"type": "string",
			},
			"before_ms": map[string]any{"type": "integer", "minimum": 1},
			"after_ms":  map[string]any{"type": "integer", "minimum": 1},
			"limit":     map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"id"},
	}
}

func templateSourceInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
			"yaml": map[string]any{"type": "string"},
		},
	}
}

func templateImportInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []string{"path"},
	}
}

func createDebugSessionInputSchema() map[string]any {
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
			"title":         map[string]any{"type": "string"},
			"question":      map[string]any{"type": "string"},
		},
		"required": []string{"title", "question"},
	}
}

func listDebugSessionsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"status":       map[string]any{"type": "string", "enum": []string{"open", "closed"}},
			"limit":        map[string]any{"type": "integer", "minimum": 1},
		},
	}
}

func getDebugSessionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"limit":      map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"session_id"},
	}
}

func appendDebugSessionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"type":       map[string]any{"type": "string", "enum": []string{"note", "tool_call", "observation"}},
			"actor":      map[string]any{"type": "string", "enum": []string{"user", "assistant", "system"}},
			"summary":    map[string]any{"type": "string"},
			"data":       map[string]any{"type": "object"},
		},
		"required": []string{"session_id", "type", "actor", "summary"},
	}
}

func closeDebugSessionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"summary":    map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
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
		{
			Tool: Tool{
				Name:        "tail_logs",
				Title:       "Tail logs",
				Description: "Fetch recent deployment logs and optionally apply project log rules.",
				InputSchema: tailLogsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.tailLogsTool,
		},
		{
			Tool: Tool{
				Name:        "search_logs",
				Title:       "Search logs",
				Description: "Search SuperDev logs by project or deployment.",
				InputSchema: searchLogsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.searchLogsTool,
		},
		{
			Tool: Tool{
				Name:        "get_log_context",
				Title:       "Get log context",
				Description: "Fetch cross-service log context around one log entry.",
				InputSchema: logContextInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getLogContextTool,
		},
		{
			Tool: Tool{
				Name:        "diagnose_service",
				Title:       "Diagnose service",
				Description: "Collect runtime status and recent log evidence for one deployment without claiming root cause.",
				InputSchema: targetInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.diagnoseServiceTool,
		},
		{
			Tool: Tool{
				Name:        "create_debug_session",
				Title:       "Create debug session",
				Description: "Create a local diagnostic session record only; it does not change runtime state or configuration.",
				InputSchema: createDebugSessionInputSchema(),
			},
			Handler: s.createDebugSessionTool,
		},
		{
			Tool: Tool{
				Name:        "list_debug_sessions",
				Title:       "List debug sessions",
				Description: "List local diagnostic session records from the SuperDev agent.",
				InputSchema: listDebugSessionsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listDebugSessionsTool,
		},
		{
			Tool: Tool{
				Name:        "get_debug_session",
				Title:       "Get debug session",
				Description: "Read one local diagnostic session and its events.",
				InputSchema: getDebugSessionInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getDebugSessionTool,
		},
		{
			Tool: Tool{
				Name:        "append_debug_session_note",
				Title:       "Append debug session note",
				Description: "Append a local diagnostic note or observation only; it does not change runtime state or configuration.",
				InputSchema: appendDebugSessionInputSchema(),
			},
			Handler: s.appendDebugSessionNoteTool,
		},
		{
			Tool: Tool{
				Name:        "close_debug_session",
				Title:       "Close debug session",
				Description: "Close a local diagnostic session record only; it does not change runtime state or configuration.",
				InputSchema: closeDebugSessionInputSchema(),
			},
			Handler: s.closeDebugSessionTool,
		},
		{
			Tool: Tool{
				Name:        "preview_pipeline_template",
				Title:       "Preview pipeline template",
				Description: "Dry-run parse and validate a pipeline template YAML string or file.",
				InputSchema: templateSourceInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.previewPipelineTemplateTool,
		},
		{
			Tool: Tool{
				Name:        "import_pipeline_template",
				Title:       "Import pipeline template",
				Description: "Import a pipeline template file into the local agent template library only.",
				InputSchema: templateImportInputSchema(),
			},
			Handler: s.importPipelineTemplateTool,
		},
	}
}
