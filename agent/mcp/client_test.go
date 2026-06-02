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
