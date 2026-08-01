// Package api 验证本机前端浏览器调试 HTTP API。
//
// 职责：
//   - 验证本机 Web entrypoint 能被列为浏览器调试目标
//   - 验证打开调试浏览器会接入 operation 审批链路
//
// 边界：
//   - 不启动真实浏览器进程
//   - 不访问外部网络
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/browserdebug"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func TestDetectDebugBrowsersReturnsConfiguredCandidates(t *testing.T) {
	browserPath := filepath.Join(t.TempDir(), "Chrome")
	require.NoError(t, os.WriteFile(browserPath, []byte("#!/bin/sh\n"), 0o755))
	app, err := NewApp(AppConfig{
		DataDir: t.TempDir(),
		DebugBrowserCandidates: []browserdebug.BrowserCandidate{{
			ID:             "chrome",
			Name:           "Google Chrome",
			ExecutablePath: browserPath,
		}},
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	browsers := getJSONForTest[[]browserdebug.BrowserRecord](t, srv.URL+"/api/debug-browsers/detected", http.StatusOK)

	require.Len(t, browsers, 1)
	assert.Equal(t, "chrome", browsers[0].ID)
	assert.True(t, browsers[0].Available)
}

func TestFreshProfileRequiresDetectedBrowsersPersistedBeforeDefaultOpen(t *testing.T) {
	readiness := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(readiness.Close)
	browserDirectory := t.TempDir()
	chromePath := filepath.Join(browserDirectory, "chrome.exe")
	edgePath := filepath.Join(browserDirectory, "msedge.exe")
	require.NoError(t, os.WriteFile(chromePath, []byte("browser"), 0o755))
	require.NoError(t, os.WriteFile(edgePath, []byte("browser"), 0o755))

	app, err := NewApp(AppConfig{
		DataDir: t.TempDir(),
		DebugBrowserCandidates: []browserdebug.BrowserCandidate{
			{ID: "chrome", Name: "Google Chrome", ExecutablePath: chromePath},
			{ID: "edge", Name: "Microsoft Edge", ExecutablePath: edgePath},
		},
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := browserDebugAPIProject()
	project.Services[0].Deployments[0].Web.URL = readiness.URL
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()

	launchedBrowserID := ""
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			launchedBrowserID = req.Browser.ID
			return browserLaunchResultForTest(), nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	configured := getJSONForTest[[]browserdebug.BrowserRecord](t, srv.URL+"/api/debug-browsers", http.StatusOK)
	assert.Empty(t, configured, "fresh lane must not treat detected browsers as persisted product configuration")
	freshOpen := postJSONForRawTest(t, srv.URL+"/api/browser-sessions", map[string]any{"deployment_id": "dep-admin-dev"}, http.StatusBadRequest)
	assert.Equal(t, "debug browser is not configured", freshOpen["error"])

	detected := getJSONForTest[[]browserdebug.BrowserRecord](t, srv.URL+"/api/debug-browsers/detected", http.StatusOK)
	require.Len(t, detected, 2)
	settingsPayload, err := json.Marshal(map[string]any{
		"debug_browser": map[string]any{
			"default_browser_id": "edge",
			"browsers": []map[string]any{
				{"id": detected[0].ID, "name": detected[0].Name, "executable_path": detected[0].ExecutablePath},
				{"id": detected[1].ID, "name": detected[1].Name, "executable_path": detected[1].ExecutablePath},
			},
		},
	})
	require.NoError(t, err)
	settingsRequest, err := http.NewRequest(http.MethodPut, srv.URL+"/api/settings", bytes.NewReader(settingsPayload))
	require.NoError(t, err)
	settingsRequest.Header.Set("Content-Type", "application/json")
	settingsResponse, err := http.DefaultClient.Do(settingsRequest)
	require.NoError(t, err)
	defer settingsResponse.Body.Close()
	require.Equal(t, http.StatusOK, settingsResponse.StatusCode)
	var saved config.AgentSettings
	require.NoError(t, json.NewDecoder(settingsResponse.Body).Decode(&saved))
	assert.Equal(t, "edge", saved.DebugBrowser.DefaultBrowserID)

	configured = getJSONForTest[[]browserdebug.BrowserRecord](t, srv.URL+"/api/debug-browsers", http.StatusOK)
	require.Len(t, configured, 2)
	assert.True(t, configured[0].Available)
	assert.True(t, configured[1].Available)

	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", nil)
	session := postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusOK)
	assert.Equal(t, "edge", launchedBrowserID)
	assert.Equal(t, "edge", session.BrowserID)
}

func TestListBrowserTargetsReturnsLocalWebDeployment(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	targets := getJSONForTest[[]browserdebug.Target](t, srv.URL+"/api/browser-targets", http.StatusOK)

	require.Len(t, targets, 1)
	assert.Equal(t, "dep-admin-dev", targets[0].DeploymentID)
	assert.Equal(t, "http://127.0.0.1:3000", targets[0].BaseURL)
}

func TestOpenBrowserSessionRequiresApprovalThenLaunches(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()

	browserPath := filepath.Join(t.TempDir(), "Arc")
	require.NoError(t, os.WriteFile(browserPath, []byte("#!/bin/sh\n"), 0o755))
	settings := config.DefaultAgentSettings()
	settings.DebugBrowser.DefaultBrowserID = "arc"
	settings.DebugBrowser.Browsers = []config.DebugBrowserConfig{{
		ID:             "arc",
		Name:           "Arc",
		ExecutablePath: browserPath,
	}}
	require.NoError(t, app.settings.Save(settings))

	launches := 0
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			launches++
			assert.Equal(t, "http://127.0.0.1:3000/users", req.TargetURL)
			assert.True(t, req.OpenDevtools)
			return browserdebug.LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Close:       func() error { return nil },
			}, nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"path":          "/users",
	}, http.StatusForbidden)
	assert.Equal(t, "approval_required", required["code"])
	plan := required["plan"].(map[string]any)
	assert.Equal(t, operation.OperationBrowserDebugOpen, plan["kind"])
	approvalID := required["approval"].(map[string]any)["id"].(string)

	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "debug local frontend",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	session := postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"path":          "/users",
	}, map[string]string{"X-SuperDev-Approval-Token": detail.ApprovalToken}, http.StatusOK)

	assert.Equal(t, "dep-admin-dev", session.DeploymentID)
	assert.Equal(t, "arc", session.BrowserID)
	assert.Equal(t, "ws://127.0.0.1:9222/devtools/page/page-1", session.PageWS)
	assert.Equal(t, 1, launches)
}

