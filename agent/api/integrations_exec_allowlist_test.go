package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execReq(program string, args ...string) integrationsExecRequest {
	return integrationsExecRequest{Program: program, Args: args, TimeoutMs: 30000}
}

func TestIntegrationExecAllowedAcceptsWhitelistedPair(t *testing.T) {
	home := t.TempDir()

	plan, err := integrationExecAllowed(home, execReq("openclaw", "mcp", "set", "superdev", "{}"))

	require.NoError(t, err)
	assert.Equal(t, "openclaw", plan.Program)
	assert.Equal(t, []string{"mcp", "set", "superdev", "{}"}, plan.Args)
	assert.Equal(t, 30*time.Second, plan.Timeout)
}

func TestIntegrationExecAllowedAcceptsGrokMcp(t *testing.T) {
	home := t.TempDir()

	_, err := integrationExecAllowed(home, execReq("grok", "mcp", "list", "--json"))

	require.NoError(t, err)
}

func TestIntegrationExecAllowedRejectsUnknownProgram(t *testing.T) {
	home := t.TempDir()

	_, err := integrationExecAllowed(home, execReq("bash", "mcp"))

	require.Error(t, err)
	assert.Equal(t, "program_not_allowed", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsUnknownSubcommand(t *testing.T) {
	home := t.TempDir()

	_, err := integrationExecAllowed(home, execReq("openclaw", "doctor", "--probe"))

	require.Error(t, err)
	assert.Equal(t, "subcommand_not_allowed", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsEmptyArgs(t *testing.T) {
	home := t.TempDir()

	_, err := integrationExecAllowed(home, execReq("openclaw"))

	require.Error(t, err)
	assert.Equal(t, "subcommand_not_allowed", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsTooManyArgs(t *testing.T) {
	home := t.TempDir()
	args := make([]string, 0, 20)
	args = append(args, "mcp")
	for i := 0; i < 19; i++ {
		args = append(args, "x")
	}

	_, err := integrationExecAllowed(home, execReq("openclaw", args...))

	require.Error(t, err)
	assert.Equal(t, "args_too_many", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsOversizedArg(t *testing.T) {
	home := t.TempDir()
	huge := strings.Repeat("x", integrationsExecMaxArgBytes+1)

	_, err := integrationExecAllowed(home, execReq("openclaw", "mcp", huge))

	require.Error(t, err)
	assert.Equal(t, "arg_too_long", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsUnknownEnvKey(t *testing.T) {
	home := t.TempDir()
	req := execReq("openclaw", "mcp", "set")
	req.Env = map[string]string{"PATH": "/tmp"}

	_, err := integrationExecAllowed(home, req)

	require.Error(t, err)
	assert.Equal(t, "env_key_not_allowed", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedRejectsEnvValueOutsidePathAllowlist(t *testing.T) {
	home := t.TempDir()
	req := execReq("openclaw", "mcp", "set")
	req.Env = map[string]string{"OPENCLAW_CONFIG_PATH": "/etc/passwd"}

	_, err := integrationExecAllowed(home, req)

	require.Error(t, err)
	assert.Equal(t, "env_value_not_allowed", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedNormalizesEnvValueThroughPathAllowlist(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".openclaw"), 0o755))
	raw := filepath.Join(home, ".openclaw", "openclaw.json")
	req := execReq("openclaw", "mcp", "set")
	req.Env = map[string]string{"OPENCLAW_CONFIG_PATH": raw}

	plan, err := integrationExecAllowed(home, req)

	require.NoError(t, err)
	expected, err := integrationPathAllowed(home, raw)
	require.NoError(t, err)
	assert.Equal(t, expected, plan.Env["OPENCLAW_CONFIG_PATH"],
		"落到子进程环境里的必须是白名单收敛后的路径，不是调用方声称的原始串")
}

func TestIntegrationExecAllowedClampsTimeout(t *testing.T) {
	home := t.TempDir()
	req := execReq("openclaw", "mcp")
	req.TimeoutMs = 999999

	_, err := integrationExecAllowed(home, req)

	require.Error(t, err)
	assert.Equal(t, "timeout_out_of_range", err.(integrationExecRejection).Code)
}

func TestIntegrationExecAllowedDefaultsTimeoutWhenZero(t *testing.T) {
	home := t.TempDir()
	req := execReq("openclaw", "mcp")
	req.TimeoutMs = 0

	plan, err := integrationExecAllowed(home, req)

	require.NoError(t, err)
	assert.Equal(t, integrationsExecDefaultTimeout, plan.Timeout)
}

// TestIntegrationsExecAllowlistMatchesDesktopFixture 跨栈校验：Go 白名单与桌面端
// 两家 CLI 连接器实际会发出的 (program, 子命令) 形状必须完全一致。
//
// 为什么必须有这条：白名单是第三份需要与桌面端同步的数据。前两份里，
// integrationConfigRoots 曾经只靠注释约定，漏掉 ~/.claude.json 直接让远端安装
// 必然 403。注释拦不住漂移，测试可以。
//
// fixture 的生成侧是**真的把连接器跑一遍**收出来的（见文件头注释），不是手抄
// 清单——手抄的话它不会因为 connectors/*.rs 改了 argv 而变化，本条照样绿，
// 漂移要到运行时 403 才暴露。
func TestIntegrationsExecAllowlistMatchesDesktopFixture(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "desktop-connector-commands.txt"))
	require.NoError(t, err,
		"fixture 由 Rust 侧 desktop_connector_commands_fixture_matches_what_the_connectors_actually_run 产出")

	expected := map[string]map[string]struct{}{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		require.Len(t, parts, 2, "每行必须是「program 子命令」两列：%s", line)
		if expected[parts[0]] == nil {
			expected[parts[0]] = map[string]struct{}{}
		}
		expected[parts[0]][parts[1]] = struct{}{}
	}

	// 清单被清空 / 只剩注释时，上面的 Equal 会退化成「白名单也得是空的」这种
	// 无意义比对，看起来仍像一条活着的跨栈校验。显式挡住。
	require.NotEmpty(t, expected, "跨栈清单不得为空，否则这条校验静默变成空转")

	assert.Equal(t, expected, integrationsExecAllowlist,
		"Go 白名单与桌面端连接器实际发出的命令形状不一致——两侧必须一起改")
}
