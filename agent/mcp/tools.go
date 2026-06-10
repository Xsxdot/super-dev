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
	properties["kind"] = map[string]any{"type": "string", "enum": []string{"runtime.start", "runtime.stop", "runtime.restart", "template.import"}}
	properties["template_path"] = map[string]any{"type": "string"}
	delete(properties, "approval_token")
	delete(properties, "debug_session_id")
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
				"description": "Service config. For remote deployments, deployments[].host_ids must contain canonical non-self Host.id values returned by list_hosts, not host name/display name.",
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
				Description: "Create or edit one service and its deployments through the local agent.",
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
