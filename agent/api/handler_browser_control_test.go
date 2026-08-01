// Package api 验证本机浏览器控制 HTTP API。
//
// 职责：
//   - 验证 browser session 能被解析为控制目标
//   - 验证页面控制请求被转发给 browsercontrol.Controller
//
// 边界：
//   - 不启动真实 Playwright
//   - 不启动真实浏览器
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/browserdebug"
	"github.com/xsxdot/super-dev/agent/operation"
)

func TestBrowserControlSnapshotUsesSessionEndpoints(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	control := &fakeBrowserControl{
		snapshot: browsercontrol.Snapshot{
			URL:   "http://127.0.0.1:5173/",
			Title: "Admin",
			Text:  "Ready",
		},
	}
	app.browserControl = control
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	got := postJSONForTest[browsercontrol.Snapshot](t, srv.URL+"/api/browser-sessions/"+sessionID+"/snapshot", map[string]any{
		"selector": "#app",
		"max_text": 1000,
	}, http.StatusOK)

	assert.Equal(t, sessionID, control.lastSession.ID)
	assert.Equal(t, "ws://127.0.0.1:9222/devtools/browser/b", control.lastSession.BrowserWS)
	assert.Equal(t, "#app", control.lastSnapshot.Selector)
	assert.Equal(t, "Admin", got.Title)
}

func TestBrowserControlSnapshotAllowsEmptyBody(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	control := &fakeBrowserControl{
		snapshot: browsercontrol.Snapshot{Title: "Admin"},
	}
	app.browserControl = control
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp, err := http.Post(srv.URL+"/api/browser-sessions/"+sessionID+"/snapshot", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got browsercontrol.Snapshot
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	assert.Equal(t, sessionID, got.SessionID)
	assert.Empty(t, control.lastSnapshot.Selector)
	assert.Equal(t, "Admin", got.Title)
}

func TestBrowserControlClickRejectsMissingSession(t *testing.T) {
	app, _ := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions/missing/click", map[string]any{
		"selector": "#submit",
	}, http.StatusNotFound)

	assert.Equal(t, "browser session not found", resp["error"])
}

