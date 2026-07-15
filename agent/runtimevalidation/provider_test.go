// provider_test.go 验证七语言共享同一 runtime/debug phase machine 和已捕获 project_id。
//
// 职责：
//   - 锁定 provider 差异只来自 fixture/platform adapter
//   - 锁定 runtime/debug 分开呈现，MCP 调用默认 supporting
//   - 禁止 provider matrix 自行注册 project 或绕过 MCP
//
// 边界：
//   - fake 只验证 orchestration seam，不能产生 strict target PASS
package runtimevalidation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderMatrixRunsSevenLanguagesAgainstCapturedProject(t *testing.T) {
	t.Parallel()

	tools := &fakeProviderTools{}
	runner := NewProviderRunner(tools, fakeProviderCommands{}, fakeProviderHTTP{})
	fixtures := make([]Fixture, 0, 7)
	for _, provider := range []string{"go", "node", "python", "java", "kotlin", "rust", "cpp"} {
		fixture := validFixture(provider)
		fixture.Debug.Provider = map[string]string{"java": "jvm", "kotlin": "jvm", "rust": "native", "cpp": "native"}[provider]
		if fixture.Debug.Provider == "" {
			fixture.Debug.Provider = provider
		}
		fixtures = append(fixtures, fixture)
	}

	results := runner.RunMatrix(context.Background(), ProviderMatrixRequest{
		CampaignID: "campaign-1", ProjectID: "project-from-manifest-bootstrap", ProjectRoot: "/tmp/runtime-validation-project",
		Platform: "darwin", Fixtures: fixtures, Ports: map[string]int{
			"go": 20101, "node": 20102, "python": 20103, "java": 20104, "kotlin": 20105, "rust": 20106, "cpp": 20107,
		},
	})

	require.Len(t, results, 7)
	for _, result := range results {
		require.Equal(t, StatusPass, result.RuntimeStatus, result.Provider)
		require.Equal(t, StatusPass, result.DebugStatus, result.Provider)
		require.Len(t, result.Phases, len(ProviderPhaseOrder), result.Provider)
	}
	for _, call := range tools.calls {
		switch call.name {
		case "preview_config_change", "apply_config_change", "start_service", "stop_service":
			require.Equal(t, "project-from-manifest-bootstrap", call.arguments["project_id"], call.name)
		}
		require.NotEqual(t, "upsert_project_config", call.name)
	}
}

func TestProviderRuntimeFailureLeavesDebugNotRunWithNamedUpstream(t *testing.T) {
	t.Parallel()

	tools := &fakeProviderTools{failTool: "start_service"}
	runner := NewProviderRunner(tools, fakeProviderCommands{}, fakeProviderHTTP{})
	result := runner.Run(context.Background(), ProviderRequest{
		CampaignID: "campaign-1", ProjectID: "project-1", ProjectRoot: "/tmp/project",
		Platform: "darwin", Fixture: validFixture("go"), Port: 20101,
	})

	require.Equal(t, StatusFail, result.RuntimeStatus)
	require.Equal(t, StatusNotRun, result.DebugStatus)
	require.Equal(t, "runtime.start", result.DebugCause.Source)
}

func TestProviderServiceConfigUsesPreflightedAdapterCommand(t *testing.T) {
	t.Parallel()

	fixture := validFixture("go")
	request := ProviderRequest{Fixture: fixture, Port: 20101, AdapterPath: "/prepared/adapters/dlv"}
	service := providerServiceConfig(request, fixture.Platforms["darwin"])
	deployments := service["deployments"].([]any)
	deployment := deployments[0].(map[string]any)
	codeDebug := deployment["code_debug"].(map[string]any)

	require.Equal(t, "/prepared/adapters/dlv", codeDebug["adapter_command"])
}

type providerCall struct {
	name      string
	arguments map[string]any
}

type fakeProviderTools struct {
	calls    []providerCall
	failTool string
}

func (f *fakeProviderTools) CallTool(_ context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	f.calls = append(f.calls, providerCall{name: name, arguments: arguments})
	if name == f.failTool {
		return ToolCallResult{}, fmt.Errorf("forced %s failure", name)
	}
	data := map[string]any{"ok": true}
	switch name {
	case "describe_language_runtime_schema":
		data = map[string]any{"ok": true, "data": map[string]any{"schema": map[string]any{"language": arguments["language"], "fields": []any{"program"}}}}
	case "validate_service_runtime":
		data = map[string]any{"ok": true, "data": map[string]any{"valid": true}}
	case "preview_config_change", "apply_config_change":
		data = map[string]any{"ok": true, "data": map[string]any{"preview": map[string]any{"validation": map[string]any{"ok": true}}}}
	case "start_service":
		data = map[string]any{"ok": true, "data": map[string]any{"action": "start"}}
	case "stop_service":
		data = map[string]any{"ok": true, "data": map[string]any{"action": "stop"}}
	case "debug_capture_at":
		variables := []any{}
		for _, name := range arguments["variable_names"].([]string) {
			value := any("breakpoint-visible")
			if name == "fixture_count" {
				value = 42
			}
			variables = append(variables, map[string]any{"name": name, "value": value})
		}
		data = map[string]any{"ok": true, "data": map[string]any{"session_id": "debug-1", "thread_id": 1, "frame_id": 1, "variables": variables}}
	}
	return ToolCallResult{StructuredContent: data}, nil
}

type fakeProviderCommands struct{}

func (fakeProviderCommands) Run(context.Context, CommandRunRequest) error { return nil }

type fakeProviderHTTP struct{}

func (fakeProviderHTTP) Probe(context.Context, HTTPProbeRequest) error { return nil }
