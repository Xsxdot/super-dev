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
			"project_id":            map[string]any{"type": "string"},
			"project_name":          map[string]any{"type": "string"},
			"env_name":              map[string]any{"type": "string"},
			"service_id":            map[string]any{"type": "string"},
			"service_name":          map[string]any{"type": "string"},
			"deployment_id":         map[string]any{"type": "string"},
			"approval_token":        map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 300},
			"debug_session_id":      map[string]any{"type": "string"},
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

func debugCredentialsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"service_id":   map[string]any{"type": "string"},
			"service_name": map[string]any{"type": "string"},
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
			"path":             map[string]any{"type": "string"},
			"approval_token":   map[string]any{"type": "string"},
			"debug_session_id": map[string]any{"type": "string"},
		},
		"required": []string{"path"},
	}
}

func previewOperationInputSchema() map[string]any {
	schema := targetInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["kind"] = map[string]any{"type": "string", "enum": []string{"runtime.start", "runtime.stop", "runtime.restart", "template.import", "browser_debug.open", "code_debug.open", "code_debug.evaluate"}}
	properties["path"] = map[string]any{"type": "string"}
	properties["template_path"] = map[string]any{"type": "string"}
	properties["expression_hash"] = map[string]any{"type": "string"}
	delete(properties, "approval_token")
	schema["required"] = []string{"kind"}
	return schema
}

func listOperationApprovalsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"status":       map[string]any{"type": "string"},
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"limit":        map[string]any{"type": "integer", "minimum": 1},
		},
	}
}

func getOperationApprovalInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"approval_id": map[string]any{"type": "string"},
		},
		"required": []string{"approval_id"},
	}
}

func listOperationAuditInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"kind":         map[string]any{"type": "string"},
			"approval_id":  map[string]any{"type": "string"},
			"since":        map[string]any{"type": "string"},
			"limit":        map[string]any{"type": "integer", "minimum": 1},
		},
	}
}

func configChangeInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"kind":             map[string]any{"type": "string", "enum": []string{"config.project.upsert", "config.service.upsert", "config.pipeline.upsert"}},
			"project_id":       map[string]any{"type": "string"},
			"project_name":     map[string]any{"type": "string"},
			"root_path":        map[string]any{"type": "string"},
			"approval_token":   map[string]any{"type": "string"},
			"debug_session_id": map[string]any{"type": "string"},
			"project":          map[string]any{"type": "object"},
			"service": map[string]any{
				"type":        "object",
				"description": "Service config. Set service.language (go, node, python) as the service implementation language; it is required for local managed runtime.type=language deployments. For remote deployments, deployments[].host_ids must contain canonical non-self Host.id values returned by list_hosts, not host name/display name.",
			},
			"pipeline": map[string]any{
				"type": "object",
				"description": "Project pipeline config. Key fields: " +
					"`variables` are pipeline-wide defaults (strings). " +
					"`environments` is a map of env name -> { variables } that OVERRIDE defaults per environment (precedence: project < pipeline < pipeline.pipeline < environments[env] < run vars); the same pipeline serves multiple environments by overriding only the differing variables. " +
					"`sync_mode` declares how build artifacts reach targets: \"transfer\" (agent uploads the packaged artifact) or \"remote_cmd\" (the target host runs a command such as git pull to fetch code itself); empty defaults to transfer. " +
					"`roles` maps a role/run-group name to a host list (run groups); a role entry uses {from_service} to derive hosts from a service's deployment for the running env, or {hosts:[...]} for an explicit set. Only plugins that need to target a specific group (e.g. nginx upstream) reference roles; most deploy steps follow the env deployment targets automatically. " +
					"Any host_ids, roles hosts, or task host_id values must use canonical non-self Host.id values returned by list_hosts, not host names.",
			},
		},
		"required": []string{"kind"},
	}
}

func projectConfigInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id": map[string]any{"type": "string"},
			"root_path":  map[string]any{"type": "string"},
		},
	}
}

func languageSchemaInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"language": map[string]any{"type": "string"},
		},
		"required": []string{"language"},
	}
}

func languageRuntimeSuggestInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"language":     map[string]any{"type": "string"},
			"project_root": map[string]any{"type": "string"},
			"cwd":          map[string]any{"type": "string"},
		},
		"required": []string{"language", "project_root"},
	}
}

func languageRuntimeConfigInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"language":     map[string]any{"type": "string"},
			"project_root": map[string]any{"type": "string"},
			"cwd":          map[string]any{"type": "string"},
			"env":          map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"config":       map[string]any{"type": "object"},
		},
		"required": []string{"language", "project_root"},
	}
}

func languageRuntimePreviewInputSchema() map[string]any {
	schema := languageRuntimeConfigInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["intent"] = map[string]any{"type": "string", "enum": []string{"start_dev", "start_normal", "debug_launch", "attach"}}
	properties["artifact_dir"] = map[string]any{"type": "string"}
	return schema
}

func upsertServiceInputSchema() map[string]any {
	schema := configChangeInputSchema()
	delete(schema["properties"].(map[string]any), "kind")
	schema["required"] = []string{"service"}
	return schema
}

func upsertProjectPipelineInputSchema() map[string]any {
	schema := configChangeInputSchema()
	delete(schema["properties"].(map[string]any), "kind")
	schema["required"] = []string{"pipeline"}
	return schema
}

func deployProjectPipelineInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"pipeline_id":  map[string]any{"type": "string"},
			"env_name":     map[string]any{"type": "string"},
			"host_ids": map[string]any{
				"type":        "array",
				"description": "Canonical non-self Host.id values returned by list_hosts; do not pass host names.",
				"items":       map[string]any{"type": "string"},
			},
			"artifact_version": map[string]any{
				"type": "string",
				"description": "Reuse an existing built artifact instead of building fresh. Empty = normal deploy (build then deploy). " +
					"Set it to deploy an existing artifact version to env_name, which SKIPS the build phase and runs deploy+finally only. " +
					"This powers both rollback (redeploy an old version) and promotion (deploy the artifact a test run produced into prod with prod's variables and deployment targets).",
			},
			"variables":      map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
			"approval_token": map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{
				"type":        "integer",
				"description": "Max seconds to block-wait for human approval before returning. 0 = do not wait. Capped at 300.",
			},
			"debug_session_id": map[string]any{"type": "string"},
		},
		"required": []string{"pipeline_id", "env_name"},
	}
}

func validateProjectPipelineInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":    map[string]any{"type": "string"},
			"project_name":  map[string]any{"type": "string"},
			"pipeline_id":   map[string]any{"type": "string"},
			"env_name":      map[string]any{"type": "string"},
			"service_names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"variables":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
		},
		"required": []string{"pipeline_id", "env_name"},
	}
}

func listPipelineRunsInputSchema() map[string]any {
	schema := projectInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["pipeline_id"] = map[string]any{"type": "string"}
	schema["required"] = []string{"pipeline_id"}
	return schema
}

func listPipelineArtifactsInputSchema() map[string]any {
	return listPipelineRunsInputSchema()
}

func readPipelineRunLogsInputSchema() map[string]any {
	schema := listPipelineRunsInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["run_id"] = map[string]any{"type": "string"}
	properties["step_name"] = map[string]any{"type": "string"}
	properties["host_id"] = map[string]any{"type": "string"}
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1}
	properties["before"] = map[string]any{"type": "integer", "minimum": 1}
	schema["required"] = []string{"pipeline_id", "run_id"}
	return schema
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

func openBrowserSessionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id":         map[string]any{"type": "string"},
			"browser_id":            map[string]any{"type": "string"},
			"path":                  map[string]any{"type": "string"},
			"open_devtools":         map[string]any{"type": "boolean"},
			"viewport_width":        map[string]any{"type": "integer", "minimum": 320, "maximum": 10000},
			"viewport_height":       map[string]any{"type": "integer", "minimum": 240, "maximum": 10000},
			"approval_token":        map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{"type": "integer", "minimum": 0, "maximum": 300},
			"debug_session_id":      map[string]any{"type": "string"},
		},
		"required": []string{"deployment_id"},
	}
}

func closeBrowserSessionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id"},
	}
}

func browserSnapshotInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
			"max_text":   map[string]any{"type": "integer", "minimum": 1},
			"max_elements": map[string]any{
				"type":    "integer",
				"minimum": 1,
				"maximum": 200,
			},
		},
		"required": []string{"session_id"},
	}
}

func browserClickInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
		},
		"required": []string{"session_id", "selector"},
	}
}

func browserTypeInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
			"text":       map[string]any{"type": "string"},
			"fill":       map[string]any{"type": "boolean"},
		},
		"required": []string{"session_id", "selector", "text"},
	}
}

func browserScreenshotInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"full_page":  map[string]any{"type": "boolean"},
		},
		"required": []string{"session_id"},
	}
}

func browserNavigateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"url":        map[string]any{"type": "string"},
			"path":       map[string]any{"type": "string"},
			"wait_until": map[string]any{"type": "string", "enum": []string{"load", "domcontentloaded", "networkidle", "commit"}},
		},
		"required": []string{"session_id"},
	}
}

func browserReloadInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"wait_until": map[string]any{"type": "string", "enum": []string{"load", "domcontentloaded", "networkidle", "commit"}},
		},
		"required": []string{"session_id"},
	}
}

func browserWaitForSelectorInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
			"state":      map[string]any{"type": "string", "enum": []string{"attached", "detached", "visible", "hidden"}},
			"timeout_ms": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"session_id", "selector"},
	}
}

func browserPressKeyInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
			"key":        map[string]any{"type": "string"},
		},
		"required": []string{"session_id", "key"},
	}
}

func browserSelectOptionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"selector":   map[string]any{"type": "string"},
			"value":      map[string]any{"type": "string"},
			"label":      map[string]any{"type": "string"},
		},
		"required": []string{"session_id", "selector"},
	}
}

func browserConsoleLogsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"level":      map[string]any{"type": "string", "enum": []string{"log", "info", "warning", "error"}},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		},
		"required": []string{"session_id"},
	}
}

func browserNetworkRequestsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"filter":     map[string]any{"type": "string"},
			"limit":      map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		},
		"required": []string{"session_id"},
	}
}

func browserEvaluateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"expression": map[string]any{"type": "string"},
		},
		"required": []string{"session_id", "expression"},
	}
}

func browserSetViewportInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string"},
			"width":      map[string]any{"type": "integer", "minimum": 320, "maximum": 10000},
			"height":     map[string]any{"type": "integer", "minimum": 240, "maximum": 10000},
		},
		"required": []string{"session_id", "width", "height"},
	}
}

func debugCaptureAtInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id":         map[string]any{"type": "string", "description": "Local managed language runtime deployment to debug. The agent resolves or creates the internal lease."},
			"source":                map[string]any{"type": "string"},
			"line":                  map[string]any{"type": "integer", "minimum": 1},
			"thread_id":             map[string]any{"type": "integer", "minimum": 0},
			"timeout_ms":            map[string]any{"type": "integer", "minimum": 1},
			"max_variables":         map[string]any{"type": "integer", "minimum": 1},
			"variable_names":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"approval_token":        map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{"type": "integer", "minimum": 0},
		},
		"required": []string{"deployment_id", "source", "line"},
	}
}

func debugInspectInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id":  map[string]any{"type": "string"},
			"thread_id":      map[string]any{"type": "integer", "minimum": 0},
			"frame_id":       map[string]any{"type": "integer", "minimum": 0},
			"max_variables":  map[string]any{"type": "integer", "minimum": 1},
			"variable_names": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"deployment_id"},
	}
}

func codeDebugBreakpointsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id": map[string]any{"type": "string"},
			"source":        map[string]any{"type": "string"},
			"lines":         map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 1}},
		},
		"required": []string{"deployment_id", "source", "lines"},
	}
}

func codeDebugThreadActionInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id": map[string]any{"type": "string"},
			"thread_id":     map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"deployment_id", "thread_id"},
	}
}

func codeDebugStackInputSchema() map[string]any {
	return codeDebugThreadActionInputSchema()
}

func codeDebugScopesInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id": map[string]any{"type": "string"},
			"frame_id":      map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"deployment_id", "frame_id"},
	}
}

func codeDebugVariablesInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id":       map[string]any{"type": "string"},
			"variables_reference": map[string]any{"type": "integer", "minimum": 1},
		},
		"required": []string{"deployment_id", "variables_reference"},
	}
}

func codeDebugEvaluateInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"deployment_id":         map[string]any{"type": "string"},
			"expression":            map[string]any{"type": "string"},
			"frame_id":              map[string]any{"type": "integer", "minimum": 0},
			"approval_token":        map[string]any{"type": "string"},
			"approval_wait_seconds": map[string]any{"type": "integer", "minimum": 0},
		},
		"required": []string{"deployment_id", "expression"},
	}
}

func analyzeTraceLogsInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":   map[string]any{"type": "string"},
			"project_name": map[string]any{"type": "string"},
			"trace_id":     map[string]any{"type": "string"},
			"request_id":   map[string]any{"type": "string"},
			"limit":        map[string]any{"type": "integer", "minimum": 1},
			"before_ms":    map[string]any{"type": "integer", "minimum": 1},
			"after_ms":     map[string]any{"type": "integer", "minimum": 1},
		},
	}
}

func summarizeErrorWindowInputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"project_id":    map[string]any{"type": "string"},
			"project_name":  map[string]any{"type": "string"},
			"deployment_id": map[string]any{"type": "string"},
			"from":          map[string]any{"type": "string"},
			"to":            map[string]any{"type": "string"},
			"since":         map[string]any{"type": "string"},
			"limit":         map[string]any{"type": "integer", "minimum": 1},
		},
	}
}

