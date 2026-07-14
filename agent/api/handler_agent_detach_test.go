// handler_agent_detach_test.go 验证 Agent Detach 与删除旁路保护的 HTTP 契约。
//
// 职责：
//   - 证明显式 Detach 只移除 Controller Agent 配置并保留 Host
//   - 证明旧 Agent DELETE 与仍被 Agent 引用的 Host DELETE 不会改变状态
//   - 证明卸载或 Detach 后 Host 可以被用户单独删除
//   - 证明未知网页 Origin 不能触发卸载或 Detach
//
// 边界：
//   - 不执行真实 SSH 卸载
//   - 不验证 Desktop 的风险确认交互
package api

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDetachAgentRemovesOnlyControllerConfigAndPreservesHost 验证受信 Desktop Origin 的 Detach 只删除 Controller 配置。
func TestDetachAgentRemovesOnlyControllerConfigAndPreservesHost(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	req.Header.Set("Origin", "tauri://localhost")
	resp := httptest.NewRecorder()
	app.Handler().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "tauri://localhost", resp.Header().Get("Access-Control-Allow-Origin"))
	assert.Contains(t, resp.Body.String(), `"status":"detached"`)
	assert.Contains(t, resp.Body.String(), `"host_id":"`+hostID+`"`)
	assert.Equal(t, 0, fake.uninstallCalls, "Detach 不得声称或尝试远端卸载")
	_, agentFound, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, agentFound)
	_, hostFound, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	assert.True(t, hostFound)
}

// TestDetachAgentRetainsConfigWhenControllerRemovalFails 验证 Controller 配置写失败时保持可重试状态。
func TestDetachAgentRetainsConfigWhenControllerRemovalFails(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: &fakeAgentInstaller{}})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)
	app.removeAgentConfig = func(string) error { return errors.New("fixture agents.json write failure") }

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))

	require.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Contains(t, resp.Body.String(), "fixture agents.json write failure")
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.True(t, found)
}

// TestLegacyAgentDeleteRequiresDecommissionWithoutMutation 验证旧 DELETE 不再成为仅删配置旁路。
func TestLegacyAgentDeleteRequiresDecommissionWithoutMutation(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: &fakeAgentInstaller{}})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	resp := httptestDo(t, app, http.MethodDelete, "/api/agents/"+hostID, nil)

	require.Equal(t, http.StatusConflict, resp.Code)
	assert.Contains(t, resp.Body.String(), `"code":"decommission_required"`)
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.True(t, found)
}

// TestCORSRejectsUnknownOriginBeforeAgentUninstallOrDetachMutation 验证未知网页在危险操作执行前被拒绝。
func TestCORSRejectsUnknownOriginBeforeAgentUninstallOrDetachMutation(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		body string
	}{
		{name: "uninstall", path: "/uninstall", body: `{"remove_data":false}`},
		{name: "detach", path: "/detach", body: `{"reason":"manual_uninstall_failed"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeAgentInstaller{}
			app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
			require.NoError(t, err)
			defer app.Close()
			hostID := createInstallTestHost(t, app)
			postInstallTestAgent(t, app, hostID, `
			  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
			  "config":{"listen_port":57019},
			  "security":{"tls":{"mode":"auto"}}
			`)

			req := httptest.NewRequest(http.MethodPost, "/api/agents/"+hostID+tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Origin", "https://attacker.example")
			rr := httptest.NewRecorder()
			app.Handler().ServeHTTP(rr, req)

			require.Equal(t, http.StatusForbidden, rr.Code)
			assert.Empty(t, rr.Header().Get("Access-Control-Allow-Origin"))
			assert.Contains(t, rr.Header().Values("Vary"), "Origin")
			assert.Equal(t, 0, fake.uninstallCalls)
			_, found, err := app.agentStore.AgentByHostID(hostID)
			require.NoError(t, err)
			assert.True(t, found, "未知 Origin 必须在任何 Agent 配置变更前被拒绝")
		})
	}
}

// TestHostDeleteRequiresAgentUninstallOrDetach 验证 Host 删除不能绕过 Agent 卸载或 Detach。
func TestHostDeleteRequiresAgentUninstallOrDetach(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cleanup func(t *testing.T, app *App, hostID string)
	}{
		{
			name: "detach",
			cleanup: func(t *testing.T, app *App, hostID string) {
				resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
				require.Equal(t, http.StatusOK, resp.Code)
			},
		},
		{
			name: "remote uninstall",
			cleanup: func(t *testing.T, app *App, hostID string) {
				resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{"remove_data":false}`))
				require.Equal(t, http.StatusOK, resp.Code)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: &fakeAgentInstaller{}})
			require.NoError(t, err)
			defer app.Close()
			hostID := createInstallTestHost(t, app)
			postInstallTestAgent(t, app, hostID, `
			  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
			  "config":{"listen_port":57019},
			  "security":{"tls":{"mode":"auto"}}
			`)

			blocked := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
			require.Equal(t, http.StatusConflict, blocked.Code)
			assert.Contains(t, blocked.Body.String(), `"code":"agent_configured"`)
			_, hostFound, err := app.remoteHostByID(hostID)
			require.NoError(t, err)
			assert.True(t, hostFound)

			tc.cleanup(t, app, hostID)

			deleted := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
			require.Equal(t, http.StatusOK, deleted.Code)
			_, hostFound, err = app.remoteHostByID(hostID)
			require.NoError(t, err)
			assert.False(t, hostFound)
		})
	}
}
