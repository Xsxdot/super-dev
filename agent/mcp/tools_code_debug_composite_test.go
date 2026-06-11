// tools_code_debug_composite_test.go 验证代码调试复合 MCP 工具。
//
// 职责：
//   - 覆盖 AI 默认使用的 debug_capture_at 和 debug_inspect
//   - 验证 debug_capture_at schema 明确 session_id/deployment_id 二选一
//
// 边界：
//   - 不连接真实 DAP adapter
//   - 不绕过 agent API 的审批和审计链路
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDebugCaptureAtToolUsesCompositeClientCall(t *testing.T) {
	client := &fakeAgentClient{
		codeDebugCaptureResult: map[string]any{
			"stack":     []any{map[string]any{"name": "main.main"}},
			"variables": []any{map[string]any{"name": "id", "value": "42"}},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_capture_at", `{"deployment_id":"dep-api-dev","source":"main.go","line":12,"approval_token":"tok_1"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
	assert.Equal(t, "dep-api-dev", client.lastCaptureAt.DeploymentID)
	assert.Equal(t, "main.go", client.lastCaptureAt.Source)
	assert.Contains(t, result.Content[0]["text"], "code debug capture completed")
}

func TestDebugInspectToolUsesCompositeClientCall(t *testing.T) {
	client := &fakeAgentClient{
		codeDebugInspectResult: map[string]any{"stack": []any{map[string]any{"name": "handler"}}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_inspect", `{"session_id":"cds_1","thread_id":1}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "cds_1", client.lastInspect.SessionID)
	assert.Contains(t, result.Content[0]["text"], "code debug inspect completed")
}

func TestDebugCaptureAtSchemaRequiresSessionOrDeployment(t *testing.T) {
	schema := debugCaptureAtInputSchema()

	assert.Equal(t, []string{"source", "line"}, schema["required"])
	require.Contains(t, schema, "oneOf")
	oneOf := schema["oneOf"].([]map[string]any)
	require.Len(t, oneOf, 2)
	assert.Equal(t, []string{"session_id"}, oneOf[0]["required"])
	assert.Equal(t, []string{"deployment_id"}, oneOf[1]["required"])
}
