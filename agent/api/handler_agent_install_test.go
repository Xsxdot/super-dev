// handler_agent_install_test.go 验证 Agent API 的 SSH 直推安装动作。
//
// 职责：
//   - 证明 /api/agents/{host_id}/install 使用 Host SSH 凭据执行安装
//   - 证明直推安装从连接链自动推导远端服务 bind 地址
//
// 边界：
//   - 不执行真实 SSH 连接
//   - 不覆盖 installer 内部上传、systemd/launchd 命令细节
package api

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type fakeAgentInstaller struct {
	host         model.Host
	restartHost  model.Host
	updateHost   model.Host
	opts         installer.ServiceOptions
	calls        int
	restartCalls int
	updateCalls  int
}

func (f *fakeAgentInstaller) Install(ctx context.Context, host model.Host, opts installer.ServiceOptions) (installer.Result, error) {
	f.calls++
	f.host = host
	f.opts = opts
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

func (f *fakeAgentInstaller) Restart(ctx context.Context, host model.Host) (installer.RestartResult, error) {
	f.restartCalls++
	f.restartHost = host
	return installer.RestartResult{OK: true, HostID: host.ID, Platform: "linux", Message: "restarted"}, nil
}

func (f *fakeAgentInstaller) UpdateBinary(ctx context.Context, host model.Host) (installer.UpdateResult, error) {
	f.updateCalls++
	f.updateHost = host
	return installer.UpdateResult{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "Agent binary updated and service restarted"}, nil
}

func createInstallTestHost(t *testing.T, app *App) string {
	t.Helper()
	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{
	  "name":"ali-01",
	  "ssh_host":"10.0.0.8",
	  "ssh_port":22,
	  "ssh_user":"root",
	  "ssh_private_key":"KEY",
	  "ssh_host_key_fingerprint":"SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	  "tags":[]
	}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	return decodeHostID(t, hostResp.Body.Bytes())
}

func postInstallTestAgent(t *testing.T, app *App, hostID string, body string) {
	t.Helper()
	agentResp := httptestDo(t, app, http.MethodPost, "/api/agents", bytes.NewBufferString(`{
	  "host_id":"`+hostID+`",`+body+`
	}`))
	require.Equal(t, http.StatusOK, agentResp.Code)
}

func TestInstallAgentPushesOverSSHWithLoopbackBindForTunnelOnlyChain(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_address":"100.117.127.123","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, hostID, fake.host.ID)
	assert.Equal(t, "10.0.0.8", fake.host.SSHHost)
	assert.Equal(t, "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A", fake.host.SSHHostKeyFingerprint)
	assert.Equal(t, model.LoopbackBindAddress, fake.opts.BindAddress)
	assert.Equal(t, 57019, fake.opts.Port)
	assert.True(t, fake.opts.RequireAuth)
	assert.NotEmpty(t, fake.opts.BootstrapToken)
	record, found := app.latestInstallTokenForHost(hostID)
	require.True(t, found)
	assert.Equal(t, fake.opts.BootstrapToken, record.BootstrapToken)
	assert.Equal(t, model.TransportTypeTunnel, record.TransportType)
	assert.Equal(t, model.LoopbackBindAddress, record.BindAddress)
	assert.Equal(t, 57019, record.RemoteAgentPort)
}

func TestInstallAgentPushesOverSSHWithPublicBindForDirectChainAndSessionToken(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[
	    {"type":"direct","direct":{"address":"100.117.127.123:57019"}},
	    {"type":"tunnel","tunnel":{"remote_agent_port":57019}}
	  ]},
	  "config":{"listen_address":"100.117.127.123","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, model.PublicBindAddress, fake.opts.BindAddress)
	assert.Equal(t, 57019, fake.opts.Port)
	assert.True(t, fake.opts.RequireAuth)
	assert.NotEmpty(t, fake.opts.BootstrapToken)
	record, found := app.latestInstallTokenForHost(hostID)
	require.True(t, found)
	assert.Equal(t, fake.opts.BootstrapToken, record.BootstrapToken)
	assert.Equal(t, model.TransportTypeDirect, record.TransportType)
	assert.Equal(t, model.PublicBindAddress, record.BindAddress)
	assert.Equal(t, 57019, record.RemoteAgentPort)
}

func TestInstallAgentResetsLocalSecurityForFreshBootstrap(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57019},
	  "security":{"token_configured":true,"provision_state":"provisioned","tls":{"mode":"auto","ca_cert":"PEM"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	saved, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, model.AgentProvisionStatePendingBootstrap, saved.Security.ProvisionState)
	assert.False(t, saved.Security.TokenConfigured)
	assert.Empty(t, saved.Secret.Token)
	assert.Equal(t, model.AgentTLSModeAuto, saved.Security.TLS.Mode)
	assert.Empty(t, saved.Security.TLS.CACert)
}

func TestRestartAgentUsesHostSSHCredentials(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57019},
	  "security":{"token_configured":true,"provision_state":"provisioned","tls":{"mode":"auto","ca_cert":"PEM"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/restart", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)
	assert.Equal(t, 1, fake.restartCalls)
	assert.Equal(t, hostID, fake.restartHost.ID)
	assert.Equal(t, "10.0.0.8", fake.restartHost.SSHHost)
}

func TestAgentUpdateTargetReturnsBundledVersionAndConcurrency(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: &fakeAgentInstaller{}})
	require.NoError(t, err)
	defer app.Close()

	resp := httptestDo(t, app, http.MethodGet, "/api/agents/update-target", nil)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"version":"`+agentAPIVersion+`"`)
	assert.Contains(t, resp.Body.String(), `"source":"bundled"`)
	assert.Contains(t, resp.Body.String(), `"concurrency_default":3`)
}

func TestUpdateAgentBinaryUsesHostSSHAndPreservesSecurity(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57019},
	  "security":{"token_configured":true,"provision_state":"provisioned","tls":{"mode":"auto","ca_cert":"PEM"}}
	`)
	app.nodeRegistry.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    hostID,
		Name:      "ali-01",
		Reachable: true,
		Agent: model.AgentRuntime{
			Installed: true,
			Health:    model.AgentHealthHealthy,
			Reachable: true,
		},
	}})

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/update-binary", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)
	assert.Contains(t, resp.Body.String(), `"version":"`+agentAPIVersion+`"`)
	assert.Contains(t, resp.Body.String(), `"updated_at":`)
	assert.Equal(t, 1, fake.updateCalls)
	assert.Equal(t, hostID, fake.updateHost.ID)
	assert.Equal(t, "10.0.0.8", fake.updateHost.SSHHost)
	assert.Equal(t, 0, fake.calls)

	saved, found, err := app.agentStore.AgentByHostID(hostID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, model.AgentProvisionStateProvisioned, saved.Security.ProvisionState)
	assert.True(t, saved.Security.TokenConfigured)
	assert.Equal(t, model.AgentTLSModeAuto, saved.Security.TLS.Mode)
	assert.Equal(t, "PEM", saved.Security.TLS.CACert)
}

func TestUpdateAgentBinaryRejectsUninstalledAgent(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57017}}]},
	  "config":{"listen_port":57017},
	  "security":{"tls":{"mode":"auto"}}
	`)
	app.nodeRegistry.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    hostID,
		Name:      "ali-01",
		Reachable: false,
		Agent: model.AgentRuntime{
			Installed: false,
			Health:    model.AgentHealthUnknown,
			Reachable: false,
		},
	}})

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/update-binary", bytes.NewBufferString(`{}`))

	require.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "agent is not installed")
	assert.Equal(t, 0, fake.updateCalls)
}
