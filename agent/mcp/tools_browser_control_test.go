// tools_browser_control_test.go 验证浏览器页面控制 MCP 工具。
//
// 职责：
//   - 覆盖 snapshot、click、type、screenshot、evaluate 的参数校验和 client 转发
//   - 确认工具输出可被 AI 以结构化方式消费
//
// 边界：
//   - 不连接真实 Playwright/CDP
//   - 不创建真实浏览器调试会话
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBrowserSnapshotTool(t *testing.T) {
	client := &fakeAgentClient{
		browserSnapshot: BrowserSnapshot{
			SessionID: "brs_1",
			URL:       "http://127.0.0.1:3000/",
			Title:     "Admin",
			Text:      "Ready",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "browser_snapshot", `{"session_id":" brs_1 ","selector":"#app","max_text":2000}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "brs_1", client.lastBrowserSnapshot.SessionID)
	assert.Equal(t, "#app", client.lastBrowserSnapshot.Selector)
	assert.Equal(t, 2000, client.lastBrowserSnapshot.MaxText)
	assert.Contains(t, result.Content[0]["text"], "browser snapshot captured")
}

func TestBrowserClickToolRequiresSelector(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "browser_click", `{"session_id":"brs_1"}`)

	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0]["text"], "selector is required")
}

func TestBrowserActionToolsForwardRequests(t *testing.T) {
	client := &fakeAgentClient{
		browserAction:   BrowserActionResult{SessionID: "brs_1", OK: true},
		browserEvaluate: BrowserEvaluateResult{SessionID: "brs_1", Result: map[string]any{"ok": true}},
	}
	server := NewServer(client)

	clicked, err := server.callToolForTest(context.Background(), "browser_click", `{"session_id":"brs_1","selector":"button.save"}`)
	require.NoError(t, err)
	require.False(t, clicked.IsError)
	typed, err := server.callToolForTest(context.Background(), "browser_type", `{"session_id":"brs_1","selector":"input[name=q]","text":"hello","fill":true}`)
	require.NoError(t, err)
	require.False(t, typed.IsError)
	evaluated, err := server.callToolForTest(context.Background(), "browser_evaluate", `{"session_id":"brs_1","expression":"() => ({ ok: true })"}`)
	require.NoError(t, err)
	require.False(t, evaluated.IsError)

	assert.Equal(t, "button.save", client.lastBrowserClick.Selector)
	assert.Equal(t, "input[name=q]", client.lastBrowserType.Selector)
	assert.Equal(t, "hello", client.lastBrowserType.Text)
	assert.True(t, client.lastBrowserType.Fill)
	assert.Equal(t, "() => ({ ok: true })", client.lastBrowserEvaluate.Expression)
}

func TestBrowserScreenshotTool(t *testing.T) {
	client := &fakeAgentClient{
		browserScreenshot: BrowserScreenshot{
			SessionID:  "brs_1",
			MimeType:   "image/png",
			DataBase64: "cG5n",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "browser_screenshot", `{"session_id":"brs_1","full_page":true}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.True(t, client.lastBrowserScreenshot.FullPage)
	assert.Contains(t, result.Content[0]["text"], "browser screenshot captured")
}
