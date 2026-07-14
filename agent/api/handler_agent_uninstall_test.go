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
)

type blockingAgentInstaller struct {
	uninstallStarted chan struct{}
	releaseUninstall chan struct{}
}

func (b *blockingAgentInstaller) Install(_ context.Context, host model.Host, _ installer.ServiceOptions) (installer.Result, error) {
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

func (b *blockingAgentInstaller) Uninstall(_ context.Context, host model.Host, removeData bool) (installer.UninstallResult, error) {
	close(b.uninstallStarted)
	<-b.releaseUninstall
	return installer.UninstallResult{OK: true, HostID: host.ID, RemovedData: removeData, Message: "Agent uninstalled"}, nil
}

func (b *blockingAgentInstaller) Restart(_ context.Context, host model.Host) (installer.RestartResult, error) {
	return installer.RestartResult{OK: true, HostID: host.ID, Platform: "linux", Message: "restarted"}, nil
}

func (b *blockingAgentInstaller) UpdateBinary(_ context.Context, host model.Host) (installer.UpdateResult, error) {
	return installer.UpdateResult{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "updated"}, nil
}

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
	app.removeAgentConfig = func(candidateHostID string) error {
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
