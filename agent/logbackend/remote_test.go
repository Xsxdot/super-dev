// Package logbackend_test 验证 RemoteAgentBackend。
package logbackend_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type mockNodeTransport struct {
	baseURL string
	wsURL   string
	err     error
}

func (m *mockNodeTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if m.err != nil {
		return nodetransport.NodeResponse{}, m.err
	}
	u, err := url.Parse(m.baseURL + req.Path)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	q := u.Query()
	for key, values := range req.Query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, u.String(), req.Body)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	for key, values := range req.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	return nodetransport.NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

func (m *mockNodeTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	if m.err != nil {
		return nil, m.err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, m.wsURL+req.Path+"?"+req.Query.Encode(), req.Headers)
	return conn, err
}

func (m *mockNodeTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (m *mockNodeTransport) Covers() []string {
	return []string{"host-1"}
}

func TestRemoteAgentBackend_QueryReturnsEntries(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	entries := []model.LogEntry{
		{ID: 1, DeploymentID: "svc-1", Timestamp: now, Message: "hello", Stream: "stdout"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/logs", r.URL.Path)
		assert.Equal(t, "svc-1", r.URL.Query().Get("deployment"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{baseURL: srv.URL})
	got, next, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "hello", got[0].Message)
	assert.Equal(t, "1", next.ID)
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
			Items:   []model.LogEntry{{ID: 1, DeploymentID: "svc-1", Timestamp: now, Message: "error occurred", Stream: "stderr"}},
			Total:   1,
			HasMore: false,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{baseURL: srv.URL})
	got, _, hasMore, err := b.Search(context.Background(), logbackend.SearchQuery{
		DeploymentIDs: []string{"svc-1"},
		Text:          "error",
		Limit:         10,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, got, 1)
	assert.Equal(t, "error occurred", got[0].Message)
}

func TestRemoteAgentBackend_ContextReturnsDeploymentEntries(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/logs/context", r.URL.Path)
		assert.Equal(t, "svc-1", r.URL.Query().Get("deployment"))
		assert.Equal(t, "42", r.URL.Query().Get("id"))
		assert.Equal(t, "1000", r.URL.Query().Get("before_ms"))
		assert.Equal(t, "2000", r.URL.Query().Get("after_ms"))
		resp := struct {
			TargetID          int64                       `json:"target_id"`
			AnchorTime        time.Time                   `json:"anchor_time"`
			ItemsByDeployment map[string][]model.LogEntry `json:"items_by_deployment"`
		}{
			TargetID:   42,
			AnchorTime: now,
			ItemsByDeployment: map[string][]model.LogEntry{
				"svc-1": {{ID: 42, DeploymentID: "svc-1", Timestamp: now, Message: "target", Stream: "stderr"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{baseURL: srv.URL})
	got, err := b.Context(context.Background(), logbackend.ContextQuery{
		TargetID: 42,
		Before:   time.Second,
		After:    2 * time.Second,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), got.TargetID)
	assert.Equal(t, now, got.AnchorTime)
	require.Len(t, got.Items, 1)
	assert.Equal(t, "target", got.Items[0].Message)
}

func TestRemoteAgentBackend_ContextFallsBackToLogsForOldRemote(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	var sawContext bool
	var sawLogs bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/logs/context":
			sawContext = true
			http.Error(w, "project is required", http.StatusBadRequest)
		case "/api/logs":
			sawLogs = true
			assert.Equal(t, "svc-1", r.URL.Query().Get("deployment"))
			assert.NotEmpty(t, r.URL.Query().Get("before"))
			entries := []model.LogEntry{
				{ID: 41, DeploymentID: "svc-1", Timestamp: now.Add(-500 * time.Millisecond), Message: "before"},
				{ID: 42, DeploymentID: "svc-1", Timestamp: now, Message: "target"},
				{ID: 43, DeploymentID: "svc-1", Timestamp: now.Add(500 * time.Millisecond), Message: "after"},
				{ID: 44, DeploymentID: "svc-1", Timestamp: now.Add(3 * time.Second), Message: "outside"},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(entries)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{baseURL: srv.URL})
	got, err := b.Context(context.Background(), logbackend.ContextQuery{
		TargetID: 42,
		Before:   time.Second,
		After:    time.Second,
	})
	require.NoError(t, err)
	assert.True(t, sawContext)
	assert.True(t, sawLogs)
	assert.Equal(t, int64(42), got.TargetID)
	assert.Equal(t, now, got.AnchorTime)
	require.Len(t, got.Items, 3)
	assert.Equal(t, []int64{41, 42, 43}, []int64{got.Items[0].ID, got.Items[1].ID, got.Items[2].ID})
}

func TestRemoteAgentBackend_ContextPageUsesRemoteContextPageAPI(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/logs/context/page", r.URL.Path)
		assert.Equal(t, "svc-1", r.URL.Query().Get("deployment"))
		assert.Equal(t, "before", r.URL.Query().Get("direction"))
		assert.Equal(t, now.Format(time.RFC3339Nano), r.URL.Query().Get("cursor_time"))
		assert.Equal(t, "42", r.URL.Query().Get("cursor_id"))
		assert.Equal(t, "2", r.URL.Query().Get("limit"))
		resp := struct {
			DeploymentID string                          `json:"deployment_id"`
			Direction    logbackend.ContextPageDirection `json:"direction"`
			Items        []model.LogEntry                `json:"items"`
			HasMore      bool                            `json:"has_more"`
		}{
			DeploymentID: "svc-1",
			Direction:    logbackend.ContextPageBefore,
			Items: []model.LogEntry{
				{ID: 40, DeploymentID: "svc-1", Timestamp: now.Add(-2 * time.Second), Message: "older"},
				{ID: 41, DeploymentID: "svc-1", Timestamp: now.Add(-time.Second), Message: "near"},
			},
			HasMore: true,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{baseURL: srv.URL})
	got, err := b.ContextPage(context.Background(), logbackend.ContextPageQuery{
		DeploymentID: "dep-center",
		Cursor:       logbackend.Cursor{Time: now, ID: "42"},
		Direction:    logbackend.ContextPageBefore,
		Limit:        2,
	})
	require.NoError(t, err)
	assert.True(t, got.HasMore)
	assert.Equal(t, []string{"older", "near"}, []string{got.Entries[0].Message, got.Entries[1].Message})
}

func TestRemoteAgentBackend_SubscribeReceivesLiveEntries(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	entry := model.LogEntry{ID: 1, DeploymentID: "svc-1", Timestamp: now, Message: "live", Stream: "stdout"}

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
	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{wsURL: wsURL})

	stream := b.Subscribe(context.Background(), logbackend.SubscribeOptions{DeploymentID: "svc-1"})
	defer stream.Cancel()

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "live", got.Message)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for live entry")
	}
}

func TestRemoteAgentBackend_QueryTunnelError(t *testing.T) {
	b := logbackend.NewRemoteAgentBackend("host-1", "svc-1", &mockNodeTransport{err: nodetransport.ErrHostUnreachable})
	_, _, err := b.Query(context.Background(), logbackend.QueryFilter{})
	assert.Error(t, err)
}
