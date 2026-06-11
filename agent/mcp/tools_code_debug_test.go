// tools_code_debug_test.go 验证本机代码调试低层 MCP 工具。
//
// 职责：
//   - 覆盖代码调试目标、session、断点、执行控制、调用栈和 evaluate 工具
//   - 确认审批 token、experimental 标记和 evaluate 来源正确转发
//
// 边界：
//   - 不连接真实 DAP adapter
//   - 不启动真实本机服务
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListCodeDebugTargetsToolMarksNodeExperimental(t *testing.T) {
	server := NewServer(&fakeAgentClient{
		codeDebugTargets: []CodeDebugTarget{{DeploymentID: "dep-web-dev", Provider: "node", Experimental: true}},
	})

	result, err := server.callToolForTest(context.Background(), "list_code_debug_targets", `{}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "code debug targets listed")
	assert.Contains(t, result.Content[0]["text"], "experimental")
}

func TestOpenCodeDebugSessionToolForwardsApprovalToken(t *testing.T) {
	client := &fakeAgentClient{
		codeDebugSession: CodeDebugSession{ID: "cds_1", DeploymentID: "dep-api-dev", Provider: "go"},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "open_code_debug_session", `{"deployment_id":"dep-api-dev","approval_token":"tok_1"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "dep-api-dev", client.lastCodeDebugOpen.DeploymentID)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
	assert.Contains(t, result.Content[0]["text"], "code debug session opened")
}

func TestSetDebugBreakpointsToolForwardsRequest(t *testing.T) {
	client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "set_debug_breakpoints", `{"session_id":"cds_1","source":"main.go","lines":[12,15]}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "cds_1", client.lastBreakpoint.SessionID)
	assert.Equal(t, []int{12, 15}, client.lastBreakpoint.Lines)
}

func TestDebugContinueToolForwardsThreadAction(t *testing.T) {
	client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_continue", `{"session_id":"cds_1","thread_id":7}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "cds_1", client.lastDebugActionSessionID)
	assert.Equal(t, "continue", client.lastDebugAction)
	assert.Equal(t, 7, client.lastDebugActionBody["thread_id"])
}

func TestDebugEvaluateToolForwardsApprovalToken(t *testing.T) {
	client := &fakeAgentClient{
		codeDebugEvaluateResult: map[string]any{"result": map[string]any{"type": "string", "value": "ok"}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_evaluate", `{"session_id":"cds_1","expression":"user.id","frame_id":1,"approval_token":"tok_eval"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "tok_eval", client.lastApprovalToken)
	assert.Equal(t, "cds_1", client.lastEvaluate.SessionID)
	assert.Equal(t, "debug_evaluate", client.lastEvaluate.Source)
	assert.Equal(t, "user.id", client.lastEvaluate.Expression)
}

func TestCodeDebugEvaluateSchemaIncludesApprovalFields(t *testing.T) {
	schema := codeDebugEvaluateInputSchema()
	properties := schema["properties"].(map[string]any)

	assert.Contains(t, properties, "approval_token")
	assert.Contains(t, properties, "approval_wait_seconds")
	assert.Equal(t, []string{"session_id", "expression"}, schema["required"])
}
