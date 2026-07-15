// provider.go 在 Windows 上通过正式 language runtime provider 验证七语言夹具。
//
// 职责：
//   - 运行 Windows 原生依赖 preflight 与固定构建包装
//   - 经 MCP validate/preview/upsert/start 驱动真实 provider 运行时
//   - 验证 readiness、鉴权正常/受控错误和真实断点采集
//
// 边界：
//   - provider 结果独立报告，不创建或复制任何 75 工具 verdict 行
//   - 依赖缺失保持 BLOCKED，产品/适配器缺陷保持 FAIL
package windowsvalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

type providerEvidence struct {
	Provider           string           `json:"provider"`
	PrerequisiteStages []stageEvidence  `json:"prerequisite_stages"`
	RuntimeStages      []stageEvidence  `json:"runtime_stages"`
	DebugStages        []stageEvidence  `json:"debug_stages"`
	Runtime            ValidationResult `json:"runtime"`
	Debug              ValidationResult `json:"debug"`
}

type providerAdapterBinding struct {
	PrerequisiteKey string
	Command         string
	Source          string
}

type stageEvidence struct {
	Stage    string           `json:"stage"`
	Tool     string           `json:"tool,omitempty"`
	Result   ValidationResult `json:"result"`
	Request  any              `json:"request,omitempty"`
	Response any              `json:"response,omitempty"`
	Summary  any              `json:"summary,omitempty"`
	ExitCode int              `json:"exit_code,omitempty"`
}

// ExecuteProviderMatrix 按冻结顺序执行七语言 runtime/debug 合同并单独报告。
func (e *ScenarioExecutor) ExecuteProviderMatrix(ctx context.Context, fixtures []FixtureManifest) []ProviderExecution {
	results := make([]ProviderExecution, 0, len(fixtures))
	for _, fixture := range fixtures {
		results = append(results, e.executeProvider(ctx, fixture))
	}
	return results
}

func (e *ScenarioExecutor) executeProvider(ctx context.Context, fixture FixtureManifest) ProviderExecution {
	log := logger.GetLogger().WithEntryName("WindowsValidationProvider")
	log.WithFields(e.logFields(map[string]any{"provider": fixture.Provider, "stage": "begin"})).Info("开始执行 Windows language provider 合同")
	evidence := providerEvidence{Provider: fixture.Provider}
	fixtureRoot := filepath.Join(fmt.Sprint(e.variables["project_root"]), filepath.FromSlash(fixture.CWD))
	stage := e.executeFixtureCommand(ctx, fixtureRoot, "preflight.cmd", "runtime")
	stage.Stage = "runtime_preflight"
	evidence.PrerequisiteStages = append(evidence.PrerequisiteStages, stage)
	if stage.Result.PhaseStatus != PhaseStatusPass {
		reason := resultReason(stage.Result)
		return e.finishProvider(fixture, evidence,
			blockedResult("runtime_preflight", reason),
			blockedResult("runtime_preflight", reason), reason)
	}
	stage = e.executeFixtureCommand(ctx, fixtureRoot, fixture.Build.WindowsCommand)
	stage.Stage = "provider_build"
	evidence.PrerequisiteStages = append(evidence.PrerequisiteStages, stage)
	if stage.Result.PhaseStatus != PhaseStatusPass {
		reason := resultReason(stage.Result)
		return e.finishProvider(fixture, evidence,
			blockedResult("provider_build", reason),
			blockedResult("provider_build", reason), reason)
	}

	configStarted := time.Now().UTC()
	e.logProviderStage(fixture.Provider, "runtime_config", "render_fixture_runtime", "started", nil)
	runtimeConfig, env, err := e.renderFixtureRuntime(fixture, fixtureRoot)
	configFinished := time.Now().UTC()
	if err != nil {
		e.logProviderStage(fixture.Provider, "runtime_config", "render_fixture_runtime", "failed", err)
		reason := err.Error()
		evidence.PrerequisiteStages = append(evidence.PrerequisiteStages, stageEvidence{
			Stage: "runtime_config", Tool: "render_fixture_runtime",
			Result:  providerStageResult(false, reason, configStarted, configFinished),
			Summary: map[string]any{"provider": fixture.Provider, "rendered": false},
		})
		return e.finishProvider(fixture, evidence,
			blockedResult("runtime_config", reason), blockedResult("runtime_config", reason), reason)
	}
	e.logProviderStage(fixture.Provider, "runtime_config", "render_fixture_runtime", "succeeded", nil)
	evidence.PrerequisiteStages = append(evidence.PrerequisiteStages, stageEvidence{
		Stage: "runtime_config", Tool: "render_fixture_runtime",
		Result:  providerStageResult(true, "", configStarted, configFinished),
		Summary: map[string]any{"provider": fixture.Provider, "rendered": true},
	})
	if err := e.configureAndStartProvider(ctx, fixture, runtimeConfig, env, &evidence); err != nil {
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence,
			aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
			blockedResult("provider_runtime", err.Error()), err.Error())
	}
	if err := e.probeFixtureRuntime(ctx, fixture, &evidence); err != nil {
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence,
			aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
			blockedResult("provider_runtime", err.Error()), err.Error())
	}

	debugStage := e.executeFixtureCommand(ctx, fixtureRoot, "preflight.cmd", "debug")
	debugStage.Stage = "debug_preflight"
	evidence.PrerequisiteStages = append(evidence.PrerequisiteStages, debugStage)
	if debugStage.Result.PhaseStatus != PhaseStatusPass {
		reason := resultReason(debugStage.Result)
		if err := e.recordProviderStop(ctx, fixture, &evidence); err != nil {
			reason += "; cleanup: " + err.Error()
		}
		return e.finishProvider(fixture, evidence,
			aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
			blockedResult("debug_preflight", resultReason(debugStage.Result)), reason)
	}
	if err := e.captureProviderBreakpoint(ctx, fixture, &evidence); err != nil {
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence,
			aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
			aggregateStageResults(fixture.Provider+" debug", evidence.DebugStages), err.Error())
	}
	if err := e.recordProviderStop(ctx, fixture, &evidence); err != nil {
		return e.finishProvider(fixture, evidence,
			aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
			aggregateStageResults(fixture.Provider+" debug", evidence.DebugStages), err.Error())
	}
	return e.finishProvider(fixture, evidence,
		aggregateStageResults(fixture.Provider+" runtime", evidence.RuntimeStages),
		aggregateStageResults(fixture.Provider+" debug", evidence.DebugStages), "")
}

