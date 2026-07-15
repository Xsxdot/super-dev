// mcp_process_test.go 验证真实 stdio JSON-RPC 会话的 initialize、tools/list 和 tools/call。
//
// 职责：
//   - 锁定持久 MCP 子进程与完整 ToolCallResult 解析
//   - 证明协议 stdout 只在内存 framing，不写入 evidence sink
//
// 边界：
//   - helper 不实现 SuperDev 行为，也不能产生 strict target PASS
package runtimevalidation

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMCPProcessRunsPersistentProtocolSession(t *testing.T) {
	t.Parallel()

	executable, err := os.Executable()
	require.NoError(t, err)
	client, err := StartMCPProcess(context.Background(), MCPProcessSpec{
		Executable: executable,
		Arguments:  []string{"-test.run=TestMCPProcessHelper"},
		Env:        map[string]string{"GO_WANT_MCP_HELPER": "1"},
		AgentURL:   "http://127.0.0.1:57099",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	initialized, err := client.Initialize(ctx)
	require.NoError(t, err)
	require.Equal(t, "superdev-mcp-helper", initialized.ServerInfo.Name)
	tools, err := client.ListTools(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"list_projects"}, tools)
	result, err := client.CallTool(ctx, "list_projects", map[string]any{})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, true, RawMessageMap(result.StructuredContent)["ok"])
	require.NoError(t, client.Close(ctx))
}

func TestMCPProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") == "" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(3)
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		var result any
		switch request["method"] {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-11-25",
				"serverInfo":      map[string]any{"name": "superdev-mcp-helper", "version": "test"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "list_projects"}}}
		case "tools/call":
			result = map[string]any{"isError": false, "structuredContent": map[string]any{"ok": true}}
		default:
			_ = encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32601, "message": "unknown method"}})
			continue
		}
		if encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}) != nil {
			os.Exit(4)
		}
	}
}
