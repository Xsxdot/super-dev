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
