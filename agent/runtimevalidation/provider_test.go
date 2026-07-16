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
	"path/filepath"
	"sync"
	"testing"
	"time"

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

func TestProviderNodeServiceConfigSeparatesLauncherFromBundledScript(t *testing.T) {
	t.Parallel()

	fixture := validFixture("node")
	request := ProviderRequest{
		Fixture: fixture, Port: 20101,
		AdapterPath: "/bundle/js-debug/src/dapDebugServer.js", AdapterCommand: "/toolchain/bin/node",
	}
	service := providerServiceConfig(request, fixture.Platforms["darwin"])
	deployments := service["deployments"].([]any)
	deployment := deployments[0].(map[string]any)
	codeDebug := deployment["code_debug"].(map[string]any)

	require.Equal(t, "/toolchain/bin/node", codeDebug["adapter_command"])
	require.NotEqual(t, request.AdapterPath, codeDebug["adapter_command"])
}

func TestProviderNodeAcceptsJSDebugZeroThreadIdentity(t *testing.T) {
	t.Parallel()

	tools := &fakeProviderTools{}
	runner := NewProviderRunner(tools, fakeProviderCommands{}, fakeProviderHTTP{})
	result := runner.Run(context.Background(), ProviderRequest{
		CampaignID: "campaign-1", ProjectID: "project-1", ProjectRoot: t.TempDir(),
		Platform: "darwin", Fixture: validFixture("node"), Port: 20101,
	})

	require.Equal(t, StatusPass, result.DebugStatus)
	for _, call := range tools.calls {
		if call.name == "debug_capture_at" {
			require.NotContains(t, call.arguments, "thread_id")
			return
		}
	}
	t.Fatal("debug_capture_at was not called")
}

func TestValidateDebugVariablesAcceptsQuotedDAPStrings(t *testing.T) {
	t.Parallel()

	for _, value := range []string{`"breakpoint-visible"`, `'breakpoint-visible'`, "breakpoint-visible"} {
		require.NoError(t, validateDebugVariables([]any{
			map[string]any{"name": "fixture_marker", "value": value},
		}, map[string]any{"fixture_marker": "breakpoint-visible"}), value)
	}
}

func TestToolApplicationErrorPreservesStructuredCause(t *testing.T) {
	t.Parallel()

	err := toolApplicationError("debug_capture_at", ToolCallResult{IsError: true, StructuredContent: map[string]any{
		"ok": false, "code": "adapter_handshake_failed", "message": "failed to attach target",
		"data": map[string]any{"cause_code": "connection_refused"},
	}})

	require.ErrorContains(t, err, "adapter_handshake_failed")
	require.ErrorContains(t, err, "failed to attach target")
	require.ErrorContains(t, err, "connection_refused")
}

func TestProviderGoUsesCampaignOwnedBuildCacheAcrossBuildRuntimeAndDebug(t *testing.T) {
	t.Parallel()

	projectRoot := t.TempDir()
	tools := &fakeProviderTools{}
	commands := &recordingProviderCommands{}
	runner := NewProviderRunner(tools, commands, fakeProviderHTTP{})
	result := runner.Run(context.Background(), ProviderRequest{
		CampaignID: "campaign-1", ProjectID: "project-1", ProjectRoot: projectRoot,
		Platform: "darwin", Fixture: validFixture("go"), Port: 20101,
	})

	require.Equal(t, StatusPass, result.RuntimeStatus)
	require.Equal(t, StatusPass, result.DebugStatus)
	require.Len(t, commands.requests, 2)
	cacheRoot := filepath.Join(projectRoot, ".runtime-validation-cache", "go-build")
	for _, request := range commands.requests {
		require.Equal(t, cacheRoot, request.Env["GOCACHE"], request.Name)
	}
	require.DirExists(t, cacheRoot)

	var validatedEnv map[string]string
	var configuredEnv map[string]string
	for _, call := range tools.calls {
		switch call.name {
		case "validate_service_runtime":
			validatedEnv = call.arguments["env"].(map[string]string)
		case "preview_config_change":
			service := call.arguments["service"].(map[string]any)
			deployment := service["deployments"].([]any)[0].(map[string]any)
			runtime := deployment["runtime"].(map[string]any)
			configuredEnv = runtime["env"].(map[string]string)
		}
	}
	require.Equal(t, cacheRoot, validatedEnv["GOCACHE"])
	require.Equal(t, cacheRoot, configuredEnv["GOCACHE"])
}

