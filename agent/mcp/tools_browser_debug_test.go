// tools_browser_debug_test.go 验证本机前端浏览器调试 MCP 工具。
//
// 职责：
//   - 覆盖浏览器/目标列表与 session 打开关闭工具
//   - 确认审批 token 与请求参数转发到 agent client
//
// 边界：
//   - 不启动真实浏览器
//   - 不访问真实 agent HTTP 服务
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListBrowserTargetsTool(t *testing.T) {
	server := NewServer(&fakeAgentClient{
		browserTargets: []BrowserTarget{{DeploymentID: "dep-admin-dev", BaseURL: "http://127.0.0.1:3000"}},
	})

	result, err := server.callToolForTest(context.Background(), "list_browser_targets", `{}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "browser targets listed")
}

func TestListDebugBrowsersTool(t *testing.T) {
	server := NewServer(&fakeAgentClient{
		debugBrowsers: []DebugBrowser{{ID: "arc", Name: "Arc", Available: true}},
	})

	result, err := server.callToolForTest(context.Background(), "list_debug_browsers", `{}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "debug browsers listed")
}

func TestOpenBrowserDebugSessionTool(t *testing.T) {
	client := &fakeAgentClient{
		browserSession: BrowserSession{
			ID:           "brs_1",
			DeploymentID: "dep-admin-dev",
			TargetURL:    "http://127.0.0.1:3000/",
			PageWS:       "ws://127.0.0.1:9222/devtools/page/page-1",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "open_browser_debug_session", `{"deployment_id":"dep-admin-dev","browser_id":"arc","approval_token":"tok_1"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "dep-admin-dev", client.lastBrowserOpen.DeploymentID)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
	assert.Contains(t, result.Content[0]["text"], "browser debug session opened")
}

func TestOpenBrowserDebugSessionToolForwardsViewport(t *testing.T) {
	client := &fakeAgentClient{
		browserSession: BrowserSession{ID: "brs_1", DeploymentID: "dep-admin-dev"},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "open_browser_debug_session", `{"deployment_id":"dep-admin-dev","viewport_width":1478,"viewport_height":1000,"approval_token":"tok_1"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, 1478, client.lastBrowserOpen.ViewportWidth)
	assert.Equal(t, 1000, client.lastBrowserOpen.ViewportHeight)
	assert.Contains(t, result.Content[0]["text"], "browser debug session opened")
}

func TestCloseBrowserDebugSessionTool(t *testing.T) {
	client := &fakeAgentClient{}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "close_browser_debug_session", `{"session_id":"brs_1"}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "brs_1", client.closedBrowserSession)
}

func TestCallToolForSmokeUsesRegisteredTools(t *testing.T) {
	server := NewServer(&fakeAgentClient{
		debugBrowsers: []DebugBrowser{{ID: "arc", Name: "Arc", Available: true}},
	})

	result, err := server.CallToolForSmoke(context.Background(), "list_debug_browsers", []byte(`{}`))

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "debug browsers listed")
}
