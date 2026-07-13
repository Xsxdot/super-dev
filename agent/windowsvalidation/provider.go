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
	Provider       string          `json:"provider"`
	RuntimeStages  []stageEvidence `json:"runtime_stages"`
	DebugStages    []stageEvidence `json:"debug_stages"`
	RuntimeVerdict string          `json:"runtime_verdict"`
	DebugVerdict   string          `json:"debug_verdict"`
}

type stageEvidence struct {
	Stage    string `json:"stage"`
	Verdict  string `json:"verdict"`
	Summary  any    `json:"summary,omitempty"`
	Error    string `json:"error,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
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
	log.WithField("provider", fixture.Provider).Info("开始执行 Windows language provider 合同")
	evidence := providerEvidence{Provider: fixture.Provider, RuntimeVerdict: verdictPass, DebugVerdict: verdictPass}
	fixtureRoot := filepath.Join(fmt.Sprint(e.variables["project_root"]), filepath.FromSlash(fixture.CWD))
	if stage := runFixtureCommand(ctx, fixtureRoot, "preflight.cmd", "runtime"); stage.Verdict != verdictPass {
		evidence.RuntimeStages = append(evidence.RuntimeStages, stage)
		evidence.RuntimeVerdict = stage.Verdict
		evidence.DebugVerdict = verdictBlocked
		return e.finishProvider(fixture, evidence, stage.Error)
	} else {
		evidence.RuntimeStages = append(evidence.RuntimeStages, stage)
	}
	if stage := runFixtureCommand(ctx, fixtureRoot, fixture.Build.WindowsCommand); stage.Verdict != verdictPass {
		evidence.RuntimeStages = append(evidence.RuntimeStages, stage)
		evidence.RuntimeVerdict = stage.Verdict
		evidence.DebugVerdict = verdictBlocked
		return e.finishProvider(fixture, evidence, stage.Error)
	} else {
		evidence.RuntimeStages = append(evidence.RuntimeStages, stage)
	}

	runtimeConfig, env, err := e.renderFixtureRuntime(fixture, fixtureRoot)
	if err != nil {
		evidence.RuntimeVerdict = verdictFail
		return e.finishProvider(fixture, evidence, err.Error())
	}
	if err := e.configureAndStartProvider(ctx, fixture, runtimeConfig, env, &evidence); err != nil {
		evidence.RuntimeVerdict = verdictFail
		evidence.DebugVerdict = verdictBlocked
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence, err.Error())
	}
	if err := e.probeFixtureRuntime(ctx, fixture, &evidence); err != nil {
		evidence.RuntimeVerdict = verdictFail
		evidence.DebugVerdict = verdictBlocked
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence, err.Error())
	}

	debugStage := runFixtureCommand(ctx, fixtureRoot, "preflight.cmd", "debug")
	evidence.DebugStages = append(evidence.DebugStages, debugStage)
	if debugStage.Verdict != verdictPass {
		evidence.DebugVerdict = debugStage.Verdict
		if err := e.recordProviderStop(ctx, fixture, &evidence); err != nil {
			evidence.RuntimeVerdict = verdictFail
			return e.finishProvider(fixture, evidence, debugStage.Error+"; cleanup: "+err.Error())
		}
		return e.finishProvider(fixture, evidence, debugStage.Error)
	}
	if err := e.captureProviderBreakpoint(ctx, fixture, &evidence); err != nil {
		evidence.DebugVerdict = verdictFail
		if cleanupErr := e.recordProviderStop(ctx, fixture, &evidence); cleanupErr != nil {
			evidence.RuntimeVerdict = verdictFail
			err = fmt.Errorf("%w; cleanup: %v", err, cleanupErr)
		}
		return e.finishProvider(fixture, evidence, err.Error())
	}
	if err := e.recordProviderStop(ctx, fixture, &evidence); err != nil {
		evidence.RuntimeVerdict = verdictFail
		return e.finishProvider(fixture, evidence, err.Error())
	}
	return e.finishProvider(fixture, evidence, "")
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
	env["FIXTURE_CAMPAIGN_ID"] = fmt.Sprint(e.variables["campaign_id"])
	return config, env, nil
}

func (e *ScenarioExecutor) configureAndStartProvider(ctx context.Context, fixture FixtureManifest, config map[string]any, env map[string]string, evidence *providerEvidence) error {
	projectID := fmt.Sprint(e.variables["project_id"])
	projectRoot := fmt.Sprint(e.variables["project_root"])
	serviceID := "provider-" + fixture.Provider
	deploymentID := serviceID + "-dev"
	// 在任何写操作前固定资源身份，后续失败路径才能只停止本 campaign 的 deployment。
	e.variables["provider_"+fixture.Provider+"_deployment_id"] = deploymentID
	codeDebug := providerCodeDebugConfig(fixture.Provider)
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
		result, err := e.client.CallTool(ctx, call.tool, call.args)
		if err != nil || result.IsError {
			if err == nil {
				err = fmt.Errorf("%s", toolErrorCode(result))
			}
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: call.stage, Verdict: verdictFail, Error: err.Error()})
			return fmt.Errorf("provider %s %s: %w", fixture.Provider, call.stage, err)
		}
		evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: call.stage, Verdict: verdictPass, Summary: e.redactor.Redact(RawMessageMap(result))})
	}
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		state, err := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		if err == nil && !state.IsError && deploymentStatus(state, deploymentID) == "running" {
			e.variables["provider_"+fixture.Provider+"_deployment_id"] = deploymentID
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: "running_state", Verdict: verdictPass, Summary: map[string]any{"deployment_id": deploymentID, "status": "running"}})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("provider %s deployment did not reach running", fixture.Provider)
}

func providerCodeDebugConfig(provider string) map[string]any {
	config := map[string]any{"policy": "auto"}
	switch provider {
	case "go":
		if path, err := exec.LookPath("dlv"); err == nil {
			config["adapter_command"] = path
		}
	case "java", "kotlin":
		path := strings.TrimSpace(os.Getenv("SUPERDEV_JVM_ADAPTER_COMMAND"))
		if path != "" {
			config["adapter_command"] = path
		}
	case "rust", "cpp":
		if path, err := exec.LookPath("lldb-dap"); err == nil {
			config["adapter_command"] = path
		}
	}
	return config
}

func (e *ScenarioExecutor) probeFixtureRuntime(ctx context.Context, fixture FixtureManifest, evidence *providerEvidence) error {
	status, body, err := fixtureRequest(ctx, "GET", fixture.Readiness.URL, "", nil)
	if err != nil || status != fixture.Readiness.Status {
		return fmt.Errorf("provider %s readiness status=%d: %w", fixture.Provider, status, err)
	}
	if err := assertFixtureJSON(body, map[string]any{"ready": true, "provider": fixture.Provider}); err != nil {
		return fmt.Errorf("provider %s readiness body: %w", fixture.Provider, err)
	}
	evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: "readiness", Verdict: verdictPass, Summary: map[string]any{"status": status, "body_sha256": digestText(body)}})
	for _, probe := range []struct {
		name    string
		outcome string
		status  int
	}{
		{"normal_probe", "ok", 200}, {"controlled_error", "error", 500},
	} {
		status, body, err = e.performFixtureProbe(ctx, fixture, probe.outcome)
		if err != nil || status != probe.status {
			return fmt.Errorf("provider %s %s status=%d: %w", fixture.Provider, probe.name, status, err)
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
		if err := assertFixtureJSON(body, expected); err != nil {
			return fmt.Errorf("provider %s %s body: %w", fixture.Provider, probe.name, err)
		}
		evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: probe.name, Verdict: verdictPass, Summary: map[string]any{"status": status, "body_sha256": digestText(body)}})
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
	result, err := e.client.CallTool(ctx, "debug_capture_at", map[string]any{
		"deployment_id": deploymentID, "source": fixture.Debug.Source, "line": fixture.Debug.Line,
		"timeout_ms": 45000, "max_variables": 50, "variable_names": fixture.Debug.Variables,
		"approval_wait_seconds": 300,
	})
	<-triggerDone
	if err != nil {
		return fmt.Errorf("provider %s debug capture: %w", fixture.Provider, err)
	}
	if result.IsError {
		return fmt.Errorf("provider %s debug capture: %s", fixture.Provider, toolErrorCode(result))
	}
	canonical := CanonicalJSON(RawMessageMap(result))
	for _, variable := range fixture.Debug.Variables {
		if !strings.Contains(canonical, variable) {
			return fmt.Errorf("provider %s debug capture omitted variable %s", fixture.Provider, variable)
		}
	}
	for name, expected := range fixture.Debug.Expected {
		if !strings.Contains(canonical, name) || !strings.Contains(canonical, fmt.Sprint(expected)) {
			return fmt.Errorf("provider %s debug capture omitted expected %s=%v", fixture.Provider, name, expected)
		}
	}
	evidence.DebugStages = append(evidence.DebugStages, stageEvidence{Stage: "debug_capture_at", Verdict: verdictPass, Summary: e.redactor.Redact(RawMessageMap(result))})
	return nil
}

func (e *ScenarioExecutor) stopProvider(ctx context.Context, fixture FixtureManifest) error {
	projectID := fmt.Sprint(e.variables["project_id"])
	deploymentID := fmt.Sprint(e.variables["provider_"+fixture.Provider+"_deployment_id"])
	if deploymentID == "" {
		return nil
	}
	result, err := e.client.CallTool(ctx, "stop_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300})
	if err != nil {
		return err
	}
	if result.IsError {
		return fmt.Errorf("stop provider %s: %s", fixture.Provider, toolErrorCode(result))
	}
	return nil
}

func (e *ScenarioExecutor) recordProviderStop(ctx context.Context, fixture FixtureManifest, evidence *providerEvidence) error {
	err := e.stopProvider(ctx, fixture)
	if err != nil {
		evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: "stop_service", Verdict: verdictFail, Error: err.Error()})
		return err
	}
	projectID := fmt.Sprint(e.variables["project_id"])
	deploymentID := fmt.Sprint(e.variables["provider_"+fixture.Provider+"_deployment_id"])
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, callErr := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
			evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: "stopped_state", Verdict: verdictPass, Summary: map[string]any{"deployment_id": deploymentID, "status": "stopped"}})
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	err = fmt.Errorf("provider %s deployment did not reach stopped", fixture.Provider)
	evidence.RuntimeStages = append(evidence.RuntimeStages, stageEvidence{Stage: "stopped_state", Verdict: verdictFail, Error: err.Error()})
	return err
}

func (e *ScenarioExecutor) finishProvider(fixture FixtureManifest, evidence providerEvidence, reason string) ProviderExecution {
	log := logger.GetLogger().WithEntryName("WindowsValidationProvider")
	relative := filepath.ToSlash(filepath.Join("evidence", "providers", fixture.Provider+".json"))
	path := filepath.Join(e.resultsDir, filepath.FromSlash(relative))
	if err := writeJSON(path, e.redactor.Redact(RawMessageMap(evidence))); err != nil {
		evidence.RuntimeVerdict = verdictFail
		reason = "write provider evidence: " + err.Error()
		log.WithErr(err).WithField("provider", fixture.Provider).Error("Windows language provider 证据写入失败")
	}
	result := ProviderExecution{Provider: fixture.Provider, RuntimeVerdict: evidence.RuntimeVerdict, DebugVerdict: evidence.DebugVerdict, EvidencePath: relative, Reason: reason}
	fields := map[string]any{"provider": fixture.Provider, "runtime": result.RuntimeVerdict, "debug": result.DebugVerdict}
	if result.RuntimeVerdict == verdictFail || result.DebugVerdict == verdictFail {
		log.WithFields(fields).WithField("reason", reason).Error("Windows language provider 合同执行失败")
	} else if result.RuntimeVerdict == verdictBlocked || result.DebugVerdict == verdictBlocked {
		log.WithFields(fields).WithField("reason", reason).Info("Windows language provider 合同执行受阻")
	} else {
		log.WithFields(fields).Info("Windows language provider 合同执行完成")
	}
	return result
}

func runFixtureCommand(ctx context.Context, directory, command string, args ...string) stageEvidence {
	stage := strings.TrimSuffix(strings.ToLower(filepath.Base(command)), filepath.Ext(command))
	provider := filepath.Base(directory)
	log := logger.GetLogger().WithEntryName("WindowsValidationFixtureCommand")
	log.WithFields(map[string]any{"provider": provider, "stage": stage, "command": filepath.Base(command)}).Info("开始执行 Windows fixture 外部进程")
	cmdArgs := []string{"/d", "/s", "/c", "call", command}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.CommandContext(ctx, "cmd.exe", cmdArgs...)
	cmd.Dir = directory
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err == nil {
		log.WithFields(map[string]any{"provider": provider, "stage": stage, "command": filepath.Base(command), "exit_code": 0}).Info("Windows fixture 外部进程执行完成")
		return stageEvidence{Stage: stage, Verdict: verdictPass, Summary: map[string]any{"command": filepath.Base(command), "output": output.String()}}
	}
	exitCode := 1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	verdict := verdictFail
	if exitCode == 10 || exitCode == 20 {
		verdict = verdictBlocked
	}
	log.WithErr(err).WithFields(map[string]any{"provider": provider, "stage": stage, "command": filepath.Base(command), "exit_code": exitCode, "verdict": verdict}).Error("Windows fixture 外部进程执行失败")
	return stageEvidence{Stage: stage, Verdict: verdict, ExitCode: exitCode, Error: strings.TrimSpace(output.String())}
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
