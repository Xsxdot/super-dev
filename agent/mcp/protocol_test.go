// Package mcp 验证 SuperDev MCP 协议壳。
//
// 职责：
//   - 验证 initialize、tools/list、tools/call 基础协议
//   - 验证工具业务错误通过 CallToolResult.isError 返回
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不测试具体业务工具逻辑
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolInitializeAndListTools(t *testing.T) {
	server := NewServer(nil)

	initResp := server.Handle(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}`),
	})

	require.Nil(t, initResp.Error)
	assert.Equal(t, "2025-11-25", initResp.Result["protocolVersion"])

	listResp := server.Handle(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
	})
	require.Nil(t, listResp.Error)
	tools, ok := listResp.Result["tools"].([]Tool)
	require.True(t, ok)
	assert.NotEmpty(t, tools)
}

func TestProtocolUnknownToolReturnsProtocolError(t *testing.T) {
	server := NewServer(nil)

	resp := server.Handle(context.Background(), rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{"name":"missing_tool","arguments":{}}`),
	})

	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "unknown tool")
}

func TestRunStdioWritesOnlyJSONRPCToStdout(t *testing.T) {
	server := NewServer(nil)
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}` + "\n")
	var output bytes.Buffer

	err := server.RunStdio(context.Background(), input, &output)

	require.NoError(t, err)
	assert.Contains(t, output.String(), `"jsonrpc":"2.0"`)
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var resp rpcResponse
		require.NoError(t, json.Unmarshal([]byte(line), &resp))
		assert.Equal(t, "2.0", resp.JSONRPC)
	}
}
