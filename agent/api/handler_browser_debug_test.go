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
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/browserdebug"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func TestListBrowserTargetsReturnsLocalWebDeployment(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
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
	settings.DebugBrowser = config.DebugBrowserSettings{
		DefaultBrowserID: "arc",
		ProfileMode:      "ephemeral",
		Browsers: []config.DebugBrowserConfig{{
			ID:             "arc",
			Name:           "Arc",
			ExecutablePath: browserPath,
		}},
	}
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
	srv := httptest.NewServer(app.Handler())
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

func TestBrowserDebugPreflightBuildsOperationPlan(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(browserDebugAPIProject())
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
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
	settings.DebugBrowser = config.DebugBrowserSettings{
		DefaultBrowserID: "arc",
		ProfileMode:      "ephemeral",
		Browsers: []config.DebugBrowserConfig{{
			ID:             "arc",
			Name:           "Arc",
			ExecutablePath: browserPath,
		}},
	}
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
	srv := httptest.NewServer(app.Handler())
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
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/browser-sessions", map[string]any{
		"deployment_id": "dep-admin-dev",
	}, http.StatusBadRequest)

	assert.Equal(t, "debug browser is not configured", resp["error"])
	approvals := getJSONForTest[[]operation.Approval](t, srv.URL+"/api/operation-approvals", http.StatusOK)
	assert.Empty(t, approvals)
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