func TestBrowserControlActionsForwardRequests(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	settings, err := app.settings.Load()
	require.NoError(t, err)
	settings.DebugBrowser.AllowEvaluate = true
	require.NoError(t, app.settings.Save(settings))
	control := &fakeBrowserControl{
		action:     browsercontrol.ActionResult{OK: true},
		screenshot: browsercontrol.Screenshot{MimeType: "image/png", DataBase64: "cG5n"},
		navigation: browsercontrol.NavigationResult{URL: "http://127.0.0.1:5173/users", Title: "Users"},
		wait:       browsercontrol.WaitForSelectorResult{Matched: true, Text: "Ready"},
		consoleLogs: browsercontrol.ConsoleLogsResult{
			Logs: []browsercontrol.ConsoleLog{{Level: "error", Text: "boom"}},
		},
		network: browsercontrol.NetworkRequestsResult{
			Requests: []browsercontrol.NetworkRequest{{Method: "GET", URL: "http://127.0.0.1:5173/api", Status: 200}},
		},
		evaluate: browsercontrol.EvaluateResult{Result: map[string]any{"ok": true}},
	}
	app.browserControl = control
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	click := postJSONForTest[browsercontrol.ActionResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/click", map[string]any{
		"selector": "#submit",
	}, http.StatusOK)
	typed := postJSONForTest[browsercontrol.ActionResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/type", map[string]any{
		"selector": "#name",
		"text":     "Codex",
		"fill":     true,
	}, http.StatusOK)
	screenshot := postJSONForTest[browsercontrol.Screenshot](t, srv.URL+"/api/browser-sessions/"+sessionID+"/screenshot", map[string]any{
		"full_page": true,
	}, http.StatusOK)
	navigated := postJSONForTest[browsercontrol.NavigationResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/navigate", map[string]any{
		"path":       "/users",
		"wait_until": "domcontentloaded",
	}, http.StatusOK)
	reloaded := postJSONForTest[browsercontrol.NavigationResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/reload", map[string]any{
		"wait_until": "load",
	}, http.StatusOK)
	waited := postJSONForTest[browsercontrol.WaitForSelectorResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/wait-for-selector", map[string]any{
		"selector":   "#ready",
		"state":      "visible",
		"timeout_ms": 1500,
	}, http.StatusOK)
	pressed := postJSONForTest[browsercontrol.ActionResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/press-key", map[string]any{
		"selector": "input[name=q]",
		"key":      "Enter",
	}, http.StatusOK)
	selected := postJSONForTest[browsercontrol.ActionResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/select-option", map[string]any{
		"selector": "select",
		"value":    "prod",
	}, http.StatusOK)
	logs := postJSONForTest[browsercontrol.ConsoleLogsResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/console-logs", map[string]any{
		"level": "error",
		"limit": 20,
	}, http.StatusOK)
	requests := postJSONForTest[browsercontrol.NetworkRequestsResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/network-requests", map[string]any{
		"filter": "api",
		"limit":  10,
	}, http.StatusOK)
	evaluated := postJSONForTest[browsercontrol.EvaluateResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": "() => ({ ok: true })",
	}, http.StatusOK)
	viewport := postJSONForTest[browsercontrol.ViewportResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/set-viewport", map[string]any{
		"width":  1478,
		"height": 1000,
	}, http.StatusOK)

	assert.True(t, click.OK)
	assert.True(t, typed.OK)
	assert.Equal(t, "#submit", control.lastClick.Selector)
	assert.Equal(t, "Codex", control.lastType.Text)
	assert.True(t, control.lastType.Fill)
	assert.True(t, control.lastScreenshot.FullPage)
	assert.Equal(t, "image/png", screenshot.MimeType)
	assert.Equal(t, "/users", control.lastNavigate.Path)
	assert.Equal(t, "domcontentloaded", control.lastNavigate.WaitUntil)
	assert.Equal(t, "load", control.lastReload.WaitUntil)
	assert.Equal(t, "#ready", control.lastWait.Selector)
	assert.Equal(t, "visible", control.lastWait.State)
	assert.Equal(t, 1500, control.lastWait.TimeoutMS)
	assert.Equal(t, "Enter", control.lastPress.Key)
	assert.Equal(t, "prod", control.lastSelect.Value)
	assert.Equal(t, "error", control.lastConsole.Level)
	assert.Equal(t, 20, control.lastConsole.Limit)
	assert.Equal(t, "api", control.lastNetwork.Filter)
	assert.Equal(t, 10, control.lastNetwork.Limit)
	assert.Equal(t, "Users", navigated.Title)
	assert.Equal(t, "Users", reloaded.Title)
	assert.True(t, waited.Matched)
	assert.True(t, pressed.OK)
	assert.True(t, selected.OK)
	require.Len(t, logs.Logs, 1)
	require.Len(t, requests.Requests, 1)
	assert.Equal(t, "() => ({ ok: true })", control.lastEvaluate.Expression)
	assert.Equal(t, map[string]any{"ok": true}, evaluated.Result)
	assert.Equal(t, 1478, control.lastViewport.Width)
	assert.Equal(t, 1000, control.lastViewport.Height)
	assert.Equal(t, 1478, viewport.Width)
	assert.Equal(t, 1000, viewport.Height)
}

func TestBrowserEvaluateDisabledByDefault(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": "() => document.title",
	}, http.StatusForbidden)

	assert.Equal(t, "browser_evaluate_disabled", resp["code"])
	assert.Equal(t, "browser_evaluate is disabled in settings", resp["error"])
}

func TestBrowserEvaluateAllowedWhenSettingEnabled(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	settings, err := app.settings.Load()
	require.NoError(t, err)
	settings.DebugBrowser.AllowEvaluate = true
	require.NoError(t, app.settings.Save(settings))
	control := &fakeBrowserControl{evaluate: browsercontrol.EvaluateResult{Result: "Admin"}}
	app.browserControl = control
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	got := postJSONForTest[browsercontrol.EvaluateResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": "() => document.title",
	}, http.StatusOK)

	assert.Equal(t, "() => document.title", control.lastEvaluate.Expression)
	assert.Equal(t, "Admin", got.Result)
}

func TestBrowserControlTypedErrorResponseIncludesCodeAndData(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{
		screenshotErr: browsercontrol.NewControlError(browsercontrol.CodeScreenshotTooLarge, "browser screenshot is too large", map[string]any{
			"limit_bytes":  1572864,
			"actual_bytes": 2097152,
			"full_page":    true,
		}),
	}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions/"+sessionID+"/screenshot", map[string]any{
		"full_page": true,
	}, http.StatusRequestTimeout)

	assert.Equal(t, "browser_screenshot_too_large", resp["code"])
	assert.Equal(t, "browser screenshot is too large", resp["error"])
	data := resp["data"].(map[string]any)
	assert.Equal(t, float64(1572864), data["limit_bytes"])
	assert.Equal(t, float64(2097152), data["actual_bytes"])
	assert.Equal(t, true, data["full_page"])
}