func (e *ScenarioExecutor) renderFixtureRuntime(fixture FixtureManifest, fixtureRoot string) (map[string]any, map[string]string, error) {
	rendered, err := RenderValue(fixture.Runtime.Config, e.variables)
	if err != nil {
		return nil, nil, err
	}
	config, ok := rendered.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("provider %s runtime config is not an object", fixture.Provider)
	}
	env := map[string]string{}
	for key, raw := range fixture.Runtime.Env {
		value, err := RenderValue(raw, e.variables)
		if err != nil {
			return nil, nil, err
		}
		env[key] = fmt.Sprint(value)
	}
	// PATH_PREPEND 是夹具元指令，不属于产品 runtime env；展开成真实 PATH 后删除。
	if prepend := env["PATH_PREPEND"]; prepend != "" {
		env["PATH"] = filepath.Join(fixtureRoot, filepath.FromSlash(prepend)) + string(os.PathListSeparator) + os.Getenv("PATH")
		delete(env, "PATH_PREPEND")
	}
	if _, bound := e.providerAdapters[fixture.Provider]; bound {
		fixtureEnvironment, err := providerFixtureEnvironment(fixture.Provider, e.providerAdapters, e.agentDataDirectory)
		if err != nil {
			return nil, nil, err
		}
		for key, value := range fixtureEnvironment {
			env[key] = value
		}
	}
	env["FIXTURE_CAMPAIGN_ID"] = fmt.Sprint(e.variables["campaign_id"])
	return config, env, nil
}

