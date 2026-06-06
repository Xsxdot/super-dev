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

type fakeDialer struct{ port int }

func (f fakeDialer) Dial(host model.Host) (*tunnel.Conn, error) {
	return tunnel.NewFakeConn(f.port), nil
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
	_, err := mgr.EnsureConnected(model.Host{ID: "h1"})
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) {
		h := model.Host{ID: "h1"}
		h.EnsureTunnelAgent()
		return []model.Host{h}, nil
	})
	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
		Query:  url.Values{"deployment": []string{"dep-1"}},
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestTunnelTransportDoReturnsUnreachableWhenNoLocalPort(t *testing.T) {
	mgr := tunnel.NewManager(fakeDialer{port: 0})
	defer mgr.Close()

	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) { return nil, nil })
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
	_, err := mgr.EnsureConnected(model.Host{ID: "h1"})
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) { return nil, nil })
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
	tunnelHost := model.Host{ID: "h-tunnel"}
	tunnelHost.EnsureTunnelAgent()
	directHost := model.Host{ID: "h-direct", Agent: &model.Agent{
		Transport: model.TransportConfig{Type: model.TransportTypeDirect},
	}}
	tr := nodetransport.NewTunnelTransport(tunnel.NewManager(fakeDialer{}), func() ([]model.Host, error) {
		return []model.Host{tunnelHost, directHost}, nil
	})

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

	host := model.Host{ID: "h1", Name: "ali-01"}
	host.EnsureTunnelAgent()
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	_, err := mgr.EnsureConnected(host)
	require.NoError(t, err)
	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) {
		return []model.Host{host}, nil
	})
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

	connected := model.Host{ID: "h1", Name: "connected"}
	connected.EnsureTunnelAgent()
	missing := model.Host{ID: "h2", Name: "missing"}
	missing.EnsureTunnelAgent()

	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	_, err := mgr.EnsureConnected(connected)
	require.NoError(t, err)

	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) {
		return []model.Host{connected, missing}, nil
	})
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

	host := model.Host{ID: "h1", Name: "ali-01"}
	host.EnsureTunnelAgent()
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) { return []model.Host{host}, nil })

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
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

	host := model.Host{ID: "h1", Name: "ali-01"}
	host.EnsureTunnelAgent()
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) { return []model.Host{host}, nil })
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

	host := model.Host{ID: "h1", Name: "old-agent"}
	host.EnsureTunnelAgent()
	mgr := tunnel.NewManager(fakeDialer{port: serverPort(t, srv.URL)})
	defer mgr.Close()
	tr := nodetransport.NewTunnelTransport(mgr, func() ([]model.Host, error) { return []model.Host{host}, nil })
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