func appendLogAnalysisInputSchema() map[string]any {
	schema := analyzeTraceLogsInputSchema()
	properties := schema["properties"].(map[string]any)
	properties["session_id"] = map[string]any{"type": "string"}
	properties["analysis_type"] = map[string]any{"type": "string", "enum": []string{"trace", "error_window"}}
	properties["from"] = map[string]any{"type": "string"}
	properties["to"] = map[string]any{"type": "string"}
	properties["since"] = map[string]any{"type": "string"}
	properties["deployment_id"] = map[string]any{"type": "string"}
	schema["required"] = []string{"session_id", "analysis_type"}
	return schema
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
				Name:        "list_hosts",
				Title:       "List hosts",
				Description: "Return host selection records. Use non-self host id fields as canonical remote host_ids values; name is display-only.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listHostsTool,
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
				Name:        "get_debug_credentials",
				Title:       "Get debug credentials",
				Description: "Return plaintext debug credentials (test login/password, service api-key) for AI to log in or authenticate legitimately during debugging instead of fabricating tokens or bypassing auth. project_id|project_name required; pass service to merge project+service level (service overrides). Read-only, not approval-gated.",
				InputSchema: debugCredentialsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getDebugCredentialsTool,
		},
		{
			Tool: Tool{
				Name:        "preview_operation",
				Title:       "Preview operation",
				Description: "Create a deterministic safety preflight plan for one write operation without creating an approval request.",
				InputSchema: previewOperationInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.previewOperationTool,
		},
		{
			Tool: Tool{
				Name:        "list_operation_approvals",
				Title:       "List operation approvals",
				Description: "List pending or historical operation approval requests.",
				InputSchema: listOperationApprovalsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listOperationApprovalsTool,
		},
		{
			Tool: Tool{
				Name:        "get_operation_approval",
				Title:       "Get operation approval",
				Description: "Read one operation approval and return a one-time token when approved.",
				InputSchema: getOperationApprovalInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getOperationApprovalTool,
		},
		{
			Tool: Tool{
				Name:        "list_operation_audit",
				Title:       "List operation audit",
				Description: "List local operation safety audit events.",
				InputSchema: listOperationAuditInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listOperationAuditTool,
		},
		{
			Tool: Tool{
				Name:        "probe_project_config",
				Title:       "Probe project config",
				Description: "Probe a project directory through the local agent without writing config.",
				InputSchema: projectConfigInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.probeProjectConfigTool,
		},
		{
			Tool: Tool{
				Name:        "get_project_config",
				Title:       "Get project config",
				Description: "Read an editable project config snapshot from the local agent.",
				InputSchema: projectConfigInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.getProjectConfigTool,
		},
		{
			Tool: Tool{
				Name:        "list_language_runtime_providers",
				Title:       "List language runtime providers",
				Description: "Return languages with runtime providers. Use before creating a language service so you can choose a supported provider.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listLanguageRuntimeProvidersTool,
		},
		{
			Tool: Tool{
				Name:        "describe_language_runtime_schema",
				Title:       "Describe language runtime schema",
				Description: "Return the config field schema for a language runtime provider, such as go. Use before creating a service so you fill cwd/env/config fields instead of guessing a command string.",
				InputSchema: languageSchemaInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.describeLanguageRuntimeSchemaTool,
		},
		{
			Tool: Tool{
				Name:        "suggest_service_runtime",
				Title:       "Suggest service runtime",
				Description: "Suggest schema-shaped runtime config for a language service from project_root and cwd. Use the result as a draft, then validate it.",
				InputSchema: languageRuntimeSuggestInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.suggestServiceRuntimeTool,
		},
		{
			Tool: Tool{
				Name:        "validate_service_runtime",
				Title:       "Validate service runtime",
				Description: "Validate cwd/env/config for a language service and return diagnostics before upsert_service.",
				InputSchema: languageRuntimeConfigInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.validateServiceRuntimeTool,
		},
		{
			Tool: Tool{
				Name:        "preview_service_execution",
				Title:       "Preview service execution",
				Description: "Preview the execution plan for a language service intent without starting it. Use this to explain what start_dev/start_normal/debug_launch would run.",
				InputSchema: languageRuntimePreviewInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.previewServiceExecutionTool,
		},
		{
			Tool: Tool{
				Name:        "preview_config_change",
				Title:       "Preview config change",
				Description: "Preview a project/service/project-pipeline config upsert without writing YAML.",
				InputSchema: configChangeInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.previewConfigChangeTool,
		},
		{
			Tool: Tool{
				Name:        "apply_config_change",
				Title:       "Apply config change",
				Description: "Apply a config upsert through the local agent safe-operation flow.",
				InputSchema: configChangeInputSchema(),
			},
			Handler: s.applyConfigChangeTool,
		},
		{
			Tool: Tool{
				Name:        "upsert_project_config",
				Title:       "Upsert project config",
				Description: "Create or edit project base config through the local agent.",
				InputSchema: configChangeInputSchema(),
			},
			Handler: s.upsertProjectConfigTool,
		},
		{
			Tool: Tool{
				Name:        "upsert_service",
				Title:       "Upsert service",
				Description: "Create or edit one service and its deployments through the local agent. Set service.language when using local managed runtime.type=language.",
				InputSchema: upsertServiceInputSchema(),
			},
			Handler: s.upsertServiceTool,
		},
		{
			Tool: Tool{
				Name:        "upsert_project_pipeline",
				Title:       "Upsert project pipeline",
				Description: "Create or edit one project-level pipeline through the local agent.",
				InputSchema: upsertProjectPipelineInputSchema(),
			},
			Handler: s.upsertProjectPipelineTool,
		},
		{
			Tool: Tool{
				Name:        "validate_project_pipeline",
				Title:       "Validate project pipeline",
				Description: "Validate an already saved project-level pipeline without executing it.",
				InputSchema: validateProjectPipelineInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.validateProjectPipelineTool,
		},
		{
			Tool: Tool{
				Name:        "deploy_project_pipeline",
				Title:       "Deploy project pipeline",
				Description: "Execute a project-level pipeline deploy or rollback through the local agent.",
				InputSchema: deployProjectPipelineInputSchema(),
			},
			Handler: s.deployProjectPipelineTool,
		},
		{
			Tool: Tool{
				Name:        "list_pipeline_runs",
				Title:       "List pipeline runs",
				Description: "List project-level pipeline execution history.",
				InputSchema: listPipelineRunsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listPipelineRunsTool,
		},
		{
			Tool: Tool{
				Name:        "list_pipeline_artifacts",
				Title:       "List pipeline artifacts",
				Description: "List project-level pipeline artifact history.",
				InputSchema: listPipelineArtifactsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listPipelineArtifactsTool,
		},
		{
			Tool: Tool{
				Name:        "read_pipeline_run_logs",
				Title:       "Read pipeline run logs",
				Description: "Read stored logs for one project-level pipeline run.",
				InputSchema: readPipelineRunLogsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.readPipelineRunLogsTool,
		},
		{
			Tool: Tool{
				Name:        "start_service",
				Title:       "Start service",
				Description: "Start one resolved deployment. If approval is required, wait for desktop approval by default and resume with a one-time token.",
				InputSchema: targetInputSchema(),
			},
			Handler: s.startServiceTool,
		},
		{
			Tool: Tool{
				Name:        "stop_service",
				Title:       "Stop service",
				Description: "Stop one resolved deployment. If approval is required, wait for desktop approval by default and resume with a one-time token.",
				InputSchema: targetInputSchema(),
				Annotations: map[string]any{"destructiveHint": true},
			},
			Handler: s.stopServiceTool,
		},
		{
			Tool: Tool{
				Name:        "restart_service",
				Title:       "Restart service",
				Description: "Restart one resolved deployment. If approval is required, wait for desktop approval by default and resume with a one-time token.",
				InputSchema: targetInputSchema(),
				Annotations: map[string]any{"destructiveHint": true},
			},
			Handler: s.restartServiceTool,
		},
		{
			Tool: Tool{
				Name:        "list_debug_browsers",
				Title:       "List debug browsers",
				Description: "Return local Chromium-compatible browsers configured for SuperDev frontend debugging.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listDebugBrowsersTool,
		},
		{
			Tool: Tool{
				Name:        "list_browser_targets",
				Title:       "List browser targets",
				Description: "Return local frontend deployments configured with web entrypoints.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listBrowserTargetsTool,
		},
		{
			Tool: Tool{
				Name:        "open_browser_debug_session",
				Title:       "Open browser debug session",
				Description: "Open a local frontend deployment in an isolated debug browser and return CDP WebSocket endpoints.",
				InputSchema: openBrowserSessionInputSchema(),
			},
			Handler: s.openBrowserDebugSessionTool,
		},
		{
			Tool: Tool{
				Name:        "close_browser_debug_session",
				Title:       "Close browser debug session",
				Description: "Close a browser debug session created by SuperDev.",
				InputSchema: closeBrowserSessionInputSchema(),
				Annotations: map[string]any{"destructiveHint": true},
			},
			Handler: s.closeBrowserDebugSessionTool,
		},
		{
			Tool: Tool{
				Name:        "browser_snapshot",
				Title:       "Browser snapshot",
				Description: "Read a text snapshot from a browser debug session page.",
				InputSchema: browserSnapshotInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.browserSnapshotTool,
		},
		{
			Tool: Tool{
				Name:        "browser_click",
				Title:       "Browser click",
				Description: "Click an element in a browser debug session page.",
				InputSchema: browserClickInputSchema(),
			},
			Handler: s.browserClickTool,
		},
		{
			Tool: Tool{
				Name:        "browser_type",
				Title:       "Browser type",
				Description: "Type or fill text into an element in a browser debug session page.",
				InputSchema: browserTypeInputSchema(),
			},
			Handler: s.browserTypeTool,
		},
		{
			Tool: Tool{
				Name:        "browser_screenshot",
				Title:       "Browser screenshot",
				Description: "Capture a PNG screenshot from a browser debug session page.",
				InputSchema: browserScreenshotInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.browserScreenshotTool,
		},
		{
			Tool: Tool{
				Name:        "browser_navigate",
				Title:       "Browser navigate",
				Description: "Perform same-origin full page navigation in a browser debug session. This may reload the page and lose SPA in-memory state; prefer browser_click for SPA route changes.",
				InputSchema: browserNavigateInputSchema(),
			},
			Handler: s.browserNavigateTool,
		},
		{
			Tool: Tool{
				Name:        "browser_reload",
				Title:       "Browser reload",
				Description: "Reload the current page in a browser debug session.",
				InputSchema: browserReloadInputSchema(),
			},
			Handler: s.browserReloadTool,
		},
		{
			Tool: Tool{
				Name:        "browser_wait_for_selector",
				Title:       "Browser wait for selector",
				Description: "Wait until a selector reaches a requested state in a browser debug session.",
				InputSchema: browserWaitForSelectorInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.browserWaitForSelectorTool,
		},
		{
			Tool: Tool{
				Name:        "browser_press_key",
				Title:       "Browser press key",
				Description: "Press a keyboard key in a browser debug session, optionally focusing a selector first.",
				InputSchema: browserPressKeyInputSchema(),
			},
			Handler: s.browserPressKeyTool,
		},
		{
			Tool: Tool{
				Name:        "browser_select_option",
				Title:       "Browser select option",
				Description: "Select an option by value or label in a browser debug session page.",
				InputSchema: browserSelectOptionInputSchema(),
			},
			Handler: s.browserSelectOptionTool,
		},
		{
			Tool: Tool{
				Name:        "browser_console_logs",
				Title:       "Browser console logs",
				Description: "Read recent console logs captured from a browser debug session page.",
				InputSchema: browserConsoleLogsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.browserConsoleLogsTool,
		},
		{
			Tool: Tool{
				Name:        "browser_network_requests",
				Title:       "Browser network requests",
				Description: "Read recent network request summaries captured from a browser debug session page.",
				InputSchema: browserNetworkRequestsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.browserNetworkRequestsTool,
		},
		{
			Tool: Tool{
				Name:        "browser_evaluate",
				Title:       "Browser evaluate",
				Description: "Evaluate JavaScript in a browser debug session page and return the serializable result.",
				InputSchema: browserEvaluateInputSchema(),
			},
			Handler: s.browserEvaluateTool,
		},
		{
			Tool: Tool{
				Name:        "browser_set_viewport",
				Title:       "Browser set viewport",
				Description: "Set the viewport size for a browser debug session page before snapshot or screenshot checks.",
				InputSchema: browserSetViewportInputSchema(),
			},
			Handler: s.browserSetViewportTool,
		},
		{
			Tool: Tool{
				Name:        "debug_capture_at",
				Title:       "Debug capture at",
				Description: "Last-resort code debug for a deployment: stop at a source line and return stack/scopes/variables in one call. Use logs and diagnose tools first.",
				InputSchema: debugCaptureAtInputSchema(),
			},
			Handler: s.debugCaptureAtTool,
		},
		{
			Tool: Tool{
				Name:        "debug_inspect",
				Title:       "Debug inspect",
				Description: "Inspect a paused deployment debug runtime and return stack/scopes/variables in one call. Prefer this over chaining low-level DAP tools.",
				InputSchema: debugInspectInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.debugInspectTool,
		},
		{
			Tool: Tool{
				Name:        "list_code_debug_targets",
				Title:       "List code debug targets",
				Description: "List local managed language runtime deployments that can use last-resort code debugging. Node targets are experimental.",
				InputSchema: emptyInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.listCodeDebugTargetsTool,
		},
		{
			Tool: Tool{
				Name:        "set_debug_breakpoints",
				Title:       "Set debug breakpoints",
				Description: "Low-level DAP escape hatch: set source breakpoints for a deployment debug runtime.",
				InputSchema: codeDebugBreakpointsInputSchema(),
			},
			Handler: s.setDebugBreakpointsTool,
		},
		{
			Tool: Tool{
				Name:        "debug_continue",
				Title:       "Debug continue",
				Description: "Low-level DAP escape hatch: continue one paused thread for a deployment debug runtime.",
				InputSchema: codeDebugThreadActionInputSchema(),
			},
			Handler: s.debugContinueTool,
		},
		{
			Tool: Tool{
				Name:        "debug_pause",
				Title:       "Debug pause",
				Description: "Low-level DAP escape hatch: pause one thread for a deployment debug runtime.",
				InputSchema: codeDebugThreadActionInputSchema(),
			},
			Handler: s.debugPauseTool,
		},
		{
			Tool: Tool{
				Name:        "debug_step_over",
				Title:       "Debug step over",
				Description: "Low-level DAP escape hatch: step over one paused thread in a deployment debug runtime.",
				InputSchema: codeDebugThreadActionInputSchema(),
			},
			Handler: s.debugStepOverTool,
		},
		{
			Tool: Tool{
				Name:        "debug_step_in",
				Title:       "Debug step in",
				Description: "Low-level DAP escape hatch: step into one paused thread in a deployment debug runtime.",
				InputSchema: codeDebugThreadActionInputSchema(),
			},
			Handler: s.debugStepInTool,
		},
		{
			Tool: Tool{
				Name:        "debug_step_out",
				Title:       "Debug step out",
				Description: "Low-level DAP escape hatch: step out from one paused thread in a deployment debug runtime.",
				InputSchema: codeDebugThreadActionInputSchema(),
			},
			Handler: s.debugStepOutTool,
		},
		{
			Tool: Tool{
				Name:        "debug_stack_trace",
				Title:       "Debug stack trace",
				Description: "Low-level DAP escape hatch: read stack frames from a deployment debug runtime.",
				InputSchema: codeDebugStackInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.debugStackTraceTool,
		},
		{
			Tool: Tool{
				Name:        "debug_scopes",
				Title:       "Debug scopes",
				Description: "Low-level DAP escape hatch: read scopes for one stack frame.",
				InputSchema: codeDebugScopesInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.debugScopesTool,
		},
		{
			Tool: Tool{
				Name:        "debug_variables",
				Title:       "Debug variables",
				Description: "Low-level DAP escape hatch: read variables for one scope. Secret-looking values are redacted by the agent.",
				InputSchema: codeDebugVariablesInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.debugVariablesTool,
		},
		{
			Tool: Tool{
				Name:        "debug_evaluate",
				Title:       "Debug evaluate",
				Description: "Low-level DAP escape hatch: evaluate an expression inside the debuggee. This is approval-gated and expression-level audited.",
				InputSchema: codeDebugEvaluateInputSchema(),
			},
			Handler: s.debugEvaluateTool,
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
				Name:        "analyze_trace_logs",
				Title:       "Analyze trace logs",
				Description: "Collect deterministic trace/request log evidence without claiming root cause.",
				InputSchema: analyzeTraceLogsInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.analyzeTraceLogsTool,
		},
		{
			Tool: Tool{
				Name:        "summarize_error_window",
				Title:       "Summarize error window",
				Description: "Summarize deterministic error signals in a project or deployment time window without claiming root cause.",
				InputSchema: summarizeErrorWindowInputSchema(),
				Annotations: map[string]any{"readOnlyHint": true},
			},
			Handler: s.summarizeErrorWindowTool,
		},
		{
			Tool: Tool{
				Name:        "append_log_analysis_to_session",
				Title:       "Append log analysis to debug session",
				Description: "Run deterministic log analysis and append the result as a local diagnostic record only; it does not change runtime state or configuration.",
				InputSchema: appendLogAnalysisInputSchema(),
			},
			Handler: s.appendLogAnalysisToSessionTool,
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