func (e *ScenarioExecutor) configureAndStartProvider(ctx context.Context, fixture FixtureManifest, config map[string]any, env map[string]string, evidence *providerEvidence) error {
	projectID := fmt.Sprint(e.variables["project_id"])
	projectRoot := fmt.Sprint(e.variables["project_root"])
	serviceID := "provider-" + fixture.Provider
	deploymentID := serviceID + "-dev"
	codeDebug, err := providerCodeDebugConfig(fixture.Provider, e.providerAdapters)
	if err != nil {
		return err
	}
	// 在任何写操作前固定资源身份，后续失败路径才能只停止本 campaign 的 deployment。
	e.variables["provider_"+fixture.Provider+"_deployment_id"] = deploymentID
	base := map[string]any{"language": fixture.Provider, "project_root": projectRoot, "cwd": fixture.CWD, "env": env, "config": config}
	for _, call := range []struct {
		stage string
		tool  string
		args  map[string]any
	}{
		{"validate_runtime", "validate_service_runtime", base},
		{"preview_execution", "preview_service_execution", mergeMaps(base, map[string]any{"intent": "start_dev", "artifact_dir": filepath.Join(projectRoot, "artifacts", fixture.Provider)})},
		{"upsert_service", "upsert_service", map[string]any{
			"project_id": projectID, "root_path": projectRoot,
			"service": map[string]any{"id": serviceID, "name": serviceID, "language": fixture.Provider, "required": false, "order": 100,
				"deployments": []any{map[string]any{"id": deploymentID, "env_name": "validation", "location": "local", "control_mode": "managed",
					"runtime":    map[string]any{"type": "language", "cwd": fixture.CWD, "env": env, "config": config},
					"logs":       map[string]any{"type": "process"},
					"readiness":  map[string]any{"type": "http", "target": fixture.Readiness.URL, "timeout_seconds": 45},
					"code_debug": codeDebug}}},
		}},
		{"start_service", "start_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300}},
	} {
		e.logProviderStage(fixture.Provider, call.stage, call.tool, "started", nil)
		started := time.Now().UTC()
		result, err := e.client.CallTool(ctx, call.tool, call.args)
		finished := time.Now().UTC()
		if err != nil || result.IsError {
			if err == nil {
				err = fmt.Errorf("product error %s", toolErrorCode(result))
			}
			e.logProviderStage(fixture.Provider, call.stage, call.tool, "failed", err)
			evidence.RuntimeStages = append(evidence.RuntimeStages, providerCallStage(
				call.stage, call.tool, call.args, result, err, started, finished,
			))
			return fmt.Errorf("provider %s %s: %w", fixture.Provider, call.stage, err)
		}
		e.logProviderStage(fixture.Provider, call.stage, call.tool, "succeeded", nil)
		evidence.RuntimeStages = append(evidence.RuntimeStages, providerCallStage(
			call.stage, call.tool, call.args, result, nil, started, finished,
		))
	}
	pollStarted := time.Now().UTC()
	e.logProviderStage(fixture.Provider, "running_state", "list_services", "started", nil)
	deadline := time.Now().Add(90 * time.Second)
	var pollAttempts []any
	for time.Now().Before(deadline) {
		attemptStarted := time.Now().UTC()
		state, err := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		attemptFinished := time.Now().UTC()
		attempt := map[string]any{
			"started_at_utc":  attemptStarted.Format(time.RFC3339Nano),
			"finished_at_utc": attemptFinished.Format(time.RFC3339Nano),
			"response":        RawMessageMap(state),
		}
		if err != nil {
			attempt["transport_error"] = err.Error()
		}
		pollAttempts = append(pollAttempts, attempt)
		if err == nil && !state.IsError && deploymentStatus(state, deploymentID) == "running" {
			e.logProviderStage(fixture.Provider, "running_state", "list_services", "succeeded", nil)
			e.variables["provider_"+fixture.Provider+"_deployment_id"] = deploymentID
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
				Stage: "running_state", Tool: "list_services",
				Result:  providerStageResult(true, "", pollStarted, attemptFinished),
				Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
				Summary: map[string]any{"deployment_id": deploymentID, "status": "running"},
			})
			return nil
		}
		select {
		case <-ctx.Done():
			failure := ctx.Err()
			e.logProviderStage(fixture.Provider, "running_state", "list_services", "failed", failure)
			finished := time.Now().UTC()
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
				Stage: "running_state", Tool: "list_services",
				Result:  providerStageResult(false, failure.Error(), pollStarted, finished),
				Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
			})
			return failure
		case <-time.After(time.Second):
		}
	}
	failure := fmt.Errorf("provider %s deployment did not reach running", fixture.Provider)
	e.logProviderStage(fixture.Provider, "running_state", "list_services", "failed", failure)
	evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
		Stage: "running_state", Tool: "list_services",
		Result:  providerStageResult(false, failure.Error(), pollStarted, time.Now().UTC()),
		Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
	})
	return failure
}

func providerCodeDebugConfig(provider string, bindings map[string]providerAdapterBinding) (map[string]any, error) {
	binding, found := bindings[provider]
	if !fixtureProviderSupported(provider) {
		return nil, fmt.Errorf("provider %s has no adapter contract", provider)
	}
	if !found || strings.TrimSpace(binding.Command) == "" {
		// diagnostic core_only 可以允许一个 adapter prerequisite BLOCKED。runtime
		// 仍可运行，但 debug 必须显式关闭，不能让 policy=auto 重新查 Agent PATH。
		return map[string]any{"policy": "disabled"}, nil
	}
	if !isAbsoluteRuntimePath(binding.Command) {
		return nil, fmt.Errorf("provider %s admitted adapter binding is not absolute", provider)
	}
	// 这里下发 collector 已实际执行并准入的绝对路径；Agent 不再重新解析
	// runtime input、ambient PATH 或 provider 默认值，避免 preflight A / execution B。
	return map[string]any{"policy": "auto", "adapter_command": binding.Command}, nil
}

func fixtureProviderSupported(provider string) bool {
	switch provider {
	case "go", "python", "node", "rust", "cpp", "java", "kotlin":
		return true
	default:
		return false
	}
}

