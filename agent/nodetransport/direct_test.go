// direct_test.go 验证直连 NodeTransport 行为。
//
// 职责：
//   - 证明 direct transport 能按 Host.Agent.Transport.Direct 地址发起 HTTP/WS 请求
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
	"net/http"
	"net/http/httptest"
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

	host := directHost("h1", "direct", strings.TrimPrefix(srv.URL, "http://"))
	tr := nodetransport.NewDirectTransport(func() ([]model.Host, error) { return []model.Host{host}, nil })
	resp, err := tr.Do(context.Background(), "h1", nodetransport.NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
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

	host := directHost("h1", "direct", srv.URL)
	tr := nodetransport.NewDirectTransport(func() ([]model.Host, error) { return []model.Host{host}, nil })
	stream, err := tr.Stream(context.Background(), "h1", nodetransport.NodeRequest{Path: "/ws/logs"})
	require.NoError(t, err)
	defer stream.Close()
	var got map[string]string
	require.NoError(t, stream.ReadJSON(&got))
	assert.Equal(t, "hello", got["message"])
}

func TestDirectTransportCoversOnlyDirectHosts(t *testing.T) {
	direct := directHost("h-direct", "direct", "100.64.0.8:57017")
	tunnelHost := model.Host{ID: "h-tunnel"}
	tunnelHost.EnsureTunnelAgent()
	tr := nodetransport.NewDirectTransport(func() ([]model.Host, error) {
		return []model.Host{tunnelHost, direct}, nil
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

	host := directHost("h1", "old", srv.URL)
	tr := nodetransport.NewDirectTransport(func() ([]model.Host, error) { return []model.Host{host}, nil })
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

func directHost(id, name, address string) model.Host {
	return model.Host{
		ID:   id,
		Name: name,
		Agent: &model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeDirect,
			Direct: &model.DirectParams{Address: address, TLS: false},
		}}}},
	}
}
