// Package mcp 验证 MCP 到本机 agent 的 HTTP client。
//
// 职责：
//   - 验证 agent REST API 路径与响应解码
//   - 验证非 2xx 响应会映射为明确错误
//
// 边界：
//   - 不启动真实 SuperDev agent
//   - 不测试 MCP tool 业务逻辑
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestHTTPAgentClientListProjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/projects", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]model.Project{{ID: "p1", Name: "demo"}})
	}))
	defer srv.Close()

	client := NewHTTPAgentClient(srv.URL, srv.Client())
	projects, err := client.ListProjects(context.Background())

	require.NoError(t, err)
	require.Len(t, projects, 1)
	assert.Equal(t, "demo", projects[0].Name)
}

func TestHTTPAgentClientMapsAgentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "deployment is read-only"})
	}))
	defer srv.Close()

	client := NewHTTPAgentClient(srv.URL, srv.Client())
	err := client.StartDeployment(context.Background(), "dep-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "deployment is read-only")
}

func TestHTTPAgentClientDebugSessionLifecycle(t *testing.T) {
	mux := http.NewServeMux()
	var createPath string
	var appendPath string
	mux.HandleFunc("POST /api/debug-sessions", func(w http.ResponseWriter, r *http.Request) {
		createPath = r.URL.Path
		var req DebugSessionCreateRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "p1", req.ProjectID)
		jsonOKForMCPClientTest(w, DebugSessionCreateResponse{
			Session: DebugSession{ID: "dbg_1", ProjectID: "p1", Status: "open"},
			Event:   DebugSessionEvent{ID: "ev_1", SessionID: "dbg_1", Type: "status_change"},
		})
	})
	mux.HandleFunc("POST /api/debug-sessions/dbg_1/events", func(w http.ResponseWriter, r *http.Request) {
		appendPath = r.URL.Path
		var req DebugSessionAppendEventRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "observation", req.Type)
		jsonOKForMCPClientTest(w, DebugSessionEvent{ID: "ev_2", SessionID: "dbg_1", Type: "observation"})
	})
	mux.HandleFunc("GET /api/debug-sessions/dbg_1", func(w http.ResponseWriter, r *http.Request) {
		jsonOKForMCPClientTest(w, DebugSessionDetailResponse{
			Session: DebugSession{ID: "dbg_1", ProjectID: "p1", Status: "open"},
			Events:  []DebugSessionEvent{{ID: "ev_1"}, {ID: "ev_2"}},
			Count:   2,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := NewHTTPAgentClient(srv.URL, srv.Client())

	created, err := client.CreateDebugSession(context.Background(), DebugSessionCreateRequest{ProjectID: "p1", Title: "t", Question: "q"})
	require.NoError(t, err)
	assert.Equal(t, "dbg_1", created.Session.ID)
	assert.Equal(t, "/api/debug-sessions", createPath)

	event, err := client.AppendDebugSessionEvent(context.Background(), "dbg_1", DebugSessionAppendEventRequest{
		Type:    "observation",
		Actor:   "assistant",
		Summary: "found evidence",
	})
	require.NoError(t, err)
	assert.Equal(t, "ev_2", event.ID)
	assert.Equal(t, "/api/debug-sessions/dbg_1/events", appendPath)

	detail, err := client.GetDebugSession(context.Background(), "dbg_1", 20)
	require.NoError(t, err)
	assert.Len(t, detail.Events, 2)
}

func jsonOKForMCPClientTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
