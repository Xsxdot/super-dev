// handler_integrations_detect_test.go 覆盖 POST /api/integrations/detect。
//
// 职责：
//   - 验证成功路径三样事实：CLI 存在性 map、home 非空、agent 自身 launch spec
//   - 验证命令名白名单校验拒绝非法输入（含长度上限）
//   - 验证匿名请求被 withSecurity 拦在 401（本端点不进 bypass 白名单）
//
// 边界：
//   - 不覆盖 Task 4 的受限文件端点、Task 5 的跨机代理，那些不属于本文件范围
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type integrationsDetectResponseForTest struct {
	Home     string          `json:"home"`
	Commands map[string]bool `json:"commands"`
	Agent    struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		URL     string   `json:"url"`
	} `json:"agent"`
}

// TestIntegrationsDetectReturnsCommandPresenceHomeAndLaunchSpec 覆盖 brief Step 1
// 列出的成功路径断言：go 一定存在、虚构命令一定不存在、home 非空、agent launch
// spec 的 args 固定为 ["mcp"]（Task 1 的 stdio MCP 分派入口）、url 以本机
// loopback 为前缀。
func TestIntegrationsDetectReturnsCommandPresenceHomeAndLaunchSpec(t *testing.T) {
	app := newTestAppForPackage(t)

	body := bytes.NewBufferString(`{"commands":["go","definitely-not-a-cli-xyz"]}`)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", body)
	require.Equal(t, http.StatusOK, resp.Code)

	var got integrationsDetectResponseForTest
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))

	require.True(t, got.Commands["go"], "go 应该在测试/CI 环境中存在于 PATH")
	require.False(t, got.Commands["definitely-not-a-cli-xyz"], "虚构命令不应存在")
	require.NotEmpty(t, got.Home, "home 目录必须非空")
	require.Equal(t, []string{"mcp"}, got.Agent.Args, "launch spec 子命令固定为 mcp（Task 1 分派入口）")
	require.True(t, strings.HasPrefix(got.Agent.URL, "http://127.0.0.1:"), "url 必须以本机 loopback http 前缀开头，got=%q", got.Agent.URL)
	require.NotEmpty(t, got.Agent.Command, "command 必须是解析后的可执行文件路径")
}

// TestIntegrationsDetectEmptyCommandsStillReturnsHomeAndAgent 覆盖 commands 为空
// 数组时仍能拿到 home 与 launch spec——detect 的 home/agent 两项事实不依赖
// commands 参数。
func TestIntegrationsDetectEmptyCommandsStillReturnsHomeAndAgent(t *testing.T) {
	app := newTestAppForPackage(t)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", bytes.NewBufferString(`{"commands":[]}`))
	require.Equal(t, http.StatusOK, resp.Code)

	var got integrationsDetectResponseForTest
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	require.NotEmpty(t, got.Home)
	require.Equal(t, []string{"mcp"}, got.Agent.Args)
	require.Empty(t, got.Commands)
}

// TestIntegrationsDetectRejectsInvalidCommandNames 覆盖命令名正则
// ^[a-z0-9][a-z0-9-]{0,63}$ 的拒绝路径：带空格、大写字母都必须 400——虽然
// exec.LookPath 本身不会执行任意字符串，但仍需要白名单化防止意外输入。
func TestIntegrationsDetectRejectsInvalidCommandNames(t *testing.T) {
	app := newTestAppForPackage(t)

	cases := []struct {
		name    string
		command string
	}{
		{name: "包含空格", command: "a b"},
		{name: "包含大写字母", command: "Go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string][]string{"commands": {tc.command}})
			require.NoError(t, err)
			resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", bytes.NewReader(payload))
			require.Equal(t, http.StatusBadRequest, resp.Code, "非法命令名必须 400: %q", tc.command)
		})
	}
}

// TestIntegrationsDetectRejectsTooManyCommands 覆盖 32 个上限：33 个必须 400。
func TestIntegrationsDetectRejectsTooManyCommands(t *testing.T) {
	app := newTestAppForPackage(t)

	commands := make([]string, 33)
	for i := range commands {
		commands[i] = "go"
	}
	payload, err := json.Marshal(map[string][]string{"commands": commands})
	require.NoError(t, err)

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", bytes.NewReader(payload))
	require.Equal(t, http.StatusBadRequest, resp.Code, "超过 32 个命令必须 400")
}

// TestIntegrationsDetectRejectsAnonymousRequest 覆盖鉴权红线：本端点绝不进
// securityBypassPath，匿名请求必须 401，而不是因为请求体校验先失败而巧合返回
// 400——这里用一个合法请求体，确保 401 是鉴权中间件本身产生的。
func TestIntegrationsDetectRejectsAnonymousRequest(t *testing.T) {
	app := newTestAppForPackage(t)

	resp := httptestDoWithHeader(t, app, http.MethodPost, "/api/integrations/detect",
		bytes.NewBufferString(`{"commands":["go"]}`),
		map[string]string{"Authorization": ""},
	)
	require.Equal(t, http.StatusUnauthorized, resp.Code, "匿名请求必须 401")
	require.Contains(t, resp.Body.String(), "agent token required")
}