func buildProviderAdapterBindings(plan EnvironmentCollectionPlan, manifest EnvironmentManifest) (map[string]providerAdapterBinding, error) {
	prerequisites := environmentPrerequisiteMap(manifest.Prerequisites)
	bindings := map[string]providerAdapterBinding{}
	for _, adapter := range plan.Adapters {
		providers, supported := fixtureProvidersForAdapterKey(adapter.Key)
		if !supported {
			return nil, fmt.Errorf("environment adapter %s has no provider fixture binding", adapter.Key)
		}
		prerequisite, found := prerequisites[adapter.Key]
		if !found || prerequisite.Result.PhaseStatus != PhaseStatusPass {
			// core_only 可以准入具名 blocker；缺失 binding 的 provider 后续保持 BLOCKED，
			// 绝不能回退到 Agent PATH 再尝试另一份 executable。
			continue
		}
		command := strings.TrimSpace(prerequisite.Resolved.Path)
		source := strings.TrimSpace(prerequisite.Resolved.Source)
		if command == "" || !isAbsoluteRuntimePath(command) {
			return nil, fmt.Errorf("environment adapter %s PASS result has no absolute resolved path", adapter.Key)
		}
		if source == "" || source != strings.TrimSpace(adapter.Expected.Source) {
			return nil, fmt.Errorf("environment adapter %s resolved source differs from the frozen plan", adapter.Key)
		}
		binding := providerAdapterBinding{PrerequisiteKey: adapter.Key, Command: command, Source: source}
		for _, provider := range providers {
			if _, duplicated := bindings[provider]; duplicated {
				return nil, fmt.Errorf("provider %s has duplicated admitted adapter bindings", provider)
			}
			bindings[provider] = binding
		}
	}
	return bindings, nil
}

func fixtureProvidersForAdapterKey(key string) ([]string, bool) {
	switch key {
	case EnvironmentKeyAdapterGo:
		return []string{"go"}, true
	case EnvironmentKeyAdapterPython:
		return []string{"python"}, true
	case EnvironmentKeyAdapterNode:
		return []string{"node"}, true
	case EnvironmentKeyAdapterNative:
		return []string{"rust", "cpp"}, true
	case EnvironmentKeyAdapterJVM:
		return []string{"java", "kotlin"}, true
	default:
		return nil, false
	}
}

func providerFixtureEnvironment(provider string, bindings map[string]providerAdapterBinding, agentDataDirectory string) (map[string]string, error) {
	binding, found := bindings[provider]
	if !found || strings.TrimSpace(binding.Command) == "" {
		return nil, fmt.Errorf("provider %s has no admitted adapter binding", provider)
	}
	if !isAbsoluteRuntimePath(binding.Command) {
		return nil, fmt.Errorf("provider %s admitted adapter binding is not absolute", provider)
	}
	environment := map[string]string{}
	switch provider {
	case "java", "kotlin":
		environment["SUPERDEV_JVM_ADAPTER_COMMAND"] = binding.Command
	case "node":
		if !isAbsoluteRuntimePath(agentDataDirectory) {
			return nil, fmt.Errorf("Node provider has no bound Agent data directory")
		}
		environment["SUPERDEV_AGENT_DATA_DIR"] = agentDataDirectory
	}
	return environment, nil
}

func (e *ScenarioExecutor) probeFixtureRuntime(ctx context.Context, fixture FixtureManifest, evidence *providerEvidence) error {
	e.logProviderStage(fixture.Provider, "readiness", "http", "started", nil)
	started := time.Now().UTC()
	status, body, err := fixtureRequest(ctx, "GET", fixture.Readiness.URL, "", nil)
	if err == nil && status != fixture.Readiness.Status {
		err = fmt.Errorf("status=%d, want %d", status, fixture.Readiness.Status)
	}
	if err == nil {
		if assertionErr := assertFixtureJSON(body, map[string]any{"ready": true, "provider": fixture.Provider}); assertionErr != nil {
			err = fmt.Errorf("readiness body: %w", assertionErr)
		}
	}
	finished := time.Now().UTC()
	evidence.RuntimeStages = append(evidence.RuntimeStages, providerHTTPStage(
		"readiness", map[string]any{"method": "GET", "url": fixture.Readiness.URL}, status, body, err, started, finished,
	))
	if err != nil {
		e.logProviderStage(fixture.Provider, "readiness", "http", "failed", err)
		return fmt.Errorf("provider %s readiness: %w", fixture.Provider, err)
	}
	e.logProviderStage(fixture.Provider, "readiness", "http", "succeeded", nil)
	for _, probe := range []struct {
		name    string
		outcome string
		status  int
	}{
		{"normal_probe", "ok", 200}, {"controlled_error", "error", 500},
	} {
		e.logProviderStage(fixture.Provider, probe.name, "http", "started", nil)
		started = time.Now().UTC()
		status, body, err = e.performFixtureProbe(ctx, fixture, probe.outcome)
		if err == nil && status != probe.status {
			err = fmt.Errorf("status=%d, want %d", status, probe.status)
		}
		expectedCode := "fixture_ok"
		if fixture.Provider == "go" {
			expectedCode = ""
		}
		if probe.outcome == "error" {
			expectedCode = "fixture_controlled_error"
		}
		expected := map[string]any{"ok": probe.outcome != "error"}
		if expectedCode != "" {
			expected["code"] = expectedCode
		}
		if err == nil {
			if assertionErr := assertFixtureJSON(body, expected); assertionErr != nil {
				err = fmt.Errorf("response body: %w", assertionErr)
			}
		}
		finished = time.Now().UTC()
		evidence.RuntimeStages = append(evidence.RuntimeStages, providerHTTPStage(
			probe.name, map[string]any{"outcome": probe.outcome}, status, body, err, started, finished,
		))
		if err != nil {
			e.logProviderStage(fixture.Provider, probe.name, "http", "failed", err)
			return fmt.Errorf("provider %s %s: %w", fixture.Provider, probe.name, err)
		}
		e.logProviderStage(fixture.Provider, probe.name, "http", "succeeded", nil)
	}
	return nil
}