func TestBrowserEvaluateAuditDoesNotLeakExpressionOrResult(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	settings, err := app.settings.Load()
	require.NoError(t, err)
	settings.DebugBrowser.AllowEvaluate = true
	require.NoError(t, app.settings.Save(settings))
	app.browserControl = &fakeBrowserControl{evaluate: browsercontrol.EvaluateResult{Result: "super-secret-token-value"}}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	const expression = "() => localStorage.getItem('auth_token')"
	postJSONForTest[browsercontrol.EvaluateResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": expression,
	}, http.StatusOK)

	events := browserControlAuditEvents(t, app)
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, operation.OperationBrowserDebugControl, event.Kind)
	assert.Equal(t, operation.AuditExecuted, event.Action)
	assert.Equal(t, "browser_debug.evaluate", event.Summary)
	assert.Equal(t, "dep-web", event.Plan.Target.DeploymentID)
	assert.Equal(t, sessionID, event.Data["session_id"])
	// 审计必须只保留 hash/长度/结果类型，绝不含表达式明文或结果值。
	assert.Equal(t, hashExpression(expression), event.Data["expression_sha256"])
	assert.EqualValues(t, len(expression), event.Data["expression_length"])
	assert.Equal(t, "string", event.Data["result_type"])
	raw := mustMarshalString(t, event)
	assert.NotContains(t, raw, "auth_token")
	assert.NotContains(t, raw, "localStorage")
	assert.NotContains(t, raw, "super-secret-token-value")
}

func TestBrowserEvaluateDisabledAttemptIsAudited(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	postJSONForRawTest(t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": "() => document.cookie",
	}, http.StatusForbidden)

	events := browserControlAuditEvents(t, app)
	require.Len(t, events, 1)
	assert.Equal(t, operation.AuditFailed, events[0].Action)
	assert.Equal(t, browsercontrol.CodeEvaluateDisabled, events[0].Data["error_code"])
	assert.NotContains(t, mustMarshalString(t, events[0]), "document.cookie")
}

func TestBrowserTypeAuditOmitsTypedText(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	postJSONForTest[browsercontrol.ActionResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/type", map[string]any{
		"selector": "#password",
		"text":     "hunter2",
		"fill":     true,
	}, http.StatusOK)

	events := browserControlAuditEvents(t, app)
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, "browser_debug.type", event.Summary)
	assert.Equal(t, "#password", event.Data["selector"])
	assert.EqualValues(t, len("hunter2"), event.Data["text_length"])
	assert.NotContains(t, mustMarshalString(t, event), "hunter2")
}

func TestBrowserSetViewportAuditRecordsDimensions(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	app.browserControl = &fakeBrowserControl{viewport: browsercontrol.ViewportResult{Width: 1478, Height: 1000}}
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	postJSONForTest[browsercontrol.ViewportResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/set-viewport", map[string]any{
		"width":  1478,
		"height": 1000,
	}, http.StatusOK)

	events := browserControlAuditEvents(t, app)
	require.Len(t, events, 1)
	event := events[0]
	assert.Equal(t, "browser_debug.set_viewport", event.Summary)
	assert.EqualValues(t, 1478, event.Data["width"])
	assert.EqualValues(t, 1000, event.Data["height"])
}

func browserControlAuditEvents(t *testing.T, app *App) []operation.AuditEvent {
	t.Helper()
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.OperationBrowserDebugControl})
	require.NoError(t, err)
	return events
}

func mustMarshalString(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func newBrowserControlTestApp(t *testing.T) (*App, string) {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(context.Context, browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			return browserdebug.LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/b",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/p",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/p",
				Close:       func() error { return nil },
			}, nil
		},
	})
	session, err := app.browserDebug.Open(context.Background(), browserdebug.OpenResolvedRequest{
		Browser:   browserdebug.BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/bin/arc", Available: true},
		Target:    browserdebug.Target{DeploymentID: "dep-web"},
		TargetURL: "http://127.0.0.1:5173/",
	})
	require.NoError(t, err)
	return app, session.ID
}

