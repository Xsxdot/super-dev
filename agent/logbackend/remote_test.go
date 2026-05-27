// Package logbackend_test 验证 RemoteAgentBackend。
package logbackend_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

// mockTunnelResolver 实现 logbackend.TunnelResolver 接口，返回固定 baseURL。
type mockTunnelResolver struct {
	baseURL string
}

func (m *mockTunnelResolver) BaseURL(hostID string) (string, error) {
	return m.baseURL, nil
}

func TestRemoteAgentBackend_QueryReturnsEntries(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	entries := []model.LogEntry{
		{ID: 1, ServiceID: "svc-1", Timestamp: now, Message: "hello", Stream: "stdout"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/logs", r.URL.Path)
		assert.Equal(t, "svc-1", r.URL.Query().Get("service"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockTunnelResolver{baseURL: srv.URL})
	got, next, err := b.Query(context.Background(), logbackend.QueryFilter{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "hello", got[0].Message)
	assert.Equal(t, int64(1), next.ID)
}

func TestRemoteAgentBackend_SearchReturnsMatches(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/log-search", r.URL.Path)
		assert.Equal(t, "error", r.URL.Query().Get("q"))
		resp := struct {
			Items   []model.LogEntry `json:"items"`
			Total   int              `json:"total"`
			HasMore bool             `json:"has_more"`
		}{
			Items:   []model.LogEntry{{ID: 1, ServiceID: "svc-1", Timestamp: now, Message: "error occurred", Stream: "stderr"}},
			Total:   1,
			HasMore: false,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockTunnelResolver{baseURL: srv.URL})
	got, _, hasMore, err := b.Search(context.Background(), logbackend.SearchQuery{
		ServiceIDs: []string{"svc-1"},
		Text:       "error",
		Limit:      10,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, got, 1)
	assert.Equal(t, "error occurred", got[0].Message)
}

func TestRemoteAgentBackend_SubscribeReceivesLiveEntries(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	entry := model.LogEntry{ID: 1, ServiceID: "svc-1", Timestamp: now, Message: "live", Stream: "stdout"}

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(entry)
		// 等待客户端断开
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	// httptest.Server 是 http://，WebSocket 需要 ws://
	wsURL := "ws" + srv.URL[4:]
	b := logbackend.NewRemoteAgentBackendWithWSURL("host-1", "svc-1", &mockTunnelResolver{baseURL: srv.URL}, wsURL)

	stream := b.Subscribe(context.Background(), "svc-1")
	defer stream.Cancel()

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "live", got.Message)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for live entry")
	}
}

func TestRemoteAgentBackend_QueryTunnelError(t *testing.T) {
	resolver := &mockTunnelResolver{baseURL: ""}
	// baseURL 为空，HTTP 请求必然失败
	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", resolver)
	_, _, err := b.Query(context.Background(), logbackend.QueryFilter{})
	assert.Error(t, err)
}