func TestProviderWaitsForAsynchronousRuntimeReadiness(t *testing.T) {
	t.Parallel()

	tools := &fakeProviderTools{}
	http := &transientReadinessProviderHTTP{remainingFailures: 2, resetFailures: 2}
	runner := NewProviderRunner(tools, fakeProviderCommands{}, http)
	result := runner.Run(context.Background(), ProviderRequest{
		CampaignID: "campaign-1", ProjectID: "project-1", ProjectRoot: t.TempDir(),
		Platform: "darwin", Fixture: validFixture("cpp"), Port: 20101,
	})

	require.Equal(t, StatusPass, result.RuntimeStatus)
	require.Equal(t, StatusPass, result.DebugStatus)
	require.Equal(t, 6, http.readinessAttempts())
}

func TestProviderRetriesDebugTriggerUntilBreakpointIsArmed(t *testing.T) {
	t.Parallel()

	breakpointTriggered := make(chan struct{})
	tools := &delayedDebugProviderTools{breakpointTriggered: breakpointTriggered}
	http := &repeatedDebugTriggerHTTP{breakpointTriggered: breakpointTriggered, requiredAttempts: 3}
	runner := NewProviderRunner(tools, fakeProviderCommands{}, http)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := runner.Run(ctx, ProviderRequest{
		CampaignID: "campaign-1", ProjectID: "project-1", ProjectRoot: t.TempDir(),
		Platform: "darwin", Fixture: validFixture("python"), Port: 20101,
	})

	require.Equal(t, StatusPass, result.RuntimeStatus)
	require.Equal(t, StatusPass, result.DebugStatus)
	require.GreaterOrEqual(t, http.debugTriggerAttempts(), 3)
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
		threadID := 1
		if deploymentID, _ := arguments["deployment_id"].(string); deploymentID == providerDeploymentID("node") {
			// js-debug 的 reverse-request child session 使用 0 作为合法 thread identity。
			threadID = 0
		}
		data = map[string]any{"ok": true, "data": map[string]any{"session_id": "debug-1", "thread_id": threadID, "frame_id": 1, "variables": variables}}
	}
	return ToolCallResult{StructuredContent: data}, nil
}

type fakeProviderCommands struct{}

func (fakeProviderCommands) Run(context.Context, CommandRunRequest) error { return nil }

type recordingProviderCommands struct {
	requests []CommandRunRequest
}

func (r *recordingProviderCommands) Run(_ context.Context, request CommandRunRequest) error {
	r.requests = append(r.requests, request)
	return nil
}

type fakeProviderHTTP struct{}

func (fakeProviderHTTP) Probe(context.Context, HTTPProbeRequest) error { return nil }

type transientReadinessProviderHTTP struct {
	mu                sync.Mutex
	remainingFailures int
	resetFailures     int
	attempts          int
}

func (p *transientReadinessProviderHTTP) Probe(_ context.Context, request HTTPProbeRequest) error {
	if request.Probe.Path == "/api/probe?mode=error" {
		p.mu.Lock()
		p.remainingFailures = p.resetFailures
		p.mu.Unlock()
		return nil
	}
	if request.Probe.Path != "/healthz" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.attempts++
	if p.remainingFailures > 0 {
		p.remainingFailures--
		return fmt.Errorf("runtime is still starting")
	}
	return nil
}

func (p *transientReadinessProviderHTTP) readinessAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts
}

type delayedDebugProviderTools struct {
	fakeProviderTools
	breakpointTriggered <-chan struct{}
}

func (f *delayedDebugProviderTools) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	if name == "debug_capture_at" {
		select {
		case <-ctx.Done():
			return ToolCallResult{}, ctx.Err()
		case <-f.breakpointTriggered:
		}
	}
	return f.fakeProviderTools.CallTool(ctx, name, arguments)
}

type repeatedDebugTriggerHTTP struct {
	mu                  sync.Mutex
	breakpointTriggered chan struct{}
	requiredAttempts    int
	debugReady          bool
	attempts            int
	once                sync.Once
}

func (p *repeatedDebugTriggerHTTP) Probe(_ context.Context, request HTTPProbeRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if request.Probe.Path == "/api/probe?mode=error" {
		p.debugReady = false
		p.attempts = 0
		return nil
	}
	if request.Probe.Path == "/healthz" {
		if !p.debugReady {
			p.debugReady = true
		}
		return nil
	}
	if p.debugReady && request.Probe.Path == "/api/probe" {
		p.attempts++
		if p.attempts >= p.requiredAttempts {
			p.once.Do(func() { close(p.breakpointTriggered) })
		}
	}
	return nil
}

func (p *repeatedDebugTriggerHTTP) debugTriggerAttempts() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.attempts
}
