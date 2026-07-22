// tools_code_debug_zero_identity_test.go 验证 js-debug 的零值 DAP identity 能穿过 MCP schema。
//
// 职责：锁定 debug_continue 对合法 thread_id=0 的转发合同。
// 边界：不启动真实 adapter，不覆盖其他 code-debug 工具行为。
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDebugContinueAllowsJSDebugZeroThreadIdentity(t *testing.T) {
	client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "debug_continue", `{"deployment_id":"dep-api-dev","thread_id":0}`)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 0, client.lastDebugActionBody["thread_id"])
}

func TestDebugStackAndScopesAllowJSDebugZeroIdentity(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments string
		action    string
		bodyKey   string
	}{
		{name: "thread", tool: "debug_stack_trace", arguments: `{"deployment_id":"dep-api-dev","thread_id":0}`, action: "stack", bodyKey: "thread_id"},
		{name: "frame", tool: "debug_scopes", arguments: `{"deployment_id":"dep-api-dev","frame_id":0}`, action: "scopes", bodyKey: "frame_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeAgentClient{codeDebugActionResult: map[string]any{"ok": true}}
			server := NewServer(client)

			result, err := server.callToolForTest(context.Background(), test.tool, test.arguments)

			require.NoError(t, err)
			require.False(t, result.IsError)
			assert.Equal(t, test.action, client.lastDebugAction)
			assert.Equal(t, 0, client.lastDebugActionBody[test.bodyKey])
		})
	}
}
