// mcp_evidence_test.go 验证 supporting MCP 调用与身份断言失败不会丢失真实调用事实。
//
// 职责：
//   - 锁定 Go fixture prerequisite 的 attempt、response 与 required evidence
//   - 锁定 runtime attestation 后置断言失败时的 sanitized response
//
// 边界：
//   - 使用内存 fake，不启动 packaged MCP 或 Windows 进程
//   - 不替代 Windows 10 x64 真机证据
package windowsvalidation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type scriptedToolCall struct {
	tool   string
	result ToolCallResult
	err    error
}

type scriptedToolCaller struct {
	testing *testing.T
	calls   []scriptedToolCall
	next    int
}

func (f *scriptedToolCaller) CallTool(_ context.Context, name string, _ map[string]any) (ToolCallResult, error) {
	f.testing.Helper()
	if f.next >= len(f.calls) {
		f.testing.Fatalf("unexpected MCP tool call %s", name)
	}
	call := f.calls[f.next]
	f.next++
	if call.tool != name {
		f.testing.Fatalf("MCP tool call=%s, want %s", name, call.tool)
	}
	return call.result, call.err
}

func TestEnsureGoRunningRetainsPrerequisiteCallsAndEvidence(t *testing.T) {
	t.Parallel()
	caller := &scriptedToolCaller{testing: t, calls: []scriptedToolCall{
		{tool: "start_service", result: ToolCallResult{StructuredContent: map[string]any{"data": map[string]any{"accepted": true}}}},
		{tool: "list_services", result: deploymentStateResult("go-validation-dev", "running")},
	}}
	directory := t.TempDir()
	executor := &ScenarioExecutor{
		client: caller, redactor: NewRedactor(), resultsDir: directory,
		variables: map[string]any{"project_id": "project", "go_deployment_id": "go-validation-dev"},
	}
	execution := executor.ensureGoRunning(context.Background(), "browser-go-fixture-running")
	if execution.Result.PhaseStatus != PhaseStatusPass || !execution.Result.Attempted || caller.next != 2 {
		t.Fatalf("prerequisite=%#v calls=%d", execution.Result, caller.next)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "evidence", "prerequisites", "browser-go-fixture-running.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, required := range []string{`"tool": "start_service"`, `"tool": "list_services"`, `"normalized_response"`, `"started_at_utc"`, `"finished_at_utc"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("prerequisite evidence missing %s: %s", required, text)
		}
	}
}

func TestEnsureGoRunningWithoutIdentityIsBlockedAndUnattempted(t *testing.T) {
	t.Parallel()
	caller := &scriptedToolCaller{testing: t}
	executor := &ScenarioExecutor{client: caller, redactor: NewRedactor(), resultsDir: t.TempDir(), variables: map[string]any{}}
	execution := executor.ensureGoRunning(context.Background(), "code-go-fixture-running")
	if execution.Result.PhaseStatus != PhaseStatusBlocked || execution.Result.Attempted || caller.next != 0 {
		t.Fatalf("missing identity prerequisite=%#v calls=%d", execution.Result, caller.next)
	}
}

func TestRemoteHostAvailabilityFailureRetainsListHostsAttempt(t *testing.T) {
	t.Parallel()
	caller := &scriptedToolCaller{testing: t, calls: []scriptedToolCall{{
		tool: "list_hosts",
		result: ToolCallResult{StructuredContent: map[string]any{"data": map[string]any{"remote_hosts": []any{
			map[string]any{"id": "different-host", "is_self": false},
		}}}},
	}}}
	directory := t.TempDir()
	executor := &ScenarioExecutor{client: caller, redactor: NewRedactor(), resultsDir: directory}
	available, prerequisite := executor.preflightRemoteHost(context.Background(), "required-host")
	if available || prerequisite.Result.PhaseStatus != PhaseStatusFail || !prerequisite.Result.Attempted {
		t.Fatalf("remote prerequisite available=%t result=%#v", available, prerequisite.Result)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "evidence", "remote-host-preflight.json"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "different-host") || !strings.Contains(text, "assertion_error") {
		t.Fatalf("remote prerequisite evidence lost response/assertion: %s", text)
	}
}

