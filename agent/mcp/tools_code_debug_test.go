// tools_code_debug_test.go 验证本机代码调试低层 MCP 工具。
//
// 职责：
//   - 覆盖代码调试目标、断点、执行控制、调用栈和 evaluate 工具
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

func TestListCodeDebugTargetsDescriptionMarksNodeAndJVMExperimental(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	tool, ok := server.tools["list_code_debug_targets"]
	require.True(t, ok)

	assert.Contains(t, tool.Tool.Description, "Node")
	assert.Contains(t, tool.Tool.Description, "JVM")
	assert.Contains(t, tool.Tool.Description, "experimental")
}

func TestSetDebugBreakpointsToolForwardsRequest(t *testing.T) {
	client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "set_debug_breakpoints", `{"deployment_id":"dep-api-dev","source":"main.go","lines":[12,15]}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "dep-api-dev", client.lastBreakpoint.DeploymentID)
	assert.Equal(t, []int{12, 15}, client.lastBreakpoint.Lines)
}

func TestSetDebugBreakpointsRequiresDeploymentID(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "set_debug_breakpoints", `{"source":"main.go","lines":[12]}`)

	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "deployment_id is required")
}

func TestDebugContinueToolForwardsThreadAction(t *testing.T) {
	client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_continue", `{"deployment_id":"dep-api-dev","thread_id":7}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "dep-api-dev", client.lastDebugActionDeploymentID)
	assert.Equal(t, "continue", client.lastDebugAction)
	assert.Equal(t, 7, client.lastDebugActionBody["thread_id"])
}

func TestDebugEvaluateToolForwardsApprovalToken(t *testing.T) {
	client := &fakeAgentClient{
		codeDebugEvaluateResult: map[string]any{"result": map[string]any{"type": "string", "value": "ok"}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_evaluate", `{"deployment_id":"dep-api-dev","expression":"user.id","frame_id":1,"approval_token":"tok_eval"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "tok_eval", client.lastApprovalToken)
	assert.Equal(t, "dep-api-dev", client.lastEvaluate.DeploymentID)
	assert.Equal(t, "debug_evaluate", client.lastEvaluate.Source)
	assert.Equal(t, "user.id", client.lastEvaluate.Expression)
}

func TestCodeDebugEvaluateSchemaIncludesApprovalFields(t *testing.T) {
	schema := codeDebugEvaluateInputSchema()
	properties := schema["properties"].(map[string]any)

	assert.Contains(t, properties, "approval_token")
	assert.Contains(t, properties, "approval_wait_seconds")
	assert.Equal(t, []string{"deployment_id", "expression"}, schema["required"])
}
