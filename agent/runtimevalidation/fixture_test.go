// fixture_test.go 验证七语言 fixture schema 的平台、动态端口和断点合同。
//
// 职责：
//   - 锁定 Go、Node.js、Python、Java、Kotlin、Rust、C++ 的完整集合
//   - 拒绝固定端口、缺平台命令、缺 readiness 或缺 debug variables
//
// 边界：
//   - 不调用工具链，不构建源码，也不启动语言服务
package runtimevalidation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateFixtureRequiresCrossPlatformDynamicPortAndDebugContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*Fixture)
	}{
		{name: "fixed port", mutate: func(fixture *Fixture) { fixture.Runtime.Env["FIXTURE_PORT"] = "18190" }},
		{name: "missing linux", mutate: func(fixture *Fixture) { delete(fixture.Platforms, "linux") }},
		{name: "missing debug variables", mutate: func(fixture *Fixture) { fixture.Debug.ExpectedVariables = nil }},
		{name: "missing readiness", mutate: func(fixture *Fixture) { fixture.Readiness.Type = "" }},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := validFixture("go")
			test.mutate(&fixture)
			require.Error(t, ValidateFixture(fixture))
		})
	}
}

func TestRepositoryRuntimeFixturesAndScenariosSatisfySharedContracts(t *testing.T) {
	t.Parallel()

	fixtures, err := LoadFixtures(filepath.Join("..", "..", "validation", "runtime", "fixtures"))
	require.NoError(t, err)
	require.Len(t, fixtures, 7)
	for _, fixture := range fixtures {
		sourcePath := filepath.Join("..", "..", "validation", "runtime", "fixtures", fixture.Provider, filepath.FromSlash(fixture.Debug.Source))
		raw, err := os.ReadFile(sourcePath)
		require.NoError(t, err, fixture.Provider)
		lines := strings.Split(string(raw), "\n")
		require.LessOrEqual(t, fixture.Debug.Line, len(lines), fixture.Provider)
		require.Contains(t, lines[fixture.Debug.Line-1], fixture.Debug.Marker, fixture.Provider)
	}

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	assignments, err := PrimaryAssignments(scenarios)
	require.NoError(t, err)
	require.NotEmpty(t, assignments)
	for _, assignment := range assignments {
		require.NotEmpty(t, assignment.Tool)
	}
}

func TestCPPFixtureEnablesImmediateManagedRestartOnSamePort(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "validation", "runtime", "fixtures", "cpp", "src", "main.cpp"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "SO_REUSEADDR")
	require.Contains(t, string(raw), "setsockopt")
}

func TestKotlinBreakpointMarkerIsRetainedInObservableBytecode(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "validation", "runtime", "fixtures", "kotlin", "src", "FixtureServer.kt"))
	require.NoError(t, err)
	require.Contains(t, string(raw), "val markerLength = fixtureMarker.length // SUPERDEV_FIXTURE_BREAKPOINT")
	require.Contains(t, string(raw), `\"marker_length\":$markerLength`)
}

func TestLoadFixturesRequiresExactlySevenProviders(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	providers := []string{"go", "node", "python", "java", "kotlin", "rust", "cpp"}
	for _, provider := range providers {
		dir := filepath.Join(root, provider)
		require.NoError(t, os.MkdirAll(dir, 0o700))
		raw, err := json.Marshal(validFixture(provider))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "fixture.json"), raw, 0o600))
	}

	fixtures, err := LoadFixtures(root)
	require.NoError(t, err)
	require.Len(t, fixtures, 7)
	require.Equal(t, "cpp", fixtures[0].Provider)
	require.Equal(t, "rust", fixtures[6].Provider)

	require.NoError(t, os.Remove(filepath.Join(root, "kotlin", "fixture.json")))
	_, err = LoadFixtures(root)
	require.ErrorContains(t, err, "missing")
}

func validFixture(provider string) Fixture {
	platform := FixturePlatform{
		Preflight:  CommandSpec{Executable: "tool", Arguments: []string{"--version"}},
		Build:      CommandSpec{Executable: "tool", Arguments: []string{"build"}},
		Run:        CommandSpec{Executable: "tool", Arguments: []string{"run", "${PORT}"}},
		Executable: "build/fixture",
		Probes: []ArtifactProbe{
			{Type: "file", Path: "build/fixture"},
			{Type: "tcp", PortVariable: "FIXTURE_PORT"},
		},
	}
	return Fixture{
		SchemaVersion: FixtureSchemaVersion,
		Kind:          FixtureKind,
		ID:            provider,
		Provider:      provider,
		WorkingDir:    ".",
		Runtime: FixtureRuntime{
			Config: map[string]any{"program": "build/fixture"},
			Env:    map[string]string{"FIXTURE_PORT": "${PORT}", "FIXTURE_CAMPAIGN_ID": "${CAMPAIGN_ID}"},
		},
		Platforms:            map[string]FixturePlatform{"windows": platform, "darwin": platform, "linux": platform},
		Readiness:            HTTPProbe{Type: "http", Method: "GET", Path: "/healthz", ExpectedStatus: 200},
		NormalProbe:          HTTPProbe{Type: "http", Method: "POST", Path: "/api/probe", ExpectedStatus: 200},
		ControlledErrorProbe: HTTPProbe{Type: "http", Method: "POST", Path: "/api/probe?mode=error", ExpectedStatus: 500},
		Debug: DebugContract{
			Provider: provider, Source: "main.txt", Line: 10, Marker: "SUPERDEV_FIXTURE_BREAKPOINT",
			ExpectedVariables: map[string]any{"fixture_marker": "breakpoint-visible", "fixture_count": 42},
		},
	}
}
