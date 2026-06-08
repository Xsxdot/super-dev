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
)

type fakeAgentInstaller struct {
	host  model.Host
	opts  installer.ServiceOptions
	calls int
}

func (f *fakeAgentInstaller) Install(ctx context.Context, host model.Host, opts installer.ServiceOptions) (installer.Result, error) {
	f.calls++
	f.host = host
	f.opts = opts
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

func createInstallTestHost(t *testing.T, app *App) string {
	t.Helper()
	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{
	  "name":"ali-01",
	  "ssh_host":"10.0.0.8",
	  "ssh_port":22,
	  "ssh_user":"root",
	  "ssh_private_key":"KEY",
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
	assert.Equal(t, model.LoopbackBindAddress, fake.opts.BindAddress)
	assert.Equal(t, 57019, fake.opts.Port)
	assert.False(t, fake.opts.RequireAuth)
	assert.Empty(t, fake.opts.BootstrapToken)
}

func TestInstallAgentPushesOverSSHWithPublicBindForDirectChainAndBootstrapToken(t *testing.T) {
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
	app.cfg.BootstrapToken = "bootstrap-token"

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, 1, fake.calls)
	assert.Equal(t, model.PublicBindAddress, fake.opts.BindAddress)
	assert.Equal(t, 57019, fake.opts.Port)
	assert.True(t, fake.opts.RequireAuth)
	assert.Equal(t, "bootstrap-token", fake.opts.BootstrapToken)
}

func TestInstallAgentPushOverSSHRejectsDirectChainWithoutBootstrapToken(t *testing.T) {
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()
	hostID := createInstallTestHost(t, app)
	postInstallTestAgent(t, app, hostID, `
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.117.127.123:57019"}}]},
	  "config":{"listen_address":"100.117.127.123","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	`)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "direct connection chain requires agent to listen on 0.0.0.0")
	assert.Equal(t, 0, fake.calls)
}
