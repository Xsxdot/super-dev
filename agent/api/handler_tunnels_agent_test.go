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
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// successTunnelDialer 避免测试建立真实 SSH 隧道。
type successTunnelDialer struct{}

func (successTunnelDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	return tunnel.NewFakeVerifiedConn(57100, "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68"), nil
}

type leakingTunnelDialer struct{}

func (leakingTunnelDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	return nil, errors.New("dial 203.0.113.10:22 failed for SHA256:raw-fingerprint with PRIVATE-KEY")
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

func TestListTunnelsIncludesAgentStatus(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	app.tunnels = tunnel.NewManager(successTunnelDialer{})
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true, Version: "0.1.0"},
	})

	host, err := app.remoteStore.AddHost(testTunnelHost("h1", "srv", "127.0.0.1", "root"))
	require.NoError(t, err)
	agent, err := app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{{
		Type:   model.TransportTypeTunnel,
		Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
	}}}})
	require.NoError(t, err)
	_, err = app.tunnels.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(nodetransport.NodeTarget{Host: host, Agent: agent}))
	require.NoError(t, err)
	app.agentHealth.ProbeOnce(context.Background(), host.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/tunnels", nil)
	rr := httptest.NewRecorder()
	app.listTunnels(rr, req)

	var got []tunnelStatusDTO
	responseBody := rr.Body.Bytes()
	require.NoError(t, json.Unmarshal(responseBody, &got))
	require.Len(t, got, 1)
	assert.Equal(t, "h1", got[0].HostID)
	assert.Equal(t, "open", got[0].State)
	assert.Equal(t, "healthy", got[0].Agent)
	assert.Equal(t, "0.1.0", got[0].AgentVersion)
	assert.NotEmpty(t, got[0].AgentCheckedAt)
	require.NotNil(t, got[0].HostKeyVerified)
	assert.True(t, *got[0].HostKeyVerified)
	assert.Equal(t, "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68", got[0].HostKeyIdentitySHA256)
	assert.NotContains(t, string(responseBody), "127.0.0.1")
	assert.NotContains(t, string(responseBody), "SHA256:")
}

func TestTunnelHTTPResponsesProjectFailuresWithoutAddressesOrKeyMaterial(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(leakingTunnelDialer{})

	host, err := app.remoteStore.AddHost(model.Host{
		ID:                    "h1",
		Name:                  "srv",
		SSHHost:               "203.0.113.10",
		SSHUser:               "root",
		SSHPassword:           "secret-password",
		SSHPrivateKey:         "PRIVATE-KEY",
		SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{{
		Type:   model.TransportTypeTunnel,
		Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
	}}}})
	require.NoError(t, err)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	connectResp, err := http.Post(srv.URL+"/api/tunnels/"+host.ID, "application/json", nil)
	require.NoError(t, err)
	connectBody, err := io.ReadAll(connectResp.Body)
	require.NoError(t, err)
	_ = connectResp.Body.Close()
	require.Equal(t, http.StatusBadGateway, connectResp.StatusCode)
	assert.Contains(t, string(connectBody), "ssh_connection_failed")
	assertTunnelPayloadIsSafe(t, connectBody)

	listResp, err := http.Get(srv.URL + "/api/tunnels")
	require.NoError(t, err)
	listBody, err := io.ReadAll(listResp.Body)
	require.NoError(t, err)
	_ = listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Contains(t, string(listBody), `"host_key_verified":false`)
	assert.Contains(t, string(listBody), "ssh_connection_failed")
	assertTunnelPayloadIsSafe(t, listBody)
}

func TestWsTunnelsProjectsStableFailureWithoutAddressesOrKeyMaterial(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()
	app.tunnels = tunnel.NewManager(leakingTunnelDialer{})

	host, err := app.remoteStore.AddHost(model.Host{
		ID:                    "h1",
		Name:                  "srv",
		SSHHost:               "203.0.113.10",
		SSHUser:               "root",
		SSHPassword:           "secret-password",
		SSHPrivateKey:         "PRIVATE-KEY",
		SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{{
		Type:   model.TransportTypeTunnel,
		Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
	}}}})
	require.NoError(t, err)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/tunnels"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()
	// Upgrade 完成后给 handler 一个短暂窗口注册订阅，避免即时 fake dialer 的事件先于订阅发出。
	time.Sleep(20 * time.Millisecond)

	connectResp, err := http.Post(srv.URL+"/api/tunnels/"+host.ID, "application/json", nil)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, connectResp.Body)
	_ = connectResp.Body.Close()
	require.Equal(t, http.StatusBadGateway, connectResp.StatusCode)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	for i := 0; i < 3; i++ {
		_, payload, readErr := conn.ReadMessage()
		require.NoError(t, readErr)
		var got tunnelStatusDTO
		require.NoError(t, json.Unmarshal(payload, &got))
		if got.State != "failed" {
			continue
		}
		assert.Equal(t, "ssh_connection_failed", got.Error)
		require.NotNil(t, got.HostKeyVerified)
		assert.False(t, *got.HostKeyVerified)
		assertTunnelPayloadIsSafe(t, payload)
		return
	}
	t.Fatal("websocket did not receive failed tunnel event")
}

func assertTunnelPayloadIsSafe(t *testing.T, body []byte) {
	t.Helper()
	assert.NotContains(t, string(body), "203.0.113.10")
	assert.NotContains(t, string(body), "SHA256:")
	assert.NotContains(t, string(body), "PRIVATE-KEY")
	assert.NotContains(t, string(body), "secret-password")
}

func TestCheckAgentReturnsAgentDTOWithRuntime(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true, Version: "0.1.0"},
	})
	host, err := app.remoteStore.AddHost(testTunnelHost("h1", "srv", "127.0.0.1", "root"))
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{{
		Type:   model.TransportTypeTunnel,
		Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
	}}}})
	require.NoError(t, err)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/api/agents/"+host.ID+"/check", "application/json", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got agentDTO
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "h1", got.HostID)
	assert.Equal(t, model.AgentHealthHealthy, got.Runtime.Health)
	assert.Equal(t, "0.1.0", got.Runtime.Version)
	assert.True(t, got.Runtime.Reachable)
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
