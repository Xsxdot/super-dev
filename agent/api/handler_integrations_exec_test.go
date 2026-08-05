// handler_integrations_exec_test.go 覆盖受限命令执行端点 POST /api/integrations/exec。
//
// 职责：
//   - 验证白名单内 program/subcommand 可执行，stdout/exit_code/timed_out 契约正确
//   - 验证 env 白名单透传、program/subcommand 拒绝、命令缺失 404
//   - 验证 CLI 非零退出仍为 HTTP 200、超时标记 timed_out
//
// 边界：
//   - 不覆盖跨机代理（proxyAgentIntegrations 通配已覆盖 /exec）
//   - 假 CLI 全部种在 integrationsHomeOverride 的兜底目录下；测试内清空 PATH，
//     避免本机真实 openclaw/grok 抢在 LookPath 里被解析到
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// isolateIntegrationExecPath 把 PATH 清空到不存在的目录，迫使 integrationCommandResolve
// 走 home 下的兜底目录，而不是本机已安装的同名 CLI。
func isolateIntegrationExecPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", filepath.FromSlash("/nonexistent-dir-for-test"))
}

// execTestApp 造一个 home 被重定向到临时目录、且 PATH 隔离后的 App，并在
// ~/.local/bin 下种一个假 openclaw：把 argv 与关心的 env 打到 stdout。
func execTestApp(t *testing.T) (*App, string) {
	t.Helper()
	isolateIntegrationExecPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	script := "#!/bin/sh\necho \"argv:$*\"\necho \"cfg:$OPENCLAW_CONFIG_PATH\"\necho \"home:$HOME\"\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "openclaw"), []byte(script), 0o755))

	app := newTestAppForPackage(t)
	app.integrationsHomeOverride = home
	return app, home
}

func execBody(t *testing.T, body any) *bytes.Reader {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	return bytes.NewReader(raw)
}

func TestIntegrationsExecRunsWhitelistedCommand(t *testing.T) {
	app, _ := execTestApp(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "openclaw",
		"args":    []string{"mcp", "set", "superdev", "{}"},
	}))

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var got struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		TimedOut bool   `json:"timed_out"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.Equal(t, 0, got.ExitCode)
	assert.False(t, got.TimedOut)
	assert.Contains(t, got.Stdout, "argv:mcp set superdev {}")
}

func TestIntegrationsExecPassesWhitelistedEnv(t *testing.T) {
	app, home := execTestApp(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".openclaw"), 0o755))
	cfg := filepath.Join(home, ".openclaw", "openclaw.json")
	// 白名单会经 integrationPathAllowed 收敛路径（macOS 上 /var → /private/var），
	// 子进程实际看到的是收敛后的值，断言必须用同一条路径。
	resolvedCfg, err := integrationPathAllowed(home, cfg)
	require.NoError(t, err)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "openclaw",
		"args":    []string{"mcp", "show", "superdev"},
		"env":     map[string]string{"OPENCLAW_CONFIG_PATH": cfg},
	}))

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var got struct {
		Stdout string `json:"stdout"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.Contains(t, got.Stdout, "cfg:"+resolvedCfg)
}

// TestIntegrationsExecChildPathIncludesCommandFallbackDirs 钉死子进程 PATH 必须
// 带上命令兜底目录。
//
// 复现的是真机上会发生的事：agent 由 launchd 拉起、PATH 是最小集，
// integrationCommandResolve 靠兜底目录把 openclaw 的绝对路径解析出来了，但
// **被调用的 CLI 自己还要在 PATH 上找解释器**——npm 系 CLI 的入口是
// `#!/usr/bin/env node` 脚本，node 装在用户级目录里，最小 PATH 下一律 exit 127，
// 于是「解析得到、跑不起来」。这里用一个只存在于兜底目录的 helper 代替 node，
// 不依赖本机是否装了 Node。
func TestIntegrationsExecChildPathIncludesCommandFallbackDirs(t *testing.T) {
	isolateIntegrationExecPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	// helper 只在兜底目录里，不在 agent 自己的 PATH 上。
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "superdev-fake-helper"),
		[]byte("#!/bin/sh\necho helper-ran\n"), 0o755))
	// CLI 裸名调用 helper：PATH 上找不到就是 exit 127（修复前的行为）。
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "openclaw"),
		[]byte("#!/bin/sh\nsuperdev-fake-helper\n"), 0o755))
	app := newTestAppForPackage(t)
	app.integrationsHomeOverride = home

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "openclaw",
		"args":    []string{"mcp", "show"},
	}))

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var got struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.Equal(t, 0, got.ExitCode,
		"子进程 PATH 少了兜底目录 → CLI 找不到自己的解释器/子进程 → 127；stderr=%s", got.Stderr)
	assert.Contains(t, got.Stdout, "helper-ran")
}

