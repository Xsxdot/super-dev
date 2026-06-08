// direct_test.go 验证直连 NodeTransport 行为。
//
// 职责：
//   - 证明 direct transport 能按 Agent.Transport.Direct 地址发起 HTTP/WS 请求
//   - 证明 direct host 覆盖范围与状态订阅行为正确
//   - 证明旧 agent 缺少 /ws/node-status 时被归类为 version-mismatch
//
// 边界：
//   - 不测试 dispatcher 选择逻辑
//   - 不建立真实远端网络连接
package nodetransport_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestDirectTransportDoUsesConfiguredAddress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/exec/health", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true"})
	}))
	defer srv.Close()

	target := directTarget("h1", "direct", strings.TrimPrefix(srv.URL, "http://"))
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })
	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestDirectTransportDoIgnoresEnvironmentProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "proxy should not handle agent traffic", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	srvURL := newNonLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	target := directTarget("h1", "direct", u.Host)
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/security/health"})

	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDirectTransportStreamUsesWebSocketScheme(t *testing.T) {
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/ws/logs", r.URL.Path)
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON(map[string]string{"message": "hello"}))
	}))
	defer srv.Close()

	target := directTarget("h1", "direct", srv.URL)
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })
	stream, err := tr.Stream(context.Background(), "h1", nodetransport.NodeRequest{Path: "/ws/logs"})
	require.NoError(t, err)
	defer stream.Close()
	var got map[string]string
	require.NoError(t, stream.ReadJSON(&got))
	assert.Equal(t, "hello", got["message"])
}

func TestDirectTransportStreamIgnoresEnvironmentProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "proxy should not handle agent traffic", http.StatusServiceUnavailable)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	upgrader := websocket.Upgrader{}
	srvURL := newNonLoopbackHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.WriteJSON(map[string]string{"message": "direct"}))
	}))
	u, err := url.Parse(srvURL)
	require.NoError(t, err)
	target := directTarget("h1", "direct", u.Host)
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })

	stream, err := tr.Stream(context.Background(), "h1", nodetransport.NodeRequest{Path: "/ws/node-status"})

	require.NoError(t, err)
	defer stream.Close()
	var got map[string]string
	require.NoError(t, stream.ReadJSON(&got))
	assert.Equal(t, "direct", got["message"])
}

func TestDirectTransportCoversOnlyDirectHosts(t *testing.T) {
	direct := directTarget("h-direct", "direct", "100.64.0.8:57017")
	tunnelTarget := nodetransport.NodeTarget{
		Host: model.Host{ID: "h-tunnel"},
		Agent: model.Agent{HostID: "h-tunnel", Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeTunnel,
			Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
		}}}},
	}
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) {
		return []nodetransport.NodeTarget{tunnelTarget, direct}, nil
	})

	assert.Equal(t, []string{"h-direct"}, tr.Covers())
}

func TestDirectTransportSubscribeNodesReportsVersionMismatch(t *testing.T) {
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

	target := directTarget("h1", "old", srv.URL)
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })
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

func TestDirectTransportInjectsAgentToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer agent-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := directTarget("h1", "direct", srv.URL)
	target.Agent.Secret.Token = "agent-token"
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDirectTransportUsesCustomCACert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	target := directTarget("h1", "direct", strings.TrimPrefix(srv.URL, "https://"))
	target.Agent.Security.TLS = model.AgentTLSSpec{Mode: model.AgentTLSModeManual, CACert: caPEM}
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
}

func TestDirectTransportRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	target := directTarget("h1", "direct", srv.URL)
	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) { return []nodetransport.NodeTarget{target}, nil })
	tr.SetTimeoutsForTest(10*time.Millisecond, 10*time.Millisecond)

	_, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/api/exec/health"})
	require.Error(t, err)
	assert.Equal(t, nodetransport.CodeRequestTimeout, nodetransport.ErrorCode(err))
}

func TestDirectTransportUsesUnifiedTLSAndTokenFromAgent(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")

	tr := nodetransport.NewDirectTransport(func() ([]nodetransport.NodeTarget, error) {
		return []nodetransport.NodeTarget{{
			Host: model.Host{ID: "h1", Name: "ali"},
			Agent: model.Agent{
				HostID: "h1",
				Transport: model.TransportConfig{Chain: []model.TransportEntry{{
					Type:   model.TransportTypeDirect,
					Direct: &model.DirectParams{Address: address},
				}}},
				Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeOff}},
				Secret:   model.AgentSecret{Token: "tok"},
			},
		}}, nil
	})

	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Path: "/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer tok", gotAuth)
}

func directTarget(id, name, address string) nodetransport.NodeTarget {
	return nodetransport.NodeTarget{
		Host: model.Host{
			ID:   id,
			Name: name,
		},
		Agent: model.Agent{HostID: id, Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeOff}}, Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeDirect,
			Direct: &model.DirectParams{Address: address},
		}}}},
	}
}

func newNonLoopbackHTTPServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	ip := firstNonLoopbackIPv4(t)
	ln, err := net.Listen("tcp4", "0.0.0.0:0")
	require.NoError(t, err)
	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = server.Close()
	})
	_, port, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)
	return "http://" + net.JoinHostPort(ip, port)
}

func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	ifaces, err := net.Interfaces()
	require.NoError(t, err)
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		require.NoError(t, err)
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip = ip.To4(); ip != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	t.Skip("no non-loopback IPv4 address available")
	return ""
}
