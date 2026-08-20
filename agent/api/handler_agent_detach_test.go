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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// TestDetachAgentRemovesOnlyControllerConfigAndPreservesHost 验证受信 Desktop Origin 的 Detach 只删除 Controller 配置。
func TestDetachAgentRemovesOnlyControllerConfigAndPreservesHost(t *testing.T) {
	fake := &fakeAgentInstaller{}
	registry := noderegistry.New(nil, noderegistry.Options{})
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake, NodeRegistryOverride: registry})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)
	registry.ApplyForTest([]nodetransport.NodeStatus{
		{HostID: hostID, Agent: model.AgentRuntime{Installed: true, Version: "0.2.3", Reachable: true}},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	req.Header.Set("Origin", "tauri://localhost")
	resp := httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
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
	_, snapshotFound := registry.SnapshotOf(hostID)
	assert.False(t, snapshotFound, "Detach 成功后 registry 不应保留该 host 的陈旧快照")
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
	app.detachAgentConfig = func(context.Context, string) error { return errors.New("fixture agents.json write failure") }

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
			req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
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

// TestDetachAgentRetryAfterAuditFailureCompletesRecovery 验证 Detach 在配置已删、
// executed 审计未持久化时返回 500 后，重试能进入底层恢复逻辑完成同一份审计计划。
func TestDetachAgentRetryAfterAuditFailureCompletesRecovery(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditStore := &recoverableTunnelInvalidationAuditStore{failExecuted: true}
	app.operationAudit = auditStore
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	detach := func() *httptest.ResponseRecorder {
		return httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	}

	first := detach()
	require.Equal(t, http.StatusInternalServerError, first.Code)
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, found, "配置已删、审计未终态是 Detach 的部分失败状态")
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, operation.AuditPrepared, events[0].Action)

	auditStore.setFailExecuted(false)
	second := detach()
	require.Equal(t, http.StatusOK, second.Code, "修复审计存储后重试 Detach 必须成功")
	events, err = auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}

// TestUninstallDoesNotRecoverDetachPartialFailure 验证 Detach 的部分失败（配置已删、
// 审计未终态）不会被随后的 Uninstall 误报为远端卸载成功：Uninstall 必须报错、
// 不执行远端卸载、也不吞掉 Detach 的计划，该计划只能由 Detach 重试完成。
func TestUninstallDoesNotRecoverDetachPartialFailure(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditStore := &recoverableTunnelInvalidationAuditStore{failExecuted: true}
	app.operationAudit = auditStore
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	detachFirst := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	require.Equal(t, http.StatusInternalServerError, detachFirst.Code)
	require.Equal(t, 0, fake.uninstallCalls, "Detach 不连接远端 Host")

	auditStore.setFailExecuted(false)
	uninstall := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusBadGateway, uninstall.Code, "Detach 的部分失败不得被 Uninstall 报成卸载成功")
	assert.NotContains(t, uninstall.Body.String(), "already uninstalled")
	assert.Equal(t, 0, fake.uninstallCalls, "配置来自 Detach 时不得执行远端卸载")
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 1, "Detach 的审计计划必须保持未终态，等待 Detach 重试完成")
	assert.Equal(t, operation.AuditPrepared, events[0].Action)

	detachRetry := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	require.Equal(t, http.StatusOK, detachRetry.Code)
	events, err = auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}

// TestDetachRecoversUninstallPartialFailure 验证反向交叉路径：Uninstall 在 config_remove
// 阶段部分失败（配置已删、审计未终态）后，Detach 作为逃生操作可以完成同一份审计计划并
// 返回成功——Detach 只宣称"Controller 配置已移除"，两种来源的计划它都有权补偿。
func TestDetachRecoversUninstallPartialFailure(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	auditStore := &recoverableTunnelInvalidationAuditStore{failExecuted: true}
	app.operationAudit = auditStore
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	uninstall := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusInternalServerError, uninstall.Code)
	assert.Contains(t, uninstall.Body.String(), `"stage":"config_remove"`)
	assert.Equal(t, 1, fake.uninstallCalls, "远端卸载已执行过一次")
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, found)
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, operation.AuditPrepared, events[0].Action)

	auditStore.setFailExecuted(false)
	detach := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	require.Equal(t, http.StatusOK, detach.Code, "Detach 应能补偿 Uninstall 留下的未终态计划")
	assert.Equal(t, 1, fake.uninstallCalls, "Detach 不连接远端 Host")
	events, err = auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}