func (e *ScenarioExecutor) performFixtureProbe(ctx context.Context, fixture FixtureManifest, outcome string) (int, []byte, error) {
	auth := fmt.Sprint(e.variables["fixture_authorization"])
	if fixture.Provider == "go" {
		path := fixture.Contract.NormalPath
		if outcome == "error" {
			path = fixture.Contract.ErrorPath
		}
		return fixtureRequest(ctx, "GET", fmt.Sprintf("http://127.0.0.1:%d%s", fixture.Run.Port, path), auth, nil)
	}
	payload, _ := json.Marshal(map[string]any{"trace_id": fixture.Provider + "-" + outcome + "-trace", "request_id": fixture.Provider + "-" + outcome + "-request", "outcome": outcome, "value": 41})
	return fixtureRequest(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d%s", fixture.Run.Port, fixture.Contract.NormalPath), auth, payload)
}

func (e *ScenarioExecutor) captureProviderBreakpoint(ctx context.Context, fixture FixtureManifest, evidence *providerEvidence) error {
	e.logProviderStage(fixture.Provider, "debug_capture_at", "debug_capture_at", "started", nil)
	deploymentID := fmt.Sprint(e.variables["provider_"+fixture.Provider+"_deployment_id"])
	triggerDone := make(chan struct{})
	go func() {
		defer close(triggerDone)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_, _, _ = e.performFixtureProbe(ctx, fixture, "ok")
		}
	}()
	arguments := map[string]any{
		"deployment_id": deploymentID, "source": fixture.Debug.Source, "line": fixture.Debug.Line,
		"timeout_ms": 45000, "max_variables": 50, "variable_names": fixture.Debug.Variables,
		"approval_wait_seconds": 300,
	}
	started := time.Now().UTC()
	result, err := e.client.CallTool(ctx, "debug_capture_at", arguments)
	<-triggerDone
	if err == nil && result.IsError {
		err = fmt.Errorf("product error %s", toolErrorCode(result))
	}
	if err == nil {
		canonical := CanonicalJSON(RawMessageMap(result))
		for _, variable := range fixture.Debug.Variables {
			if !strings.Contains(canonical, variable) {
				err = fmt.Errorf("debug capture omitted variable %s", variable)
				break
			}
		}
		if err == nil {
			for name, expected := range fixture.Debug.Expected {
				if !strings.Contains(canonical, name) || !strings.Contains(canonical, fmt.Sprint(expected)) {
					err = fmt.Errorf("debug capture omitted expected %s=%v", name, expected)
					break
				}
			}
		}
	}
	finished := time.Now().UTC()
	evidence.DebugStages = append(evidence.DebugStages, providerCallStage(
		"debug_capture_at", "debug_capture_at", arguments, result, err, started, finished,
	))
	if err != nil {
		e.logProviderStage(fixture.Provider, "debug_capture_at", "debug_capture_at", "failed", err)
		return fmt.Errorf("provider %s debug capture: %w", fixture.Provider, err)
	}
	e.logProviderStage(fixture.Provider, "debug_capture_at", "debug_capture_at", "succeeded", nil)
	return nil
}

