// tools_langruntime_test.go 验证 Language Runtime Provider MCP 工具。
//
// 职责：
//   - 验证 schema/suggest/validate/preview 工具委托 AgentClient
//   - 验证 provider 列表工具返回稳定数据 key
//   - 验证必填语言参数不会静默为空
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不校验具体语言 provider 内部逻辑
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func callLanguageRuntimeTool(t *testing.T, server *Server, name string, args string) CallToolResult {
	t.Helper()
	tool, ok := server.tools[name]
	require.True(t, ok, "tool %s should be registered", name)
	result, err := tool.Handler(context.Background(), []byte(args))
	require.NoError(t, err)
	return result
}

func TestListLanguageRuntimeProvidersToolReturnsLanguages(t *testing.T) {
	client := &fakeAgentClient{languageRuntimeProviders: []string{"go"}}
	server := NewServer(client)

	result := callLanguageRuntimeTool(t, server, "list_language_runtime_providers", `{}`)

	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	assert.Equal(t, []string{"go"}, data["languages"])
}

func TestDescribeLanguageRuntimeSchemaToolReturnsSchema(t *testing.T) {
	client := &fakeAgentClient{languageRuntimeSchema: map[string]any{"language": "go", "fields": []any{map[string]any{"key": "program"}}}}
	server := NewServer(client)

	result := callLanguageRuntimeTool(t, server, "describe_language_runtime_schema", `{"language":"go"}`)

	assert.False(t, result.IsError)
	assert.Equal(t, "describe", client.lastLanguageRuntimeMethod)
	assert.Equal(t, "go", client.lastLanguageRuntimeLanguage)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	assert.Equal(t, client.languageRuntimeSchema, data["schema"])
}

func TestDescribeLanguageRuntimeSchemaToolRequiresLanguage(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result := callLanguageRuntimeTool(t, server, "describe_language_runtime_schema", `{}`)

	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "invalid_arguments", payload.Code)
}

func TestSuggestServiceRuntimeToolPassesConfigInput(t *testing.T) {
	client := &fakeAgentClient{languageRuntimeResponse: map[string]any{
		"suggestions": []any{map[string]any{"label": "Go ./cmd/server"}},
	}}
	server := NewServer(client)

	result := callLanguageRuntimeTool(t, server, "suggest_service_runtime", `{"language":"go","project_root":"/repo","cwd":"./server"}`)

	assert.False(t, result.IsError)
	assert.Equal(t, "suggest", client.lastLanguageRuntimeMethod)
	assert.Equal(t, "/repo", client.lastLanguageRuntimeBody["project_root"])
	assert.Equal(t, "./server", client.lastLanguageRuntimeBody["cwd"])
}

func TestValidateServiceRuntimeToolPassesRuntimeConfig(t *testing.T) {
	client := &fakeAgentClient{languageRuntimeResponse: map[string]any{"valid": true, "diagnostics": []any{}}}
	server := NewServer(client)

	result := callLanguageRuntimeTool(t, server, "validate_service_runtime", `{"language":"go","project_root":"/repo","config":{"program":"."}}`)

	assert.False(t, result.IsError)
	assert.Equal(t, "validate", client.lastLanguageRuntimeMethod)
	assert.Equal(t, map[string]any{"program": "."}, client.lastLanguageRuntimeBody["config"])
}

func TestPreviewServiceExecutionToolPassesIntent(t *testing.T) {
	client := &fakeAgentClient{languageRuntimeResponse: map[string]any{"preview": "go build && ./app"}}
	server := NewServer(client)

	result := callLanguageRuntimeTool(t, server, "preview_service_execution", `{"language":"go","project_root":"/repo","intent":"start_dev","artifact_dir":"/tmp/run-bin/x"}`)

	assert.False(t, result.IsError)
	assert.Equal(t, "preview", client.lastLanguageRuntimeMethod)
	assert.Equal(t, "start_dev", client.lastLanguageRuntimeBody["intent"])
	assert.Equal(t, "/tmp/run-bin/x", client.lastLanguageRuntimeBody["artifact_dir"])
}