func TestOpenBrowserSessionReusesExistingBrowserAndNavigatesWithoutNewApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	saveDebugBrowserSettingsForTest(t, app)
	control := &fakeBrowserControl{
		navigation: browsercontrol.NavigationResult{URL: "http://127.0.0.1:3000/settings", Title: "Settings"},
	}
	app.browserControl = control

	launches := 0
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			launches++
			assert.Equal(t, "http://127.0.0.1:3000/users", req.TargetURL)
			return browserLaunchResultForTest(), nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", map[string]any{"path": "/users"})

	first := postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"path":          "/users",
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusOK)

	second := postJSONForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"path":          "/settings",
	}, http.StatusOK)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, "http://127.0.0.1:3000/settings", second.TargetURL)
	assert.Equal(t, "http://127.0.0.1:3000/settings", control.lastNavigate.URL)
	assert.Equal(t, 1, launches)
	approvals := getJSONForTest[[]operation.Approval](t, srv.URL+"/api/operation-approvals", http.StatusOK)
	require.Len(t, approvals, 1)
}

func TestOpenBrowserSessionAppliesRequestedViewport(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	saveDebugBrowserSettingsForTest(t, app)
	control := &fakeBrowserControl{
		viewport: browsercontrol.ViewportResult{Width: 1478, Height: 1000},
	}
	app.browserControl = control
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			assert.Equal(t, 1478, req.ViewportWidth)
			assert.Equal(t, 1000, req.ViewportHeight)
			return browserLaunchResultForTest(), nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", map[string]any{
		"viewport_width":  1478,
		"viewport_height": 1000,
	})

	session := postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id":   "dep-admin-dev",
		"viewport_width":  1478,
		"viewport_height": 1000,
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusOK)

	assert.Equal(t, session.ID, control.lastSession.ID)
	assert.Equal(t, 1478, control.lastViewport.Width)
	assert.Equal(t, 1000, control.lastViewport.Height)
}

func TestOpenBrowserSessionWaitsForReadinessBeforeLaunch(t *testing.T) {
	readyAttempts := 0
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readyAttempts++
		if readyAttempts < 2 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready"))
	}))
	t.Cleanup(web.Close)

	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := browserDebugAPIProject()
	project.Services[0].Deployments[0].Web.URL = web.URL
	project.Services[0].Deployments[0].Web.Readiness = model.WebReadinessConfig{Type: model.WebReadinessHTTP, TimeoutSeconds: 2}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	saveDebugBrowserSettingsForTest(t, app)

	launches := 0
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			launches++
			assert.Equal(t, web.URL+"/", req.TargetURL)
			return browserLaunchResultForTest(), nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", nil)

	_ = postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusOK)

	assert.Equal(t, 1, launches)
	assert.GreaterOrEqual(t, readyAttempts, 2)
}

func TestOpenBrowserSessionReturnsUnavailableWhenReadinessTimesOut(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	t.Cleanup(web.Close)

	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := browserDebugAPIProject()
	project.Services[0].Deployments[0].Web.URL = web.URL
	project.Services[0].Deployments[0].Web.Readiness = model.WebReadinessConfig{Type: model.WebReadinessHTTP, TimeoutSeconds: 1}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	saveDebugBrowserSettingsForTest(t, app)
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(context.Context, browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			t.Fatal("launcher should not run before readiness")
			return browserdebug.LaunchResult{}, nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", nil)

	resp := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusServiceUnavailable)

	assert.Equal(t, "web_entrypoint_not_ready", resp["code"])
	assert.Contains(t, resp["error"], "http status 503")
}