func (e *ScenarioExecutor) recordProviderStop(ctx context.Context, fixture FixtureManifest, evidence *providerEvidence) error {
	projectID := fmt.Sprint(e.variables["project_id"])
	deploymentID := fmt.Sprint(e.variables["provider_"+fixture.Provider+"_deployment_id"])
	if deploymentID == "" {
		return nil
	}
	stopArguments := map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300}
	e.logProviderStage(fixture.Provider, "stop_service", "stop_service", "started", nil)
	stopStarted := time.Now().UTC()
	stopResult, err := e.client.CallTool(ctx, "stop_service", stopArguments)
	stopFinished := time.Now().UTC()
	if err == nil && stopResult.IsError {
		err = fmt.Errorf("product error %s", toolErrorCode(stopResult))
	}
	evidence.RuntimeStages = append(evidence.RuntimeStages, providerCallStage(
		"stop_service", "stop_service", stopArguments, stopResult, err, stopStarted, stopFinished,
	))
	if err != nil {
		e.logProviderStage(fixture.Provider, "stop_service", "stop_service", "failed", err)
		return fmt.Errorf("stop provider %s: %w", fixture.Provider, err)
	}
	e.logProviderStage(fixture.Provider, "stop_service", "stop_service", "succeeded", nil)
	pollStarted := time.Now().UTC()
	e.logProviderStage(fixture.Provider, "stopped_state", "list_services", "started", nil)
	deadline := time.Now().Add(60 * time.Second)
	var pollAttempts []any
	for time.Now().Before(deadline) {
		attemptStarted := time.Now().UTC()
		state, callErr := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		attemptFinished := time.Now().UTC()
		attempt := map[string]any{
			"started_at_utc":  attemptStarted.Format(time.RFC3339Nano),
			"finished_at_utc": attemptFinished.Format(time.RFC3339Nano),
			"response":        RawMessageMap(state),
		}
		if callErr != nil {
			attempt["transport_error"] = callErr.Error()
		}
		pollAttempts = append(pollAttempts, attempt)
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
			e.logProviderStage(fixture.Provider, "stopped_state", "list_services", "succeeded", nil)
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
				Stage: "stopped_state", Tool: "list_services",
				Result:  providerStageResult(true, "", pollStarted, attemptFinished),
				Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
				Summary: map[string]any{"deployment_id": deploymentID, "status": "stopped"},
			})
			return nil
		}
		select {
		case <-ctx.Done():
			err = ctx.Err()
			e.logProviderStage(fixture.Provider, "stopped_state", "list_services", "failed", err)
			finished := time.Now().UTC()
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
				Stage: "stopped_state", Tool: "list_services",
				Result:  providerStageResult(false, err.Error(), pollStarted, finished),
				Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
			})
			return err
		case <-time.After(time.Second):
		}
	}
	err = fmt.Errorf("provider %s deployment did not reach stopped", fixture.Provider)
	e.logProviderStage(fixture.Provider, "stopped_state", "list_services", "failed", err)
	evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{
		Stage: "stopped_state", Tool: "list_services",
		Result:  providerStageResult(false, err.Error(), pollStarted, time.Now().UTC()),
		Request: map[string]any{"project_id": projectID}, Response: pollAttempts,
	})
	return err
}

func (e *ScenarioExecutor) finishProvider(fixture FixtureManifest, evidence providerEvidence, runtimeResult, debugResult ValidationResult, reason string) ProviderExecution {
	log := logger.GetLogger().WithEntryName("WindowsValidationProvider")
	relative := providerEvidenceRef(fixture.Provider)
	path := filepath.Join(e.resultsDir, filepath.FromSlash(relative))
	present := EvidenceRecord{Name: "provider_evidence", Required: true, Present: true, Ref: relative}
	completedEvidence := completeProviderEvidence(evidence, runtimeResult, debugResult, present)
	redactedEvidence := e.redactor.Redact(RawMessageMap(completedEvidence))
	var writeErr error
	if e.redactor.containsKnownSecret(redactedEvidence) {
		writeErr = fmt.Errorf("redaction invariant failed before writing provider evidence")
	} else {
		writeErr = writeJSON(path, redactedEvidence)
	}
	inline := map[string]any(nil)
	if writeErr != nil {
		missing := EvidenceRecord{Name: "provider_evidence", Required: true, Present: false, Ref: relative, WriteError: writeErr.Error()}
		completedEvidence = completeProviderEvidence(evidence, runtimeResult, debugResult, missing)
		reason = "write provider evidence: " + writeErr.Error()
		failedPayload := e.redactor.Redact(RawMessageMap(completedEvidence))
		if safe, ok := failedPayload.(map[string]any); ok && !e.redactor.containsKnownSecret(safe) {
			inline = safe
		}
		log.WithErr(writeErr).WithFields(e.logFields(map[string]any{"provider": fixture.Provider, "stage": "persist_evidence"})).Error("Windows language provider 证据写入失败")
	}
	runtimeResult = completedEvidence.Runtime
	debugResult = completedEvidence.Debug
	prerequisites := providerPrerequisiteExecutions(completedEvidence.PrerequisiteStages)
	children := make([]ValidationResult, 0, 2+len(prerequisites))
	children = append(children, runtimeResult, debugResult)
	for _, prerequisite := range prerequisites {
		children = append(children, prerequisite.Result)
	}
	overall := aggregateResult(fixture.Provider+" provider", len(children), children)
	if strings.TrimSpace(reason) == "" && overall.PhaseStatus != PhaseStatusPass {
		reason = resultReason(overall)
	}
	result := ProviderExecution{
		Provider: fixture.Provider, Result: overall, Runtime: runtimeResult, Debug: debugResult, Prerequisites: prerequisites,
		EvidencePath: relative, InlineEvidence: inline, Reason: reason,
	}
	fields := e.logFields(map[string]any{"provider": fixture.Provider, "stage": "complete", "runtime": result.Runtime.PhaseStatus, "debug": result.Debug.PhaseStatus, "result": result.Result.PhaseStatus})
	if result.Result.PhaseStatus == PhaseStatusFail {
		log.WithFields(fields).WithField("reason", reason).Error("Windows language provider 合同执行失败")
	} else if result.Result.PhaseStatus == PhaseStatusBlocked || result.Result.PhaseStatus == PhaseStatusNotRun {
		log.WithFields(fields).WithField("reason", reason).Info("Windows language provider 合同执行受阻")
	} else {
		log.WithFields(fields).Info("Windows language provider 合同执行完成")
	}
	return result
}

