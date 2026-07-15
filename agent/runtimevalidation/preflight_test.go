// preflight_test.go 验证 active marker 前的外部依赖与 borrowed topology gate。
//
// 职责：
//   - 锁定七语言、adapter、浏览器、bundle 调试资源、端口和远端治理标签全集
//   - 锁定缺任一外部依赖都返回 BLOCKED
//
// 边界：
//   - 不执行真实工具链、不启动 Agent/MCP，也不创建 active marker
package runtimevalidation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunReadOnlyPreflightPassesCompleteNativeDependencySet(t *testing.T) {
	bundleRoot, input := createPreflightEnvironment(t)
	executor := &recordingPreflightCommands{}

	result := RunReadOnlyPreflight(context.Background(), bundleRoot, input, Target{OS: "linux", Architecture: "amd64"}, executor)

	require.Equal(t, StatusPass, result.Status, result.Cause)
	require.Equal(t, 7, executor.calls)
	require.NoFileExists(t, filepath.Join(FoundationStateRoot(input.FoundationPath, input.ProfileID), activeMarkerFilename))
}

func TestRunReadOnlyPreflightBlocksBeforeMarkerWhenAdapterIsMissing(t *testing.T) {
	bundleRoot, input := createPreflightEnvironment(t)
	require.NoError(t, os.Remove(input.Adapters["dlv"]))

	result := RunReadOnlyPreflight(context.Background(), bundleRoot, input, Target{OS: "linux", Architecture: "amd64"}, &recordingPreflightCommands{})

	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "debug_adapter_unavailable", result.Cause.Code)
	require.NoFileExists(t, filepath.Join(FoundationStateRoot(input.FoundationPath, input.ProfileID), activeMarkerFilename))
}

func TestRunReadOnlyPreflightRequiresProductValidGraceWithoutUsingIt(t *testing.T) {
	bundleRoot, input := createPreflightEnvironment(t)
	var settings map[string]any
	require.NoError(t, readJSONFile(filepath.Join(input.FoundationPath, "settings.json"), &settings))
	settings["approval"].(map[string]any)["grace_minutes"] = 0
	writeJSONFile(t, filepath.Join(input.FoundationPath, "settings.json"), settings)

	result := RunReadOnlyPreflight(context.Background(), bundleRoot, input, Target{OS: "linux", Architecture: "amd64"}, &recordingPreflightCommands{})

	require.Equal(t, StatusBlocked, result.Status)
	require.Equal(t, "browser_or_debug_policy_unavailable", result.Cause.Code)
}

type recordingPreflightCommands struct {
	calls int
}

func (r *recordingPreflightCommands) Run(context.Context, CommandRunRequest) error {
	r.calls++
	return nil
}

func createPreflightEnvironment(t *testing.T) (string, RuntimeInput) {
	t.Helper()
	bundleRoot := t.TempDir()
	fixtureRoot := filepath.Join(bundleRoot, "validation", "fixtures")
	for _, provider := range requiredFixtureProviders {
		fixture := validFixture(provider)
		fixture.Debug.AdapterResource = map[string]string{
			"go": "dlv", "node": "resources/js-debug", "python": "debugpy",
			"java": "jvm-dap-wrapper", "kotlin": "jvm-dap-wrapper", "rust": "lldb-dap", "cpp": "lldb-dap",
		}[provider]
		directory := filepath.Join(fixtureRoot, provider)
		require.NoError(t, os.MkdirAll(directory, 0o700))
		writeJSONFile(t, filepath.Join(directory, "fixture.json"), fixture)
	}
	scenarioRoot := filepath.Join(bundleRoot, "validation", "scenarios")
	require.NoError(t, os.MkdirAll(scenarioRoot, 0o700))
	writeJSONFile(t, filepath.Join(scenarioRoot, "identity.json"), validScenario("list_projects"))

	jsDebug := filepath.Join(bundleRoot, "resources", "js-debug", "src", "dapDebugServer.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(jsDebug), 0o700))
	require.NoError(t, os.WriteFile(jsDebug, []byte("// validation js-debug\n"), 0o600))
	for path, content := range map[string]string{
		filepath.Join(bundleRoot, "resources", "playwright-driver", "node", "node"):      "native node",
		filepath.Join(bundleRoot, "resources", "playwright-driver", "package", "cli.js"): "driver package",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o700))
	}

	foundation := createValidFoundation(t)
	browser := filepath.Join(t.TempDir(), "chromium")
	require.NoError(t, os.WriteFile(browser, []byte("native browser"), 0o700))
	writeJSONFile(t, filepath.Join(foundation, "settings.json"), map[string]any{
		"approval": map[string]any{
			"config_upsert": true, "pipeline_upsert": true, "pipeline_run": true, "template_import": true,
			"browser_debug_open": true, "code_debug_open": true, "code_debug_evaluate": true, "grace_minutes": 15,
		},
		"debug_browser": map[string]any{
			"default_browser_id": "validation-chromium", "profile_mode": "ephemeral", "allow_evaluate": true,
			"browsers": []any{map[string]any{"id": "validation-chromium", "executable_path": browser}},
		},
	})
	writeJSONFile(t, filepath.Join(foundation, "hosts.json"), []any{map[string]any{
		"id": "remote-linux", "is_self": false, "tags": []string{dedicatedRemoteHostTag},
	}})
	writeJSONFile(t, filepath.Join(foundation, "agents.json"), []any{map[string]any{
		"host_id": "remote-linux", "transport": map[string]any{"chain": []any{map[string]any{"type": "direct"}}},
	}})

	adapterRoot := t.TempDir()
	adapters := map[string]string{}
	for _, name := range []string{"dlv", "debugpy", "jvm-dap-wrapper", "lldb-dap"} {
		path := filepath.Join(adapterRoot, name)
		require.NoError(t, os.WriteFile(path, []byte("native adapter"), 0o700))
		adapters[name] = path
	}
	governance := filepath.Join(t.TempDir(), "governance.json")
	require.NoError(t, os.WriteFile(governance, []byte(`{}`), 0o600))
	return bundleRoot, RuntimeInput{
		FoundationPath: foundation, ProfileID: "profile-1", RemoteHostID: "remote-linux", ExpectedRemoteIdentity: "node-remote-linux",
		GovernanceAttestationPath: governance, RemoteRootTemplate: "/srv/superdev-runtime-validation/{campaign_id}",
		ResultsRoot: t.TempDir(), Adapters: adapters,
	}
}
