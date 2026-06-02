// Package mcp 验证 MCP stdio 到 fake agent 的端到端链路。
//
// 职责：
//   - 验证 stdio JSON-RPC 可调用真实工具
//   - 验证 stdout 不包含普通日志
//
// 边界：
//   - 使用 fake HTTP agent
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestMCPStdioListProjectsAgainstFakeAgent(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/projects":
			_ = json.NewEncoder(w).Encode([]model.Project{{ID: "p1", Name: "demo"}})
		case "/api/services":
			_ = json.NewEncoder(w).Encode([]model.Service{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer agent.Close()

	client := NewHTTPAgentClient(agent.URL, agent.Client())
	server := NewServer(client)
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_projects","arguments":{}}}`,
		"",
	}, "\n")
	var output bytes.Buffer

	require.NoError(t, server.RunStdio(context.Background(), strings.NewReader(input), &output))
	assert.Contains(t, output.String(), `"demo"`)
	assert.NotContains(t, output.String(), "SuperDev agent listening")
}