func (e *ScenarioExecutor) logProviderStage(provider, stage, tool, outcome string, stageErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationProviderStage")
	fields := e.logFields(map[string]any{"provider": provider, "stage": stage, "tool": tool, "outcome": outcome})
	if stageErr != nil {
		log.WithErr(stageErr).WithFields(fields).Error("Windows language provider 阶段失败")
		return
	}
	log.WithFields(fields).Info("Windows language provider 阶段状态变化")
}

func completeProviderEvidence(evidence providerEvidence, runtimeResult, debugResult ValidationResult, record EvidenceRecord) providerEvidence {
	completed := evidence
	completed.PrerequisiteStages = append([]stageEvidence{}, evidence.PrerequisiteStages...)
	completed.RuntimeStages = append([]stageEvidence{}, evidence.RuntimeStages...)
	completed.DebugStages = append([]stageEvidence{}, evidence.DebugStages...)
	for index := range completed.PrerequisiteStages {
		completed.PrerequisiteStages[index].Result = withEvidence(completed.PrerequisiteStages[index].Result, record)
	}
	for index := range completed.RuntimeStages {
		completed.RuntimeStages[index].Result = withEvidence(completed.RuntimeStages[index].Result, record)
	}
	for index := range completed.DebugStages {
		completed.DebugStages[index].Result = withEvidence(completed.DebugStages[index].Result, record)
	}
	completed.Runtime = withEvidence(runtimeResult, record)
	completed.Debug = withEvidence(debugResult, record)
	return completed
}

func providerPrerequisiteExecutions(stages []stageEvidence) []StepExecution {
	results := make([]StepExecution, 0, len(stages))
	for _, stage := range stages {
		results = append(results, StepExecution{
			StepID: stage.Stage, Tool: stage.Tool, Coverage: CoverageSupporting, Result: stage.Result,
		})
	}
	return results
}

func (e *ScenarioExecutor) runFixtureCommand(ctx context.Context, directory, command string, args ...string) stageEvidence {
	stage := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), filepath.Ext(command))
	provider := filepath.Base(directory)
	log := logger.GetLogger().WithEntryName("WindowsValidationFixtureCommand")
	log.WithFields(e.logFields(map[string]any{"provider": provider, "stage": stage, "tool": filepath.Base(command)})).Info("开始执行 Windows fixture 外部进程")
	started := time.Now().UTC()
	fixtureEnvironment := map[string]string{}
	if fixtureCommandRequiresAdapterBinding(command, args) {
		var bindingErr error
		fixtureEnvironment, bindingErr = providerFixtureEnvironment(provider, e.providerAdapters, e.agentDataDirectory)
		if bindingErr != nil {
			log.WithFields(e.logFields(map[string]any{"provider": provider, "stage": stage, "tool": filepath.Base(command), "cause_code": "adapter_binding_unavailable"})).Error("Windows fixture 缺少已准入 adapter 绑定")
			return stageEvidence{
				Stage: stage, Tool: filepath.Base(command),
				Result:  blockedResult("provider_adapter_binding", bindingErr.Error()),
				Request: map[string]any{"command": filepath.Base(command), "arguments": args},
				Summary: map[string]any{"cause_code": "adapter_binding_unavailable"},
			}
		}
	}
	cmdArgs := []string{"/d", "/s", "/c", "call", command}
	cmdArgs = append(cmdArgs, args...)
	commandPath, commandErr := trustedWindowsCommandPath()
	if commandErr != nil {
		return stageEvidence{
			Stage: stage, Tool: filepath.Base(command),
			Result:  blockedResult("trusted_cmd_unavailable", "trusted System32 cmd.exe is unavailable"),
			Request: map[string]any{"command": filepath.Base(command), "arguments": args},
			Summary: map[string]any{"cause_code": "trusted_cmd_unavailable"},
		}
	}
	cmd := exec.CommandContext(ctx, commandPath, cmdArgs...)
	cmd.Dir = directory
	cmd.Env = environmentWithOverrides(os.Environ(), fixtureEnvironment)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	finished := time.Now().UTC()
	if err == nil {
		log.WithFields(e.logFields(map[string]any{"provider": provider, "stage": stage, "tool": filepath.Base(command), "exit_code": 0})).Info("Windows fixture 外部进程执行完成")
		return stageEvidence{
			Stage: stage, Tool: filepath.Base(command),
			Result:   providerStageResult(true, "", started, finished),
			Request:  map[string]any{"command": filepath.Base(command), "arguments": args},
			Response: map[string]any{"exit_code": 0, "output": output.String()},
			Summary:  map[string]any{"command": filepath.Base(command), "exit_code": 0},
		}
	}
	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	failure := strings.TrimSpace(output.String())
	if failure == "" {
		failure = err.Error()
	}
	log.WithErr(err).WithFields(e.logFields(map[string]any{"provider": provider, "stage": stage, "tool": filepath.Base(command), "exit_code": exitCode})).Error("Windows fixture 外部进程执行失败")
	return stageEvidence{
		Stage: stage, Tool: filepath.Base(command), ExitCode: exitCode,
		Result:   providerStageResult(false, failure, started, finished),
		Request:  map[string]any{"command": filepath.Base(command), "arguments": args},
		Response: map[string]any{"exit_code": exitCode, "output": output.String(), "process_error": err.Error()},
	}
}

