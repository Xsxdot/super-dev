package nodetransport_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

const (
	testHostKeyFingerprint = "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A"
	testHostKeyIdentity    = "2b8d2037d26edc9d429d8cd7e3d043c09d5f0bea6c2d3b63c35769c6ab8f2d68"
)

type fakeDialer struct{ port int }

func (f fakeDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	return tunnel.NewFakeVerifiedConn(f.port, testHostKeyIdentity), nil
}

type recordingDialer struct {
	port   int
	target tunnel.Target
}

func (d *recordingDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.target = target
	return tunnel.NewFakeVerifiedConn(d.port, testHostKeyIdentity), nil
}

type sequenceDialer struct {
	ports []int
	calls int
}

func (d *sequenceDialer) Dial(target tunnel.Target) (*tunnel.Conn, error) {
	d.calls++
	idx := d.calls - 1
	if idx >= len(d.ports) {
		idx = len(d.ports) - 1
	}
	return tunnel.NewFakeVerifiedConn(d.ports[idx], testHostKeyIdentity), nil
}

func tunnelNode(id, name string) nodetransport.NodeTarget {
	return nodetransport.NodeTarget{
		Host: model.Host{
			ID: id, Name: name, SSHHost: "10.0.0.8", SSHPort: 22, SSHUser: "root",
			SSHHostKeyFingerprint: testHostKeyFingerprint,
		},
		Agent: model.Agent{
			HostID: id,
			Transport: model.TransportConfig{Chain: []model.TransportEntry{{
				Type:   model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
			}}},
			Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeOff}},
		},
	}
}

func TestTunnelTransportUsesHostSSHAndAgentRemotePort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	dialer := &recordingDialer{port: serverPort(t, server.URL)}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()
	target := nodetransport.NodeTarget{
		Host: model.Host{
			ID: "h1", Name: "ali", SSHHost: "10.0.0.8", SSHPort: 2222, SSHUser: "root", SSHPrivateKey: "KEY",
			SSHHostKeyFingerprint: testHostKeyFingerprint,
		},
		Agent: model.Agent{
			HostID: "h1",
			Transport: model.TransportConfig{Chain: []model.TransportEntry{{
				Type:   model.TransportTypeTunnel,
				Tunnel: &model.TunnelParams{RemoteAgentPort: 57018},
			}}},
			Secret:   model.AgentSecret{Token: "tok"},
			Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeOff}},
		},
	}
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))

	covers := tr.Covers()
	require.Equal(t, []string{"h1"}, covers)
	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, "h1", dialer.target.HostID)
	assert.Equal(t, "10.0.0.8", dialer.target.SSHHost)
	assert.Equal(t, 2222, dialer.target.SSHPort)
	assert.Equal(t, "root", dialer.target.SSHUser)
	assert.Equal(t, "KEY", dialer.target.SSHPrivateKey)
	assert.Equal(t, testHostKeyFingerprint, dialer.target.SSHHostKeyFingerprint)
	assert.Equal(t, 57018, dialer.target.RemoteAgentPort)
}

func tunnelSource(targets ...nodetransport.NodeTarget) nodetransport.TargetSource {
	return func() ([]nodetransport.NodeTarget, error) {
		return targets, nil
	}
}

func serverPort(t *testing.T, rawURL string) int {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	_, portText, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	return port
}

func TestTunnelTransportDoRoutesToConnectedLocalPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/logs", r.URL.Path)
		assert.Equal(t, "dep-1", r.URL.Query().Get("deployment"))
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "yes"})
	}))
	defer srv.Close()

	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	target := tunnelNode("h1", "ali-01")
	_, err := mgr.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(target))
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))
	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
		Query:  url.Values{"deployment": []string{"dep-1"}},
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTunnelTransportInjectsAgentToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer tunnel-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := tunnelNode("h1", "ali-01")
	target.Agent.Secret.Token = "tunnel-token"
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestTunnelTransportDoReturnsUnreachableWhenNoLocalPort(t *testing.T) {
	mgr := tunnel.NewManager(fakeDialer{port: 0})
	defer mgr.Close()

	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource())
	_, err := tr.Do(context.Background(), "missing", nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
	})
	require.ErrorIs(t, err, nodetransport.ErrHostUnreachable)
}

func TestTunnelTransportStreamUsesWebSocketScheme(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ws/logs", r.URL.Path)
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON(map[string]string{"message": "hello"}))
	}))
	defer srv.Close()

	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	target := tunnelNode("h1", "ali-01")
	_, err := mgr.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(target))
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))
	stream, err := tr.Stream(context.Background(), "h1", nodetransport.NodeRequest{
		Path: "/ws/logs",
	})
	require.NoError(t, err)
	defer stream.Close()
	var got map[string]string
	require.NoError(t, stream.ReadJSON(&got))
	assert.Equal(t, "hello", got["message"])
}

func TestTunnelTransportCoversOnlyTunnelHosts(t *testing.T) {
	tunnelTarget := tunnelNode("h-tunnel", "tunnel")
	directTarget := directTarget("h-direct", "direct", "100.64.0.8:57017")
	tr := nodetransport.NewTunnelTransport(tunnel.NewManager(fakeDialer{}), tunnelSource(tunnelTarget, directTarget))

	assert.Equal(t, []string{"h-tunnel"}, tr.Covers())
}

