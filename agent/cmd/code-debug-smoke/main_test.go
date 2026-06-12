// main_test.go 验证 code-debug-smoke 命令的 HTTP 协议细节。
//
// 职责：
//   - 锁定 keep-runtime smoke 路径的 close 请求体
//   - 避免命令退回旧的 DELETE close 协议
//
// 边界：
//   - 不启动真实 agent
//   - 不连接真实 DAP adapter
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloseDebugSessionKeepsRuntimeWhenRequested(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/code-debug-sessions/cds_1/close", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "cds_1"})
	}))
	t.Cleanup(srv.Close)

	err := closeDebugSession(srv.Client(), srv.URL, "cds_1", true)

	require.NoError(t, err)
	assert.Equal(t, false, body["stop_runtime"])
}