func (e *ScenarioExecutor) executeFixtureCommand(ctx context.Context, directory, command string, args ...string) stageEvidence {
	if e.fixtureCommandRunner != nil {
		return e.fixtureCommandRunner(ctx, directory, command, args...)
	}
	return e.runFixtureCommand(ctx, directory, command, args...)
}

func fixtureCommandRequiresAdapterBinding(command string, args []string) bool {
	return strings.EqualFold(filepath.Base(command), "preflight.cmd") && len(args) == 1 && strings.EqualFold(args[0], "debug")
}

func environmentWithOverrides(base []string, overrides map[string]string) []string {
	environment := append([]string{}, base...)
	for key, value := range overrides {
		replaced := false
		for index, entry := range environment {
			name, _, found := strings.Cut(entry, "=")
			if found && strings.EqualFold(name, key) {
				environment[index] = key + "=" + value
				replaced = true
			}
		}
		if !replaced {
			environment = append(environment, key+"="+value)
		}
	}
	return environment
}

func providerCallStage(stage, tool string, request map[string]any, response ToolCallResult, callErr error, started, finished time.Time) stageEvidence {
	failure := ""
	if callErr != nil {
		failure = callErr.Error()
	}
	return stageEvidence{
		Stage: stage, Tool: tool,
		Result:  providerStageResult(callErr == nil, failure, started, finished),
		Request: cloneJSONMap(request), Response: RawMessageMap(response),
	}
}

func providerHTTPStage(stage string, request map[string]any, status int, body []byte, callErr error, started, finished time.Time) stageEvidence {
	failure := ""
	if callErr != nil {
		failure = callErr.Error()
	}
	return stageEvidence{
		Stage: stage, Tool: "http",
		Result:   providerStageResult(callErr == nil, failure, started, finished),
		Request:  cloneJSONMap(request),
		Response: map[string]any{"status": status, "body": string(body), "body_sha256": digestText(body)},
	}
}

func providerStageResult(succeeded bool, failure string, started, finished time.Time) ValidationResult {
	return attemptedResult(succeeded, failure, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), nil)
}

func providerEvidenceRef(provider string) string {
	return filepath.ToSlash(filepath.Join("evidence", "providers", provider+".json"))
}

func aggregateStageResults(name string, stages []stageEvidence) ValidationResult {
	children := make([]ValidationResult, 0, len(stages))
	for _, stage := range stages {
		children = append(children, stage.Result)
	}
	return aggregateResult(name, len(children), children)
}

func fixtureRequest(ctx context.Context, method, url, authorization string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, data, err
}

func assertFixtureJSON(body []byte, expected map[string]any) error {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return err
	}
	for key, value := range expected {
		actual, exists := payload[key]
		if !exists || fmt.Sprint(actual) != fmt.Sprint(value) {
			return fmt.Errorf("field %s=%v, want %v", key, actual, value)
		}
	}
	return nil
}

func mergeMaps(left, right map[string]any) map[string]any {
	out := make(map[string]any, len(left)+len(right))
	for key, value := range left {
		out[key] = value
	}
	for key, value := range right {
		out[key] = value
	}
	return out
}

func digestText(value []byte) string {
	digest, _, _ := digestEvidenceValue(string(value))
	return digest
}
