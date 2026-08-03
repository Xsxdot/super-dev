// handler_mcp_setup_test.go 验证归属节点 claude-code MCP 最小配置端点。
//
// 职责：
//   - 验证空 HOME、已有其他 mcpServers 条目、二次调用幂等、非法 JSON 拒绝写入
//     四类核心场景，均直接读写真实临时文件系统（t.TempDir()），不 mock 文件 I/O
//   - 验证 Task 5 调用方带 {project_id,root_path} body 时端点仍正常返回 200
//
// 边界：
//   - 不验证 skill 目录/hook 安装（不在本端点职责内）
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// withMCPSetupHomeDir 把 mcpSetupHomeDir seam 指向一个隔离的临时目录，并在
// 测试结束时还原，避免污染同包内其他测试（该 var 是包级全局状态）。
func withMCPSetupHomeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig := mcpSetupHomeDir
	mcpSetupHomeDir = dir
	t.Cleanup(func() { mcpSetupHomeDir = orig })
	return dir
}

func expectedSuperdevAgentURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", model.DefaultAgentListenPort)
}

// TestMCPSetupClaudeCode_EmptyHomeCreatesFile 验证空 HOME 下调用会创建
// ~/.claude.json 并写入含正确 SUPERDEV_AGENT_URL 的 superdev 条目。
func TestMCPSetupClaudeCode_EmptyHomeCreatesFile(t *testing.T) {
	home := withMCPSetupHomeDir(t)
	app := newTestAppForPackage(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", nil)
	require.Equal(t, http.StatusOK, resp.Code)

	var out mcpSetupResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	wantPath := filepath.Join(home, ".claude.json")
	assert.Equal(t, "installed", out.Status)
	assert.Equal(t, wantPath, out.Path)

	data, err := os.ReadFile(wantPath)
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	servers, ok := doc["mcpServers"].(map[string]any)
	require.True(t, ok, "mcpServers 应为对象")
	entry, ok := servers["superdev"].(map[string]any)
	require.True(t, ok, "superdev 条目应存在")
	assert.Equal(t, "superdev-mcp", entry["command"])
	env, ok := entry["env"].(map[string]any)
	require.True(t, ok, "env 应为对象")
	assert.Equal(t, expectedSuperdevAgentURL(), env["SUPERDEV_AGENT_URL"])
}

// TestMCPSetupClaudeCode_MergesWithExistingEntries 验证已有 .claude.json（含其他
// mcpServers 条目和其他顶层键）时，合并后原有键全部保留，且新增 superdev 条目。
func TestMCPSetupClaudeCode_MergesWithExistingEntries(t *testing.T) {
	home := withMCPSetupHomeDir(t)
	app := newTestAppForPackage(t)

	existing := `{
  "otherTopLevelKey": "keep-me",
  "mcpServers": {
    "other-connector": {"command": "other-cmd", "env": {"FOO": "bar"}}
  }
}`
	require.NoError(t, os.WriteFile(filepath.Join(home, ".claude.json"), []byte(existing), 0o644))

	resp := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", nil)
	require.Equal(t, http.StatusOK, resp.Code)

	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))

	assert.Equal(t, "keep-me", doc["otherTopLevelKey"], "非 mcpServers 的其他顶层键必须原样保留")
	servers, ok := doc["mcpServers"].(map[string]any)
	require.True(t, ok)

	other, ok := servers["other-connector"].(map[string]any)
	require.True(t, ok, "既有的其他 connector 条目必须保留")
	assert.Equal(t, "other-cmd", other["command"])

	entry, ok := servers["superdev"].(map[string]any)
	require.True(t, ok, "superdev 条目应新增")
	assert.Equal(t, "superdev-mcp", entry["command"])
}

// TestMCPSetupClaudeCode_SecondCallIsIdempotent 验证二次调用不产生重复条目，
// 且两次写入结果字节级一致（收敛到同一个稳定结果，而非累加或漂移）。
func TestMCPSetupClaudeCode_SecondCallIsIdempotent(t *testing.T) {
	home := withMCPSetupHomeDir(t)
	app := newTestAppForPackage(t)
	path := filepath.Join(home, ".claude.json")

	resp1 := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", nil)
	require.Equal(t, http.StatusOK, resp1.Code)
	first, err := os.ReadFile(path)
	require.NoError(t, err)

	resp2 := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", nil)
	require.Equal(t, http.StatusOK, resp2.Code)
	second, err := os.ReadFile(path)
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second), "二次调用应收敛到完全相同的文件内容")

	var doc map[string]any
	require.NoError(t, json.Unmarshal(second, &doc))
	servers := doc["mcpServers"].(map[string]any)
	assert.Len(t, servers, 1, "不应出现重复的 superdev 条目")
}

// TestMCPSetupClaudeCode_IllegalJSONRejected 验证既有 .claude.json 内容非法 JSON
// 时端点返回 400，且绝不覆盖——文件字节必须与调用前完全一致。
func TestMCPSetupClaudeCode_IllegalJSONRejected(t *testing.T) {
	home := withMCPSetupHomeDir(t)
	app := newTestAppForPackage(t)
	path := filepath.Join(home, ".claude.json")

	illegal := []byte(`{"mcpServers": { not valid json`)
	require.NoError(t, os.WriteFile(path, illegal, 0o644))

	resp := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", nil)
	require.Equal(t, http.StatusBadRequest, resp.Code)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, illegal, after, "非法 JSON 时文件必须原样保留，绝不被骨架覆盖")
}

// TestMCPSetupClaudeCode_ToleratesRequestBody 验证 Task 5 转移引擎实际会带的
// {project_id,root_path} body 不会导致 400——本端点忽略 body 内容。
func TestMCPSetupClaudeCode_ToleratesRequestBody(t *testing.T) {
	withMCPSetupHomeDir(t)
	app := newTestAppForPackage(t)

	body := []byte(`{"project_id":"proj-1","root_path":"/home/user/proj"}`)
	resp := httptestDo(t, app, http.MethodPost, "/api/mcp-setup/claude-code", bytes.NewReader(body))
	require.Equal(t, http.StatusOK, resp.Code)

	var out mcpSetupResponse
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &out))
	assert.Equal(t, "installed", out.Status)
}
