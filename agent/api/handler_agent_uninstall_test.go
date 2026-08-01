// handler_agent_uninstall_test.go 验证 Agent HTTP 生命周期的安全卸载契约。
//
// 职责：
//   - 证明远端卸载成功后才移除 Controller Agent 配置
//   - 证明默认卸载保留远端 Agent 数据和 Host 记录
//
// 边界：
//   - 不执行真实 SSH 连接
//   - 不验证 installer 内部的平台命令序列
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
	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

type blockingAgentInstaller struct {
	uninstallStarted chan struct{}
	releaseUninstall chan struct{}
}

// Install 立即成功返回；本 fake 只为卸载生命周期测试占位，不模拟安装行为。
func (b *blockingAgentInstaller) Install(_ context.Context, host model.Host, _ installer.ServiceOptions) (installer.Result, error) {
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

// Uninstall 阻塞在 releaseUninstall 上，用于验证同 Host 生命周期操作的互斥门。
func (b *blockingAgentInstaller) Uninstall(_ context.Context, host model.Host, removeData bool) (installer.UninstallResult, error) {
	close(b.uninstallStarted)
	<-b.releaseUninstall
	return installer.UninstallResult{OK: true, HostID: host.ID, RemovedData: removeData, Message: "Agent uninstalled"}, nil
}

// Restart 立即成功返回；本 fake 只为互斥门用例提供可调用的重启桩。
func (b *blockingAgentInstaller) Restart(_ context.Context, host model.Host) (installer.RestartResult, error) {
	return installer.RestartResult{OK: true, HostID: host.ID, Platform: "linux", Message: "restarted"}, nil
}

// UpdateBinary 立即成功返回；本 fake 只为互斥门用例提供可调用的更新桩。
func (b *blockingAgentInstaller) UpdateBinary(_ context.Context, host model.Host) (installer.UpdateResult, error) {
	return installer.UpdateResult{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "updated"}, nil
}

// TestUninstallAgentRemovesConfigAfterRemoteSuccessAndPreservesHost 验证远端卸载成功后
// 才移除 Controller Agent 配置，且默认保留远端数据与 Host 记录。
func TestUninstallAgentRemovesConfigAfterRemoteSuccessAndPreservesHost(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)
	assert.Equal(t, 1, fake.uninstallCalls)
	assert.Equal(t, hostID, fake.uninstallHost.ID)
	assert.Equal(t, "10.0.0.8", fake.uninstallHost.SSHHost)
	assert.False(t, fake.removeData)

	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, found)
	_, hostFound, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	assert.True(t, hostFound)
}

// TestUninstallAgentPurgesRemoteDataOnlyWhenExplicitlyRequested 验证只有显式选择
// remove_data 时才清除远端 Agent 数据。
func TestUninstallAgentPurgesRemoteDataOnlyWhenExplicitlyRequested(t *testing.T) {
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

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{"remove_data":true}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, fake.removeData)
	assert.Contains(t, resp.Body.String(), `"removed_data":true`)
}