// TestAgentSelfLaunchSpecUsesActualListenPort 直接验证 agentSelfLaunchSpec 在
// Serve 已写入 listenAddr 后，url 里的端口取自真实监听地址，而不是巧合落在
// 回退值上——httptestDo 系列 helper 从不调用 Serve，上面的 HTTP 层测试因此
// 只覆盖了回退路径，这里补上真实路径。
func TestAgentSelfLaunchSpecUsesActualListenPort(t *testing.T) {
	app := newTestAppForPackage(t)

	app.setListenAddr("127.0.0.1:54321")
	spec, err := app.agentSelfLaunchSpec()
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:54321", spec.URL)
	require.Equal(t, []string{"mcp"}, spec.Args)
	require.NotEmpty(t, spec.Command)
}

// TestListenPortFallsBackToDefaultWhenUnset 验证 Serve 尚未写入 listenAddr
// （或写入了无法解析出该端口的地址）时，listenPort 回退到 agent 默认端口
// 57017，与 agent/mcp.ResolveStdioAgentURL 的默认值保持一致。
func TestListenPortFallsBackToDefaultWhenUnset(t *testing.T) {
	app := newTestAppForPackage(t)

	require.Equal(t, "57017", app.listenPort(), "未调用 Serve 时应回退默认端口")
}

// writeFakeCLI 在 dir 下造一个可执行文件，返回它的绝对路径。
func writeFakeCLI(t *testing.T, dir, name string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

// TestIntegrationsDetectFindsCommandsOutsidePath 是本轮真机验证照出来的缺陷的
// 回归测试。
//
// 现象：目标机上确实装了 claude / codex，接入面板却一律报「未检测到 CLI」。
// 根因：agent 在目标机上由 launchd/systemd 拉起，拿到的是最小 PATH（那台 mac
// 上实测 `/usr/bin:/bin:/usr/sbin:/sbin`），而 CLI 装在 `~/.local/bin`；detect
// 当时只有 `exec.LookPath` 一条路，于是「装了」被一律报成「没装」。
//
// 桌面端本机侧从来没这个问题，因为它的 command_search_dirs 在 PATH 之外还扫一份
// 兜底目录清单——GUI 应用拿到的同样是最小 PATH，本机侧当初正是为此才加的。远端
// 侧必须扫同一份清单，否则「同一台机器、本机装得出、远端装不出」。
//
// 本测试把 PATH 收缩成 launchd 那种最小集，逐个目录验证兜底扫描真的生效。
func TestIntegrationsDetectFindsCommandsOutsidePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 复刻目标机上 launchd 给 agent 的真实 PATH：不含任何用户级目录。
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	writeFakeCLI(t, filepath.Join(home, ".local", "bin"), "claude")
	writeFakeCLI(t, filepath.Join(home, ".npm-global", "bin"), "codex")
	writeFakeCLI(t, filepath.Join(home, ".bun", "bin"), "kimi")
	writeFakeCLI(t, filepath.Join(home, ".cargo", "bin"), "hermes")
	writeFakeCLI(t, filepath.Join(home, ".opencode", "bin"), "opencode")

	app := newTestAppForPackage(t)
	body := bytes.NewBufferString(`{"commands":["claude","codex","kimi","hermes","opencode","definitely-not-a-cli-xyz"]}`)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", body)
	require.Equal(t, http.StatusOK, resp.Code)

	var got integrationsDetectResponseForTest
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))

	for _, name := range []string{"claude", "codex", "kimi", "hermes", "opencode"} {
		require.True(t, got.Commands[name],
			"%s 装在 PATH 之外的用户级目录里，detect 必须扫到它——只查 PATH 会把「装了」报成「没装」", name)
	}
	require.False(t, got.Commands["definitely-not-a-cli-xyz"],
		"兜底扫描不能把不存在的命令也报成存在")
}

// TestIntegrationsDetectIgnoresDirectoriesNamedLikeCommands 钉住兜底扫描只认
// 普通文件：`~/.local/bin/claude` 若是个目录，它不是一个可执行的 CLI，报成
// 存在会让后续安装写出一份指向不存在命令的配置。
func TestIntegrationsDetectIgnoresDirectoriesNamedLikeCommands(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

	require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "bin", "claude"), 0o755))

	app := newTestAppForPackage(t)
	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect",
		bytes.NewBufferString(`{"commands":["claude"]}`))
	require.Equal(t, http.StatusOK, resp.Code)

	var got integrationsDetectResponseForTest
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	require.False(t, got.Commands["claude"], "同名目录不是 CLI，不能报成已安装")
}

// TestIntegrationsDetectResolvesHomeWhenHomeEnvEmpty 是 F1 真机 500 的盲区闸门。
//
// 现象：systemd 不给 agent 设 HOME，detect 直调 os.UserHomeDir 返回
// "$HOME is not defined"，端点 500。现有测试要么 HOME 本来就有值，要么走
// integrationsHomeOverride，真实解析路径结构性地走不到。
//
// 本测试清空 HOME、且不设 override，强制走 hostpaths.UserHome 的 passwd 回落。
func TestIntegrationsDetectResolvesHomeWhenHomeEnvEmpty(t *testing.T) {
	t.Setenv("HOME", "")

	app := newTestAppForPackage(t)
	require.Empty(t, app.integrationsHomeOverride, "本测试必须走真实 home 解析，不能用 override 绕开")

	resp := httptestDo(t, app, http.MethodPost, "/api/integrations/detect", bytes.NewBufferString(`{"commands":[]}`))
	require.Equal(t, http.StatusOK, resp.Code, "HOME 为空时 detect 必须回落到 passwd，不能 500。body=%s", resp.Body.String())

	var got integrationsDetectResponseForTest
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &got))
	require.NotEmpty(t, got.Home)
	info, err := os.Stat(got.Home)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "detect 返回的 home 必须是已存在的目录，got=%q", got.Home)
}
