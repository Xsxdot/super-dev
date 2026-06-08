// handler_agent_install_test.go 验证 Agent API 的 SSH 直推安装动作。
//
// 职责：
//   - 证明 /api/agents/{host_id}/install 使用 Host SSH 凭据执行安装
//   - 证明直推安装读取 Agent 的监听配置作为远端服务参数
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
	host model.Host
	opts installer.ServiceOptions
}

func (f *fakeAgentInstaller) Install(ctx context.Context, host model.Host, opts installer.ServiceOptions) (installer.Result, error) {
	f.host = host
	f.opts = opts
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

func TestInstallAgentPushesOverSSHWithAgentListenConfig(t *testing.T) {
	dataDir := t.TempDir()
	fake := &fakeAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: dataDir, InstallerOverride: fake})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{
	  "name":"ali-01",
	  "ssh_host":"10.0.0.8",
	  "ssh_port":22,
	  "ssh_user":"root",
	  "ssh_private_key":"KEY",
	  "tags":[]
	}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())

	agentResp := httptestDo(t, app, http.MethodPost, "/api/agents", bytes.NewBufferString(`{
	  "host_id":"`+hostID+`",
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57019}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57019},
	  "security":{"tls":{"mode":"auto"}}
	}`))
	require.Equal(t, http.StatusOK, agentResp.Code)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install", bytes.NewBufferString(`{"method":"push_over_ssh"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ok":true`)
	assert.Equal(t, hostID, fake.host.ID)
	assert.Equal(t, "10.0.0.8", fake.host.SSHHost)
	assert.Equal(t, "0.0.0.0", fake.opts.BindAddress)
	assert.Equal(t, 57019, fake.opts.Port)
}
