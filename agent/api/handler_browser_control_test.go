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
	srv := httptest.NewServer(app.Handler())
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
	srv := httptest.NewServer(app.Handler())
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
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions/missing/click", map[string]any{
		"selector": "#submit",
	}, http.StatusNotFound)

	assert.Equal(t, "browser session not found", resp["error"])
}

func TestBrowserControlActionsForwardRequests(t *testing.T) {
	app, sessionID := newBrowserControlTestApp(t)
	control := &fakeBrowserControl{
		action:     browsercontrol.ActionResult{OK: true},
		screenshot: browsercontrol.Screenshot{MimeType: "image/png", DataBase64: "cG5n"},
		evaluate:   browsercontrol.EvaluateResult{Result: map[string]any{"ok": true}},
	}
	app.browserControl = control
	srv := httptest.NewServer(app.Handler())
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
	evaluated := postJSONForTest[browsercontrol.EvaluateResult](t, srv.URL+"/api/browser-sessions/"+sessionID+"/evaluate", map[string]any{
		"expression": "() => ({ ok: true })",
	}, http.StatusOK)

	assert.True(t, click.OK)
	assert.True(t, typed.OK)
	assert.Equal(t, "#submit", control.lastClick.Selector)
	assert.Equal(t, "Codex", control.lastType.Text)
	assert.True(t, control.lastType.Fill)
	assert.True(t, control.lastScreenshot.FullPage)
	assert.Equal(t, "image/png", screenshot.MimeType)
	assert.Equal(t, "() => ({ ok: true })", control.lastEvaluate.Expression)
	assert.Equal(t, map[string]any{"ok": true}, evaluated.Result)
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
	lastEvaluate   browsercontrol.EvaluateRequest
	snapshot       browsercontrol.Snapshot
	action         browsercontrol.ActionResult
	screenshot     browsercontrol.Screenshot
	evaluate       browsercontrol.EvaluateResult
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
	f.screenshot.SessionID = session.ID
	return f.screenshot, nil
}

func (f *fakeBrowserControl) Evaluate(_ context.Context, session browsercontrol.SessionRef, req browsercontrol.EvaluateRequest) (browsercontrol.EvaluateResult, error) {
	f.lastSession = session
	f.lastEvaluate = req
	f.evaluate.SessionID = session.ID
	return f.evaluate, nil
}