type fakeBrowserControl struct {
	lastSession    browsercontrol.SessionRef
	lastSnapshot   browsercontrol.SnapshotRequest
	lastClick      browsercontrol.ClickRequest
	lastType       browsercontrol.TypeRequest
	lastScreenshot browsercontrol.ScreenshotRequest
	lastNavigate   browsercontrol.NavigateRequest
	lastReload     browsercontrol.ReloadRequest
	lastWait       browsercontrol.WaitForSelectorRequest
	lastPress      browsercontrol.PressKeyRequest
	lastSelect     browsercontrol.SelectOptionRequest
	lastConsole    browsercontrol.ConsoleLogsRequest
	lastNetwork    browsercontrol.NetworkRequestsRequest
	lastEvaluate   browsercontrol.EvaluateRequest
	lastViewport   browsercontrol.ViewportRequest
	snapshot       browsercontrol.Snapshot
	action         browsercontrol.ActionResult
	screenshot     browsercontrol.Screenshot
	navigation     browsercontrol.NavigationResult
	wait           browsercontrol.WaitForSelectorResult
	consoleLogs    browsercontrol.ConsoleLogsResult
	network        browsercontrol.NetworkRequestsResult
	evaluate       browsercontrol.EvaluateResult
	viewport       browsercontrol.ViewportResult
	screenshotErr  error
}

func (f *fakeBrowserControl) Snapshot(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.SnapshotRequest) (browsercontrol.Snapshot, error) {
	f.lastSession = session
	f.lastSnapshot = req
	f.snapshot.SessionID = session.ID
	return f.snapshot, nil
}

func (f *fakeBrowserControl) Click(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.ClickRequest) (browsercontrol.ActionResult, error) {
	f.lastSession = session
	f.lastClick = req
	f.action.SessionID = session.ID
	return f.action, nil
}

func (f *fakeBrowserControl) Type(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.TypeRequest) (browsercontrol.ActionResult, error) {
	f.lastSession = session
	f.lastType = req
	f.action.SessionID = session.ID
	return f.action, nil
}

func (f *fakeBrowserControl) Screenshot(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.ScreenshotRequest) (browsercontrol.Screenshot, error) {
	f.lastSession = session
	f.lastScreenshot = req
	if f.screenshotErr != nil {
		return browsercontrol.Screenshot{}, f.screenshotErr
	}
	f.screenshot.SessionID = session.ID
	return f.screenshot, nil
}

func (f *fakeBrowserControl) Navigate(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.NavigateRequest) (browsercontrol.NavigationResult, error) {
	f.lastSession = session
	f.lastNavigate = req
	f.navigation.SessionID = session.ID
	return f.navigation, nil
}

func (f *fakeBrowserControl) Reload(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.ReloadRequest) (browsercontrol.NavigationResult, error) {
	f.lastSession = session
	f.lastReload = req
	f.navigation.SessionID = session.ID
	return f.navigation, nil
}

func (f *fakeBrowserControl) WaitForSelector(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.WaitForSelectorRequest) (browsercontrol.WaitForSelectorResult, error) {
	f.lastSession = session
	f.lastWait = req
	f.wait.SessionID = session.ID
	return f.wait, nil
}

func (f *fakeBrowserControl) PressKey(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.PressKeyRequest) (browsercontrol.ActionResult, error) {
	f.lastSession = session
	f.lastPress = req
	f.action.SessionID = session.ID
	return f.action, nil
}

func (f *fakeBrowserControl) SelectOption(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.SelectOptionRequest) (browsercontrol.ActionResult, error) {
	f.lastSession = session
	f.lastSelect = req
	f.action.SessionID = session.ID
	return f.action, nil
}

func (f *fakeBrowserControl) ConsoleLogs(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.ConsoleLogsRequest) (browsercontrol.ConsoleLogsResult, error) {
	f.lastSession = session
	f.lastConsole = req
	f.consoleLogs.SessionID = session.ID
	return f.consoleLogs, nil
}

func (f *fakeBrowserControl) NetworkRequests(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.NetworkRequestsRequest) (browsercontrol.NetworkRequestsResult, error) {
	f.lastSession = session
	f.lastNetwork = req
	f.network.SessionID = session.ID
	return f.network, nil
}

func (f *fakeBrowserControl) Evaluate(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.EvaluateRequest) (browsercontrol.EvaluateResult, error) {
	f.lastSession = session
	f.lastEvaluate = req
	f.evaluate.SessionID = session.ID
	return f.evaluate, nil
}

func (f *fakeBrowserControl) SetViewport(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.ViewportRequest) (browsercontrol.ViewportResult, error) {
	f.lastSession = session
	f.lastViewport = req
	f.viewport.SessionID = session.ID
	if f.viewport.Width == 0 {
		f.viewport.Width = req.Width
	}
	if f.viewport.Height == 0 {
		f.viewport.Height = req.Height
	}
	return f.viewport, nil
}