func TestNodeStatusJSONShape(t *testing.T) {
	now := time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC)
	status := nodetransport.NodeStatus{
		HostID:    "h1",
		Name:      "ali-01",
		Reachable: true,
		Agent: model.AgentRuntime{
			Installed: true,
			Version:   "0.1.0",
			Health:    model.AgentHealthHealthy,
			Reachable: true,
			LocalPort: 57100,
		},
		Deployments: []model.InstanceStatus{{
			ServiceID:    "svc-1",
			ServiceName:  "api",
			DeploymentID: "dep-1",
			NodeID:       "h1",
			NodeName:     "ali-01",
			IsLocal:      false,
			Metrics: model.InstanceMetrics{
				Health: model.HealthRunning,
				Base:   "systemd",
			},
		}},
		Managed: &model.ManagedDeploymentStatus{
			DeploymentCount: 1,
			CollectorCount:  1,
			Collectors: []model.ManagedCollectorStatus{{
				DeploymentID: "dep-1",
				ServiceName:  "api",
				Desired:      true,
				Running:      true,
			}},
		},
		UpdatedAt: now,
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"host_id":"h1"`)
	assert.Contains(t, string(data), `"updated_at":"2026-06-06T10:00:00Z"`)
	assert.Contains(t, string(data), `"managed"`)
	assert.NotContains(t, string(data), `"HostID"`)
}

func TestUnreachableNodeStatusJSONKeepsDeploymentsArray(t *testing.T) {
	status := nodetransport.NodeStatus{
		HostID:    "h1",
		Name:      "ali-01",
		Reachable: false,
		Agent: model.AgentRuntime{
			Health:    model.AgentHealthUnreachable,
			Reachable: false,
		},
		UpdatedAt: time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
		Error:     "node unreachable",
	}

	data, err := json.Marshal(status)
	require.NoError(t, err)

	assert.Contains(t, string(data), `"deployments":[]`)
	assert.NotContains(t, string(data), `"deployments":null`)
}

func TestTunnelTransportSubscribeNodesStreamsConnectedHosts(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/ws/node-status", r.URL.Path)
		require.Equal(t, "h1", r.URL.Query().Get("host_id"))
		require.Equal(t, "ali-01", r.URL.Query().Get("host_name"))
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON([]nodetransport.NodeStatus{{
			HostID:    r.URL.Query().Get("host_id"),
			Name:      r.URL.Query().Get("host_name"),
			Reachable: true,
			Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
			UpdatedAt: time.Now().UTC(),
		}}))
	}))
	defer srv.Close()

	target := tunnelNode("h1", "ali-01")
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	_, err := mgr.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(target))
	require.NoError(t, err)
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))
	tr.SetStatusReconnectIntervalForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := tr.SubscribeNodes(ctx)
	defer stop()

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].HostID == "h1" && batch[0].Reachable
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestTunnelTransportSubscribeNodesEmitsUnreachableWithoutBlockingOtherHosts(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON([]nodetransport.NodeStatus{{
			HostID:    r.URL.Query().Get("host_id"),
			Name:      r.URL.Query().Get("host_name"),
			Reachable: true,
			Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
			UpdatedAt: time.Now().UTC(),
		}}))
	}))
	defer srv.Close()

	connected := tunnelNode("h1", "connected")
	missing := tunnelNode("h2", "missing")

	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	_, err := mgr.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(connected))
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(connected, missing))
	tr.SetStatusReconnectIntervalForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := tr.SubscribeNodes(ctx)
	defer stop()

	seen := map[string]bool{}
	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			for _, status := range batch {
				seen[status.HostID+":"+strconv.FormatBool(status.Reachable)] = true
			}
		default:
		}
		return seen["h1:true"] && seen["h2:false"]
	}, time.Second, 10*time.Millisecond)
}

func TestTunnelTransportDoEnsuresTunnelBeforeRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/exec/health", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	target := tunnelNode("h1", "ali-01")
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTunnelTransportInvalidatesStaleTunnelAfterRequestFailure(t *testing.T) {
	stale := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	stalePort := serverPort(t, stale.URL)
	stale.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/exec/health", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := tunnelNode("h1", "ali-01")
	dialer := &sequenceDialer{ports: []int{stalePort, serverPort(t, srv.URL)}}
	mgr := tunnel.NewManager(dialer)
	defer mgr.Close()
	_, err := mgr.EnsureConnected(nodetransport.TunnelTargetFromNodeTarget(target))
	require.NoError(t, err)
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))

	_, err = tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.Error(t, err)
	assert.Equal(t, nodetransport.CodeTransportUnreachable, nodetransport.ErrorCode(err))
	assert.Equal(t, tunnel.StatusFailed, mgr.Status("h1"))
	assert.Equal(t, 0, mgr.LocalPort("h1"))

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, 2, dialer.calls)
	assert.Equal(t, serverPort(t, srv.URL), mgr.LocalPort("h1"))
}

func TestTunnelTransportSubscribeNodesEnsuresTunnel(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON([]nodetransport.NodeStatus{{
			HostID:    "h1",
			Name:      "ali-01",
			Reachable: true,
			Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
			UpdatedAt: time.Now().UTC(),
		}}))
	}))
	defer srv.Close()

	target := tunnelNode("h1", "ali-01")
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))
	tr.SetStatusReconnectIntervalForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := tr.SubscribeNodes(ctx)
	defer stop()

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].Reachable
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestTunnelTransportSubscribeNodesReportsVersionMismatchForOldAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws/node-status" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/api/exec/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	target := tunnelNode("h1", "old-agent")
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, tunnelSource(target))
	tr.SetStatusReconnectIntervalForTest(20 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, stop := tr.SubscribeNodes(ctx)
	defer stop()

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].Agent.Health == model.AgentHealthVersionMismatch
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}
