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
