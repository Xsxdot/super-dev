// handler_tunnels_agent_test.go 验证隧道接口外贴 agent 健康状态。
//
// 职责：
//   - 验证 /api/tunnels 快照包含当前 agent 健康状态
//   - 验证 /ws/tunnels 能转发只包含 host_id + agent 的部分更新
//
// 边界：
//   - 不建立真实 SSH 隧道，Dialer 使用 fake conn
//   - 不测试前端 merge 行为
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/agenthealth"
	"github.com/superdev/agent/installer"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/tunnel"
)

// successTunnelDialer 避免测试建立真实 SSH 隧道。
type successTunnelDialer struct{}

func (successTunnelDialer) Dial(host model.Host) (*tunnel.Conn, error) {
	return tunnel.NewFakeConn(57100), nil
}

// staticAgentHealthProber 给测试 Monitor 注入固定探活结果。
type staticAgentHealthProber struct {
	result agenthealth.ProbeResult
	err    error
}

func (s staticAgentHealthProber) Probe(ctx context.Context, hostID string) (agenthealth.ProbeResult, error) {
	if s.err != nil {
		return agenthealth.ProbeResult{}, s.err
	}
	return s.result, nil
}

type recordingHostAgentInstaller struct {
	uninstallHostID     string
	uninstallRemoveData bool
}

func (r *recordingHostAgentInstaller) Install(ctx context.Context, host model.Host) (installer.Result, error) {
	return installer.Result{OK: true, HostID: host.ID, Platform: "linux/amd64", Message: "installed"}, nil
}

func (r *recordingHostAgentInstaller) Uninstall(ctx context.Context, host model.Host, removeData bool) (installer.UninstallResult, error) {
	r.uninstallHostID = host.ID
	r.uninstallRemoveData = removeData
	return installer.UninstallResult{OK: true, HostID: host.ID, RemovedData: removeData, Message: "uninstalled"}, nil
}

func TestListTunnelsIncludesAgentStatus(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true, Version: "0.1.0"},
	})

	host, err := app.remoteStore.AddHost(model.Host{
		ID:      "h1",
		Name:    "srv",
		SSHHost: "127.0.0.1",
		SSHUser: "root",
		Tags:    []string{},
	})
	require.NoError(t, err)
	_, err = app.tunnels.EnsureConnected(host)
	require.NoError(t, err)
	app.agentHealth.ProbeOnce(context.Background(), host.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	rr := httptest.NewRecorder()
	app.listTunnels(rr, req)

	var got []tunnelStatusDTO
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "h1", got[0].HostID)
	assert.Equal(t, "open", got[0].State)
	assert.Equal(t, "healthy", got[0].Agent)
	assert.Equal(t, "0.1.0", got[0].AgentVersion)
	assert.NotEmpty(t, got[0].AgentCheckedAt)
}

func TestCheckHostAgentEnsuresTunnelAndReturnsAgentMeta(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true, Version: "0.1.0"},
	})
	host, err := app.remoteStore.AddHost(model.Host{
		ID:      "h1",
		Name:    "srv",
		SSHHost: "127.0.0.1",
		SSHUser: "root",
		Tags:    []string{},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/hosts/"+host.ID+"/agent/check", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got tunnelStatusDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "h1", got.HostID)
	assert.Equal(t, "open", got.State)
	assert.Equal(t, 57100, got.LocalPort)
	assert.Equal(t, "healthy", got.Agent)
	assert.Equal(t, "0.1.0", got.AgentVersion)
	assert.NotEmpty(t, got.AgentCheckedAt)
}

func TestUninstallHostAgentPassesRemoveDataChoice(t *testing.T) {
	recorder := &recordingHostAgentInstaller{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), InstallerOverride: recorder})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{
		ID:      "h1",
		Name:    "srv",
		SSHHost: "127.0.0.1",
		SSHUser: "root",
		Tags:    []string{},
	})
	require.NoError(t, err)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	body := bytes.NewBufferString(`{"remove_data":true}`)
	resp, err := http.Post(srv.URL+"/api/hosts/"+host.ID+"/agent/uninstall", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got uninstallHostAgentResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.True(t, got.Result.OK)
	assert.True(t, got.Result.RemovedData)
	assert.Equal(t, "h1", got.Tunnel.HostID)
	assert.Equal(t, "idle", got.Tunnel.State)
	assert.Equal(t, "unreachable", got.Tunnel.Agent)
	assert.NotEmpty(t, got.Tunnel.AgentCheckedAt)
	assert.Equal(t, "h1", recorder.uninstallHostID)
	assert.True(t, recorder.uninstallRemoveData)
}

func TestWsTunnelsForwardsAgentHealthPartialUpdate(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true, Version: "0.1.0"},
	})
	app.agentHealth.SetPollInterval(time.Hour)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/tunnels"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// Dial 返回后 handler 仍可能刚完成 Upgrade；给订阅注册一个短暂窗口，避免丢掉首个 agent 事件。
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan agenthealth.TunnelSignal, 1)
	go app.agentHealth.Run(ctx, signals)
	signals <- agenthealth.TunnelSignal{HostID: "h1", Connected: true}

	var got map[string]any
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	require.NoError(t, conn.ReadJSON(&got))
	assert.Equal(t, "h1", got["host_id"])
	assert.Equal(t, "healthy", got["agent"])
	assert.Equal(t, "0.1.0", got["agent_version"])
	assert.NotEmpty(t, got["agent_checked_at"])
	assert.NotContains(t, got, "state")
	assert.NotContains(t, got, "local_port")
	assert.NotContains(t, got, "error")
}