// TestIntegrationsExecChildHomeMatchesTheAllowlistHome 钉死子进程 HOME 与路径
// 白名单判定用的 home 是同一个值——两者取不同来源的话，CLI 会按一个 home 写
// 配置、白名单按另一个 home 判定。
func TestIntegrationsExecChildHomeMatchesTheAllowlistHome(t *testing.T) {
	app, home := execTestApp(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "openclaw",
		"args":    []string{"mcp", "show"},
	}))

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var got struct {
		Stdout string `json:"stdout"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.Contains(t, got.Stdout, "home:"+home,
		"子进程 HOME 必须是 integrationsHome() 解析出的那个，而不是 agent 进程自己的 $HOME")
}

// TestIntegrationsExecChildPathKeepsAgentPathEntriesFirst 钉死「只补不删」：
// agent 自身 PATH 的目录必须原样保留且优先级在前，补的兜底目录追加在后。
func TestIntegrationsExecChildPathKeepsAgentPathEntriesFirst(t *testing.T) {
	t.Setenv("PATH", strings.Join(
		[]string{filepath.FromSlash("/agent-path-a"), filepath.FromSlash("/usr/bin")},
		string(os.PathListSeparator)))
	home := filepath.FromSlash("/home/tester")

	dirs := filepath.SplitList(integrationsExecChildPath(home))

	require.NotEmpty(t, dirs)
	assert.Equal(t, filepath.FromSlash("/agent-path-a"), dirs[0], "agent 自身 PATH 排在最前")
	assert.Contains(t, dirs, filepath.Join(home, ".local", "bin"), "兜底目录必须被补上")
	// /usr/bin 同时出现在 agent PATH 与兜底清单里，去重后只能有一条。
	count := 0
	for _, dir := range dirs {
		if dir == filepath.FromSlash("/usr/bin") {
			count++
		}
	}
	assert.Equal(t, 1, count, "重复目录必须去重")
}

func TestIntegrationsExecRejectsNonWhitelistedProgram(t *testing.T) {
	app, _ := execTestApp(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "sh",
		"args":    []string{"mcp"},
	}))

	assert.Equal(t, http.StatusForbidden, resp.Code)
	assert.Contains(t, resp.Body.String(), "program_not_allowed")
}

func TestIntegrationsExecRejectsNonWhitelistedSubcommand(t *testing.T) {
	app, _ := execTestApp(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "openclaw",
		"args":    []string{"doctor"},
	}))

	assert.Equal(t, http.StatusForbidden, resp.Code)
	assert.Contains(t, resp.Body.String(), "subcommand_not_allowed")
}

func TestIntegrationsExecReportsMissingCommand(t *testing.T) {
	isolateIntegrationExecPath(t)
	app := newTestAppForPackage(t)
	app.integrationsHomeOverride = t.TempDir()

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "grok",
		"args":    []string{"mcp", "list"},
	}))

	assert.Equal(t, http.StatusNotFound, resp.Code)
	assert.Contains(t, resp.Body.String(), "command_not_found")
}

func TestIntegrationsExecReturnsNonZeroExitWithoutHttpError(t *testing.T) {
	isolateIntegrationExecPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "grok"),
		[]byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o755))
	app := newTestAppForPackage(t)
	app.integrationsHomeOverride = home

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program": "grok",
		"args":    []string{"mcp", "list"},
	}))

	require.Equal(t, http.StatusOK, resp.Code, "CLI 返回非零不是 HTTP 错误，调用方要拿到 exit_code 自己判断")
	var got struct {
		ExitCode int    `json:"exit_code"`
		Stderr   string `json:"stderr"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.Equal(t, 3, got.ExitCode)
	assert.Contains(t, got.Stderr, "boom")
}

func TestIntegrationsExecTimesOut(t *testing.T) {
	isolateIntegrationExecPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	// sleep 必须用绝对路径：子进程 PATH = agent 自身 PATH（这里被隔离成一个不存在
	// 的目录）+ integrationCommandSearchDirs 的兜底目录，而 /bin 不在那份清单里，
	// 裸 sleep 仍会 exit 127 而不是超时。
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "grok"),
		[]byte("#!/bin/sh\n/bin/sleep 5\n"), 0o755))
	app := newTestAppForPackage(t)
	app.integrationsHomeOverride = home

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/exec", execBody(t, map[string]any{
		"program":    "grok",
		"args":       []string{"mcp", "list"},
		"timeout_ms": 300,
	}))

	require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
	var got struct {
		TimedOut bool `json:"timed_out"`
	}
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	assert.True(t, got.TimedOut)
}
