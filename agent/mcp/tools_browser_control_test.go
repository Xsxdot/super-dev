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
			Elements: []BrowserSnapshotElement{
				{Role: "button", Name: "Save", Selector: `[data-test="save"]`, Visible: true, Enabled: true},
			},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "browser_snapshot", `{"session_id":" brs_1 ","selector":"#app","max_text":2000,"max_elements":25}`)

	require.NoError(t, err)
	require.False(t, result.IsError)
	assert.Equal(t, "brs_1", client.lastBrowserSnapshot.SessionID)
	assert.Equal(t, "#app", client.lastBrowserSnapshot.Selector)
	assert.Equal(t, 2000, client.lastBrowserSnapshot.MaxText)
	assert.Equal(t, 25, client.lastBrowserSnapshot.MaxElements)
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

func TestBrowserScreenshotToolReturnsStructuredAgentError(t *testing.T) {
	client := &fakeAgentClient{
		browserScreenshotErr: AgentError{
			Code:    "browser_screenshot_too_large",
			Message: "browser screenshot is too large",
			Data:    map[string]any{"limit_bytes": 1572864},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "browser_screenshot", `{"session_id":"brs_1","full_page":true}`)

	require.NoError(t, err)
	require.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "browser_screenshot_too_large", payload.Code)
	assert.Equal(t, map[string]any{"limit_bytes": 1572864}, payload.Data)
}

func TestBrowserNavigationToolsForwardRequests(t *testing.T) {
	client := &fakeAgentClient{
		browserNavigation: BrowserNavigationResult{SessionID: "brs_1", URL: "http://127.0.0.1:5173/users", Title: "Users"},
		browserAction:     BrowserActionResult{SessionID: "brs_1", OK: true},
	}
	server := NewServer(client)

	navigated, err := server.callToolForTest(context.Background(), "browser_navigate", `{"session_id":"brs_1","path":"/users","wait_until":"domcontentloaded"}`)
	require.NoError(t, err)
	require.False(t, navigated.IsError)
	reloaded, err := server.callToolForTest(context.Background(), "browser_reload", `{"session_id":"brs_1","wait_until":"load"}`)
	require.NoError(t, err)
	require.False(t, reloaded.IsError)

	assert.Equal(t, "/users", client.lastBrowserNavigate.Path)
	assert.Equal(t, "domcontentloaded", client.lastBrowserNavigate.WaitUntil)
	assert.Equal(t, "load", client.lastBrowserReload.WaitUntil)
}

func TestBrowserWaitPressSelectForwardRequests(t *testing.T) {
	client := &fakeAgentClient{
		browserWait:   BrowserWaitResult{SessionID: "brs_1", Matched: true, Text: "Ready"},
		browserAction: BrowserActionResult{SessionID: "brs_1", OK: true},
	}
	server := NewServer(client)

	waited, err := server.callToolForTest(context.Background(), "browser_wait_for_selector", `{"session_id":"brs_1","selector":"#ready","state":"visible","timeout_ms":1500}`)
	require.NoError(t, err)
	require.False(t, waited.IsError)
	pressed, err := server.callToolForTest(context.Background(), "browser_press_key", `{"session_id":"brs_1","selector":"input[name=q]","key":"Enter"}`)
	require.NoError(t, err)
	require.False(t, pressed.IsError)
	selected, err := server.callToolForTest(context.Background(), "browser_select_option", `{"session_id":"brs_1","selector":"select","value":"prod"}`)
	require.NoError(t, err)
	require.False(t, selected.IsError)

	assert.Equal(t, "#ready", client.lastBrowserWait.Selector)
	assert.Equal(t, "visible", client.lastBrowserWait.State)
	assert.Equal(t, 1500, client.lastBrowserWait.TimeoutMS)
	assert.Equal(t, "Enter", client.lastBrowserPress.Key)
	assert.Equal(t, "prod", client.lastBrowserSelect.Value)
}

func TestBrowserNavigationToolMetadataDocumentsSPARisk(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	navigate := server.tools["browser_navigate"].Tool
	wait := server.tools["browser_wait_for_selector"].Tool
	consoleLogs := server.tools["browser_console_logs"].Tool
	networkRequests := server.tools["browser_network_requests"].Tool

	assert.Contains(t, navigate.Description, "lose SPA in-memory state")
	assert.Equal(t, true, wait.Annotations["readOnlyHint"])
	assert.Equal(t, true, consoleLogs.Annotations["readOnlyHint"])
	assert.Equal(t, true, networkRequests.Annotations["readOnlyHint"])
}

func TestBrowserConsoleAndNetworkToolsForwardRequests(t *testing.T) {
	client := &fakeAgentClient{
		browserConsoleLogs:     BrowserConsoleLogsResult{SessionID: "brs_1", Logs: []BrowserConsoleLog{{Level: "error", Text: "boom"}}},
		browserNetworkRequests: BrowserNetworkRequestsResult{SessionID: "brs_1", Requests: []BrowserNetworkRequest{{URL: "http://127.0.0.1:5173/api", Method: "GET", Status: 200}}},
	}
	server := NewServer(client)

	logs, err := server.callToolForTest(context.Background(), "browser_console_logs", `{"session_id":"brs_1","level":"error","limit":20}`)
	require.NoError(t, err)
	require.False(t, logs.IsError)
	requests, err := server.callToolForTest(context.Background(), "browser_network_requests", `{"session_id":"brs_1","filter":"api","limit":10}`)
	require.NoError(t, err)
	require.False(t, requests.IsError)

	assert.Equal(t, "error", client.lastBrowserConsoleLogs.Level)
	assert.Equal(t, 20, client.lastBrowserConsoleLogs.Limit)
	assert.Equal(t, "api", client.lastBrowserNetworkRequests.Filter)
	assert.Equal(t, 10, client.lastBrowserNetworkRequests.Limit)
}