type fakeAttestationClient struct {
	initialize  MCPInitializeResult
	tools       []map[string]any
	toolNames   []string
	provider    ToolCallResult
	initErr     error
	toolsErr    error
	providerErr error
}

func (f *fakeAttestationClient) Initialize(context.Context) (MCPInitializeResult, error) {
	return f.initialize, f.initErr
}

func (f *fakeAttestationClient) ListTools(context.Context) ([]map[string]any, []string, error) {
	return f.tools, f.toolNames, f.toolsErr
}

func (f *fakeAttestationClient) CallTool(_ context.Context, name string, _ map[string]any) (ToolCallResult, error) {
	if name != "list_language_runtime_providers" {
		return ToolCallResult{}, errors.New("unexpected attestation tool " + name)
	}
	return f.provider, f.providerErr
}

func TestAttestRuntimePreservesToolsListResponseAfterSurfaceAssertionFailure(t *testing.T) {
	t.Parallel()
	client := &fakeAttestationClient{
		toolNames: []string{"actual_tool"},
		tools:     []map[string]any{{"name": "actual_tool", "description": "real response"}},
	}
	client.initialize.ProtocolVersion = "2025-11-25"
	client.initialize.ServerInfo.Name = "superdev"
	client.initialize.ServerInfo.Version = "1.0.0"
	source := PackageSource{}
	source.Frozen.Build.ProductVersion = "1.0.0"
	source.Frozen.SourceSurface.MCPTools.Names = []string{"frozen_tool"}
	directory := t.TempDir()
	attestation, err := attestRuntime(context.Background(), client, source, filepath.Join(directory, "superdev-mcp.exe"), directory, NewRedactor(), "campaign-test", "nsis_core")
	if err == nil {
		t.Fatal("attestation mismatch unexpectedly passed")
	}
	if attestation.Result.PhaseStatus != PhaseStatusFail || !attestation.Result.Attempted || len(attestation.Result.Evidence) != 1 || !attestation.Result.Evidence[0].Present {
		t.Fatalf("attestation failure facts=%#v", attestation.Result)
	}
	raw, readErr := os.ReadFile(filepath.Join(directory, "runtime-attestation.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	text := string(raw)
	for _, required := range []string{"actual_tool", "real response", "installed MCP tool surface differs", `"operation": "tools/list"`} {
		if !strings.Contains(text, required) {
			t.Fatalf("attestation evidence missing %q: %s", required, text)
		}
	}
}

func TestAttestRuntimeFallsBackToInlineEvidenceWhenWriteFails(t *testing.T) {
	t.Parallel()
	client := &fakeAttestationClient{
		toolNames: []string{"actual_tool"},
		tools:     []map[string]any{{"name": "actual_tool", "description": "real response"}},
	}
	client.initialize.ProtocolVersion = "2025-11-25"
	client.initialize.ServerInfo.Name = "superdev"
	client.initialize.ServerInfo.Version = "1.0.0"
	source := PackageSource{}
	source.Frozen.Build.ProductVersion = "1.0.0"
	source.Frozen.SourceSurface.MCPTools.Names = []string{"frozen_tool"}
	blockedRoot := filepath.Join(t.TempDir(), "results-file")
	if err := os.WriteFile(blockedRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	attestation, err := attestRuntime(context.Background(), client, source, "superdev-mcp.exe", blockedRoot, NewRedactor(), "campaign-inline", "nsis_core")
	if err == nil || attestation.InlineEvidence == nil || attestation.Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("attestation write failure lost inline facts: result=%#v inline=%#v err=%v", attestation.Result, attestation.InlineEvidence, err)
	}
	encoded := CanonicalJSON(attestation.InlineEvidence)
	for _, required := range []string{"actual_tool", "real response", "installed MCP tool surface differs", "started_at_utc", "finished_at_utc"} {
		if !strings.Contains(encoded, required) {
			t.Fatalf("inline attestation evidence missing %q: %s", required, encoded)
		}
	}
}

func deploymentStateResult(deploymentID, status string) ToolCallResult {
	return ToolCallResult{StructuredContent: map[string]any{"data": map[string]any{"services": []any{
		map[string]any{"deployments": []any{map[string]any{"id": deploymentID, "status": status}}},
	}}}}
}