// TestUninstallAgentRetainsConfigForEveryRemoteFailureStage 验证远端卸载的任一平台阶段
// 失败都保留 Controller 配置，便于修复后安全重试。
func TestUninstallAgentRetainsConfigForEveryRemoteFailureStage(t *testing.T) {
	stages := []string{"connect", "detect_platform", "uninstall_systemd", "uninstall_launchd", "uninstall_windows_task"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			fake := &fakeAgentInstaller{uninstallErr: &installer.InstallError{Stage: stage, Err: errors.New("fixture failure")}}
			app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
			require.NoError(t, err)
			defer app.Close()
			hostID := createInstallTestHost(t, app)
			postInstallTestAgent(t, app, hostID, `
			  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
			  "config":{"listen_port":57019},
			  "security":{"tls":{"mode":"auto"}}
			`)

			resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{"remove_data":false}`))

			require.Equal(t, http.StatusBadGateway, resp.Code)
			assert.Contains(t, resp.Body.String(), `"stage":"remote_uninstall"`)
			assert.Contains(t, resp.Body.String(), stage)
			_, found, err := app.agentStore.AgentByHostID(hostID)
			require.NoError(t, err)
			assert.True(t, found)
		})
	}
}

// TestUninstallAgentReportsConfigRemoveFailureAndRetryCompletes 验证远端卸载成功但
// Controller 配置移除失败时报告 config_remove，且重试可幂等完成。
func TestUninstallAgentReportsConfigRemoveFailureAndRetryCompletes(t *testing.T) {
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
	realRemove := app.agentStore.RemoveAgent
	removeCalls := 0
	app.removeAgentConfig = func(_ context.Context, candidateHostID string) error {
		removeCalls++
		assert.Equal(t, hostID, candidateHostID)
		assert.GreaterOrEqual(t, fake.uninstallCalls, 1, "Controller 配置不能先于远端卸载移除")
		if removeCalls == 1 {
			return errors.New("fixture agents.json write failure")
		}
		return realRemove(candidateHostID)
	}

	first := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusInternalServerError, first.Code)
	assert.Contains(t, first.Body.String(), `"stage":"config_remove"`)
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.True(t, found)

	second := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 2, fake.uninstallCalls)
	_, found, err = app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, found)
	_, hostFound, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	assert.True(t, hostFound)
}

// TestAgentLifecycleOperationsRejectSameHostConflictWithoutBlockingOtherHosts 验证同一
// Host 的生命周期操作互斥返回 409，且不影响其他 Host 的操作。
func TestAgentLifecycleOperationsRejectSameHostConflictWithoutBlockingOtherHosts(t *testing.T) {
	fake := &blockingAgentInstaller{
		uninstallStarted: make(chan struct{}),
		releaseUninstall: make(chan struct{}),
	}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)
	otherHostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, otherHostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.124:57019"}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	uninstallResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))
		// 鉴权常开后必须带凭据，否则请求在 withSecurity 就被 401 拒绝，
		// fake.uninstallStarted 永远不会被 close，下面的 <-fake.uninstallStarted 会死等。
		req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
		rr := httptest.NewRecorder()
		app.Handler().ServeHTTP(rr, req)
		uninstallResult <- rr
	}()
	<-fake.uninstallStarted

	conflicts := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "install", method: http.MethodPost, path: "/api/agents/" + hostID + "/install", body: `{"method":"push_over_ssh"}`},
		{name: "restart", method: http.MethodPost, path: "/api/agents/" + hostID + "/restart", body: `{}`},
		{name: "update binary", method: http.MethodPost, path: "/api/agents/" + hostID + "/update-binary", body: `{}`},
		{name: "update transport", method: http.MethodPut, path: "/api/agents/" + hostID + "/transport", body: `{"transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.8:57017"}}]}}`},
		{name: "update config", method: http.MethodPut, path: "/api/agents/" + hostID + "/config", body: `{"config":{"listen_port":57018},"security":{"tls":{"mode":"auto"}}}`},
		{name: "detach", method: http.MethodPost, path: "/api/agents/" + hostID + "/detach", body: `{"reason":"manual_uninstall_failed"}`},
		{name: "delete host", method: http.MethodDelete, path: "/api/hosts/" + hostID},
	}
	for _, tc := range conflicts {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptestDo(t, app, tc.method, tc.path, bytes.NewBufferString(tc.body))
			require.Equal(t, http.StatusConflict, resp.Code)
			assert.Contains(t, resp.Body.String(), `"code":"operation_in_progress"`)
		})
	}

	otherResp := httptestDo(t, app, http.MethodPut, "/api/agents/"+otherHostID+"/transport", bytes.NewBufferString(`{"transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.9:57017"}}]}}`))
	require.Equal(t, http.StatusOK, otherResp.Code)

	close(fake.releaseUninstall)
	require.Equal(t, http.StatusOK, (<-uninstallResult).Code)
}

// TestUninstallAgentRetryAfterConfigRemoveAuditFailureCompletesRecovery 验证
// config_remove 阶段"配置已删、executed 审计未持久化"的部分失败后，重试卸载
// 不再被挡在 remote_uninstall，而是幂等完成同一份审计计划并返回成功。
func TestUninstallAgentRetryAfterConfigRemoveAuditFailureCompletesRecovery(t *testing.T) {
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

	uninstall := func() *httptest.ResponseRecorder {
		return httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))
	}

	first := uninstall()
	require.Equal(t, http.StatusInternalServerError, first.Code)
	assert.Contains(t, first.Body.String(), `"stage":"config_remove"`)
	// 部分失败的真实状态：配置已删、审计只到 prepared；重试必须能从这个状态恢复。
	_, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	assert.False(t, found)
	events, err := auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, operation.AuditPrepared, events[0].Action)

	auditStore.setFailExecuted(false)
	second := uninstall()
	require.Equal(t, http.StatusOK, second.Code)
	assert.Equal(t, 1, fake.uninstallCalls, "配置已删除时重试不应再次执行远端卸载")
	events, err = auditStore.List(context.Background(), operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, operation.AuditExecuted, events[0].Action)
	assert.Equal(t, events[1].Plan.ID, events[0].Plan.ID)
}

// TestUninstallAgentAfterDetachDoesNotReportRemoteUninstall 验证 Detach 只移除 Controller
// 配置后，Uninstall 不得把"配置不存在"误报为远端卸载成功：必须返回错误且不再调用远端卸载。
func TestUninstallAgentAfterDetachDoesNotReportRemoteUninstall(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	detachResp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/detach", bytes.NewBufferString(`{"reason":"manual_uninstall_failed"}`))
	require.Equal(t, http.StatusOK, detachResp.Code)
	require.Equal(t, 0, fake.uninstallCalls, "Detach 不连接远端 Host")

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/uninstall", bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusBadGateway, resp.Code, "Detach 后 Uninstall 不得返回卸载成功")
	assert.Contains(t, resp.Body.String(), `"stage":"remote_uninstall"`)
	assert.NotContains(t, resp.Body.String(), "already uninstalled")
	assert.Equal(t, 0, fake.uninstallCalls, "配置不存在时不得执行远端卸载")
}