func TestOpenBrowserSessionClassifiesCDPLaunchFailure(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	saveDebugBrowserSettingsForTest(t, app)
	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(context.Context, browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			return browserdebug.LaunchResult{}, errors.New("devtools active port not produced")
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	token := approveBrowserOpenForTest(t, srv.URL, "dep-admin-dev", nil)

	resp := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, map[string]string{"X-SuperDev-Approval-Token": token}, http.StatusInternalServerError)

	assert.Equal(t, "browser_cdp_connection_failed", resp["code"])
	assert.Contains(t, resp["error"], "devtools active port not produced")
}

func TestBrowserDebugPreflightBuildsOperationPlan(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	plan := postJSONForTest[operation.Plan](t, srv.URL+"/api/operations/preflight", map[string]any{
		"kind":          operation.OperationBrowserDebugOpen,
		"deployment_id": "dep-admin-dev",
		"path":          "/users",
	}, http.StatusOK)

	assert.Equal(t, operation.OperationBrowserDebugOpen, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, "dep-admin-dev", plan.Target.DeploymentID)
	assert.Contains(t, plan.ExpectedEffects[0], "http://127.0.0.1:3000/users")
}

func TestOpenBrowserSessionHonorsExplicitOpenDevtoolsFalse(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()

	browserPath := filepath.Join(t.TempDir(), "Arc")
	require.NoError(t, os.WriteFile(browserPath, []byte("#!/bin/sh\n"), 0o755))
	settings := config.DefaultAgentSettings()
	settings.DebugBrowser.DefaultBrowserID = "arc"
	settings.DebugBrowser.Browsers = []config.DebugBrowserConfig{{
		ID:             "arc",
		Name:           "Arc",
		ExecutablePath: browserPath,
	}}
	require.NoError(t, app.settings.Save(settings))

	app.browserDebug = browserdebug.NewManager(browserdebug.ManagerOptions{
		Launch: func(_ context.Context, req browserdebug.LaunchRequest) (browserdebug.LaunchResult, error) {
			assert.False(t, req.OpenDevtools)
			return browserdebug.LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Close:       func() error { return nil },
			}, nil
		},
	})
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"open_devtools": false,
	}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)

	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	_ = postJSONWithHeadersForTest[browserdebug.Session](t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
		"open_devtools": false,
	}, map[string]string{"X-SuperDev-Approval-Token": detail.ApprovalToken}, http.StatusOK)
}

func TestOpenBrowserSessionRejectsMissingBrowserBeforeApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, http.StatusBadRequest)

	assert.Equal(t, "debug browser is not configured", resp["error"])
	approvals := getJSONForTest[[]operation.Approval](t, srv.URL+"/api/operation-approvals", http.StatusOK)
	assert.Empty(t, approvals)
}

func saveDebugBrowserSettingsForTest(t *testing.T, app *App) {
	t.Helper()
	browserPath := filepath.Join(t.TempDir(), "Arc")
	require.NoError(t, os.WriteFile(browserPath, []byte("#!/bin/sh\n"), 0o755))
	settings := config.DefaultAgentSettings()
	settings.DebugBrowser.DefaultBrowserID = "arc"
	settings.DebugBrowser.Browsers = []config.DebugBrowserConfig{{
		ID:             "arc",
		Name:           "Arc",
		ExecutablePath: browserPath,
	}}
	require.NoError(t, app.settings.Save(settings))
}

func browserLaunchResultForTest() browserdebug.LaunchResult {
	return browserdebug.LaunchResult{
		ProcessID:   123,
		DebugPort:   9222,
		BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
		PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
		DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
		Close:       func() error { return nil },
	}
}

func approveBrowserOpenForTest(t *testing.T, baseURL string, deploymentID string, body map[string]any) string {
	t.Helper()
	if body == nil {
		body = map[string]any{}
	}
	body["deployment_id"] = deploymentID
	required := postJSONForRawTest(t, baseURL+"/api/browser-sessions", body, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operation.Approval](t, baseURL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, baseURL+"/api/operation-approvals/"+approvalID, http.StatusOK)
	return detail.ApprovalToken
}

func browserDebugAPIProject() model.Project {
	return model.Project{
		ID:   "p1",
		Name: "demo",
		Services: []model.Service{{
			ID:   "svc-admin",
			Name: "admin",
			Deployments: []model.Deployment{{
				ID:       "dep-admin-dev",
				EnvName:  "dev",
				Location: model.LocationLocal,
				Web: &model.WebEntrypointConfig{
					Enabled:     true,
					URL:         "http://127.0.0.1:3000",
					DefaultPath: "/",
					AIDebug:     model.WebAIDebugConfig{Enabled: true},
				},
			}},
		}},
	}
}
