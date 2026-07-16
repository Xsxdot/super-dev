// provider.go 参数化执行七语言 fixture 的 runtime 与真实 DAP debug phase machine。
//
// 职责：
//   - 复用同一 provider schema/config/start/probe/stop 编排
//   - 独立呈现 runtime 与 debug 结论，并验证断点 stack/scopes/variables
//   - 把 MCP 调用记录为 supporting evidence，primary 仍只由 scenario manifest 分配
//
// 边界：
//   - 不注册 project；必须复用 manifest bootstrap 捕获的 project_id
//   - 不绕过 MCP 管理服务，也不在 adapter 缺失时伪造 debug PASS
package runtimevalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

// ProviderPhaseOrder 是每种语言必须完整呈现的 runtime/debug phase 顺序。
var ProviderPhaseOrder = []string{
	"preflight.runtime", "build", "runtime.start", "runtime.ready", "runtime.normal_probe",
	"runtime.controlled_error_probe", "runtime.stop", "preflight.debug", "debug.breakpoint",
	"debug.stack_and_variables", "debug.stop",
}

// ProviderPhaseResult 保存一个语言 phase 的状态与具名根因。
type ProviderPhaseResult struct {
	Phase  string `json:"phase"`
	Status Status `json:"status"`
	Cause  Cause  `json:"cause,omitempty"`
}

// LanguageResult 保存一个 provider 的 runtime/debug 独立结论和 supporting MCP evidence。
type LanguageResult struct {
	Provider        string                `json:"provider"`
	RuntimeStatus   Status                `json:"runtime_status"`
	RuntimeCause    Cause                 `json:"runtime_cause,omitempty"`
	DebugStatus     Status                `json:"debug_status"`
	DebugCause      Cause                 `json:"debug_cause,omitempty"`
	Phases          []ProviderPhaseResult `json:"phases"`
	SupportingCalls []ToolEvidence        `json:"supporting_calls"`
}

// ProviderMatrixRequest 提交七语言共享的 campaign/project/platform 和动态端口。
type ProviderMatrixRequest struct {
	CampaignID      string
	ProjectID       string
	ProjectRoot     string
	Platform        string
	Fixtures        []Fixture
	Ports           map[string]int
	AdapterPaths    map[string]string
	AdapterCommands map[string]string
	ApprovalToken   string
	Cleanup         *CleanupStack
}

// ProviderRequest 提交一个语言 provider 的 strict runtime/debug 输入。
type ProviderRequest struct {
	CampaignID     string
	ProjectID      string
	ProjectRoot    string
	Platform       string
	Fixture        Fixture
	Port           int
	AdapterPath    string
	AdapterCommand string
	ApprovalToken  string
	Cleanup        *CleanupStack
}

// CommandRunRequest 描述一个 fixture preflight/build 外部调用。
type CommandRunRequest struct {
	Name      string
	Command   CommandSpec
	Directory string
	Env       map[string]string
}

// CommandExecutor 执行 fixture 声明的 target-native preflight/build 命令。
type CommandExecutor interface {
	Run(ctx context.Context, request CommandRunRequest) error
}

// HTTPProbeRequest 描述 loopback fixture 的一次语义 HTTP probe。
type HTTPProbeRequest struct {
	URL   string
	Probe HTTPProbe
	Quiet bool
}

// HTTPProber 执行 readiness、正常或受控错误 HTTP probe。
type HTTPProber interface {
	Probe(ctx context.Context, request HTTPProbeRequest) error
}

// ProviderRunner 协调 MCP、target-native build 与 loopback HTTP probe。
type ProviderRunner struct {
	tools                ToolCaller
	commands             CommandExecutor
	http                 HTTPProber
	debugTriggerDelay    time.Duration
	debugTriggerInterval time.Duration
}

// NewProviderRunner 创建参数化 provider runner。
//
// 参数：
//   - tools: 真实 packaged MCP ToolCaller
//   - commands: fixture preflight/build executor；nil 使用 OS executor
//   - prober: loopback HTTP prober；nil 使用标准库客户端
//
// 返回：
//   - 不拥有 project 注册能力的 ProviderRunner
func NewProviderRunner(tools ToolCaller, commands CommandExecutor, prober HTTPProber) *ProviderRunner {
	if commands == nil {
		commands = NewOSCommandExecutor(io.Discard)
	}
	if prober == nil {
		prober = &DefaultHTTPProber{Client: &http.Client{Timeout: 10 * time.Second}}
	}
	return &ProviderRunner{
		tools: tools, commands: commands, http: prober,
		debugTriggerDelay: 100 * time.Millisecond, debugTriggerInterval: 250 * time.Millisecond,
	}
}

// RunMatrix 依次执行七语言 fixtures，并为缺失 provider 产生显式 FAIL result。
//
// 参数：
//   - ctx: matrix deadline
//   - request: bootstrap project_id、platform、fixtures、ports 和 adapter paths
//
// 返回：
//   - 按 provider 名排序的七个独立 LanguageResult
//
// 注意：差异只来自 fixture/platform/adapter；matrix 不自行注册 project。
func (r *ProviderRunner) RunMatrix(ctx context.Context, request ProviderMatrixRequest) []LanguageResult {
	fixtures := append([]Fixture{}, request.Fixtures...)
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Provider < fixtures[j].Provider })
	results := make([]LanguageResult, 0, len(fixtures))
	for _, fixture := range fixtures {
		adapterPath := request.AdapterPaths[fixture.Debug.AdapterResource]
		adapterCommand := request.AdapterCommands[fixture.Debug.AdapterResource]
		if adapterCommand == "" {
			adapterCommand = adapterPath
		}
		results = append(results, r.Run(ctx, ProviderRequest{
			CampaignID: request.CampaignID, ProjectID: request.ProjectID, ProjectRoot: request.ProjectRoot,
			Platform: request.Platform, Fixture: fixture, Port: request.Ports[fixture.Provider],
			AdapterPath: adapterPath, AdapterCommand: adapterCommand,
			ApprovalToken: request.ApprovalToken, Cleanup: request.Cleanup,
		}))
	}
	return results
}

// Run 执行一个语言 fixture 的完整 runtime/debug phase machine。
//
// 参数：
//   - ctx: provider deadline
//   - request: 已捕获 project_id、fixture、platform、动态端口和 adapter
//
// 返回：
//   - runtime/debug 独立状态、全部 phase 和 supporting MCP calls
//
// 注意：runtime 失败会让 debug 成为具名 NOT_RUN；debug BLOCKED 不抹掉 runtime PASS。
func (r *ProviderRunner) Run(ctx context.Context, request ProviderRequest) LanguageResult {
	result := newLanguageResult(request.Fixture.Provider)
	log := logger.GetLogger().WithEntryName("RuntimeValidationProvider").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "project_id": request.ProjectID,
		"provider": request.Fixture.Provider, "platform": request.Platform,
	})
	log.Info("开始执行语言 runtime/debug provider")
	if strings.TrimSpace(request.CampaignID) == "" || strings.TrimSpace(request.ProjectID) == "" || strings.TrimSpace(request.ProjectRoot) == "" {
		return failProviderFrom(&result, 0, StatusFail, "provider_input_invalid", "campaign_id, project_id and project_root are required")
	}
	platform, ok := request.Fixture.Platforms[request.Platform]
	if !ok || request.Port <= 0 {
		return failProviderFrom(&result, 0, StatusBlocked, "provider_preflight_blocked", "platform contract or dynamic port is missing")
	}
	if err := ValidateFixture(request.Fixture); err != nil {
		return failProviderFrom(&result, 0, StatusFail, "fixture_invalid", err.Error())
	}
	fixtureRoot := filepath.Join(request.ProjectRoot, "fixtures", request.Fixture.Provider)
	env := providerEnvironment(request)
	if err := prepareProviderEnvironment(request, env); err != nil {
		return failProviderFrom(&result, 0, StatusFail, "provider_environment_prepare_failed", err.Error())
	}
	if err := r.commands.Run(ctx, CommandRunRequest{Name: request.Fixture.Provider + "-preflight", Command: platform.Preflight, Directory: fixtureRoot, Env: env}); err != nil {
		return failProviderFrom(&result, 0, StatusBlocked, "runtime_toolchain_unavailable", err.Error())
	}
	setProviderPhase(&result, 0, StatusPass, Cause{})

	if err := r.commands.Run(ctx, CommandRunRequest{Name: request.Fixture.Provider + "-build", Command: platform.Build, Directory: fixtureRoot, Env: env}); err != nil {
		return failProviderFrom(&result, 1, StatusFail, "fixture_build_failed", err.Error())
	}
	setProviderPhase(&result, 1, StatusPass, Cause{})

	service := providerServiceConfig(request, platform)
	if err := r.configureProvider(ctx, request, service, &result); err != nil {
		return failProviderFrom(&result, 2, StatusFail, "runtime_config_failed", err.Error())
	}
	runtimeKey := request.Fixture.Provider + ":runtime"
	if err := r.startService(ctx, request, runtimeKey, &result); err != nil {
		return failProviderFrom(&result, 2, StatusFail, "runtime_start_failed", err.Error())
	}
	setProviderPhase(&result, 2, StatusPass, Cause{})

	baseURL := "http://127.0.0.1:" + strconv.Itoa(request.Port)
	if err := r.waitForReadiness(ctx, request, baseURL, "runtime"); err != nil {
		_ = r.stopService(ctx, request, runtimeKey, &result)
		return failProviderFrom(&result, 3, StatusFail, "runtime_readiness_failed", err.Error())
	}
	setProviderPhase(&result, 3, StatusPass, Cause{})
	if err := r.http.Probe(ctx, HTTPProbeRequest{URL: baseURL + request.Fixture.NormalProbe.Path, Probe: request.Fixture.NormalProbe}); err != nil {
		_ = r.stopService(ctx, request, runtimeKey, &result)
		return failProviderFrom(&result, 4, StatusFail, "runtime_normal_probe_failed", err.Error())
	}
	setProviderPhase(&result, 4, StatusPass, Cause{})
	if err := r.http.Probe(ctx, HTTPProbeRequest{URL: baseURL + request.Fixture.ControlledErrorProbe.Path, Probe: request.Fixture.ControlledErrorProbe}); err != nil {
		_ = r.stopService(ctx, request, runtimeKey, &result)
		return failProviderFrom(&result, 5, StatusFail, "runtime_controlled_error_probe_failed", err.Error())
	}
	setProviderPhase(&result, 5, StatusPass, Cause{})
	if err := r.stopService(ctx, request, runtimeKey, &result); err != nil {
		return failProviderFrom(&result, 6, StatusFail, "runtime_stop_failed", err.Error())
	}
	setProviderPhase(&result, 6, StatusPass, Cause{})
	result.RuntimeStatus = StatusPass

	if request.AdapterPath != "" {
		if _, err := os.Stat(request.AdapterPath); err != nil {
			return blockProviderDebug(&result, "debug_adapter_unavailable", err.Error())
		}
	}
	debugKey := request.Fixture.Provider + ":debug"
	if err := r.startService(ctx, request, debugKey, &result); err != nil {
		return failProviderDebug(&result, 7, "debug_runtime_start_failed", err.Error())
	}
	// debug 使用 stop 后重新拉起的新进程；runtime 阶段的 readiness 不能证明新 PID 已监听。
	// 在 attach 前重新等待，避免 adapter 与业务进程启动时序竞争。
	if err := r.waitForReadiness(ctx, request, baseURL, "debug"); err != nil {
		_ = r.stopService(ctx, request, debugKey, &result)
		return failProviderDebug(&result, 7, "debug_runtime_readiness_failed", err.Error())
	}
	setProviderPhase(&result, 7, StatusPass, Cause{})

	variableNames := make([]string, 0, len(request.Fixture.Debug.ExpectedVariables))
	for name := range request.Fixture.Debug.ExpectedVariables {
		variableNames = append(variableNames, name)
	}
	sort.Strings(variableNames)
	triggerCtx, cancelTrigger := context.WithCancel(ctx)
	triggerDone := make(chan debugTriggerResult, 1)
	go func() {
		triggerDone <- r.pumpDebugTrigger(triggerCtx, request, baseURL)
	}()
	debugArguments := map[string]any{
		"deployment_id": providerDeploymentID(request.Fixture.Provider), "source": request.Fixture.Debug.Source,
		"line": request.Fixture.Debug.Line, "timeout_ms": 30000, "max_variables": len(variableNames) + 8,
		"variable_names": variableNames, "approval_wait_seconds": 300,
	}
	debugResult, err := r.callSupporting(ctx, request, &result, "debug.breakpoint", "debug_capture_at", debugArguments)
	cancelTrigger()
	triggerResult := <-triggerDone
	if err != nil {
		_ = r.stopService(ctx, request, debugKey, &result)
		return failProviderDebug(&result, 8, "debug_breakpoint_failed", err.Error())
	}
	logger.GetLogger().WithEntryName("RuntimeValidationProviderDebugTrigger").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "provider": request.Fixture.Provider,
		"attempts": triggerResult.Attempts, "completed_responses": triggerResult.CompletedResponses,
	}).Info("断点触发请求循环已随 capture 结果停止")
	data := structuredData(debugResult)
	sessionID, _ := data["session_id"].(string)
	threadID := integerValue(data["thread_id"])
	frameID := integerValue(data["frame_id"])
	threadIdentityValid := threadID > 0
	if request.Fixture.Debug.Provider == "node" && threadID == 0 && numericIdentity(data["thread_id"]) {
		// js-debug reverse-request child session 使用 0 作为合法 thread identity；
		// presence + numeric type 仍能区分合同缺字段和真实的 zero identity。
		threadIdentityValid = true
	}
	if sessionID == "" || !threadIdentityValid || frameID <= 0 {
		_ = r.stopService(ctx, request, debugKey, &result)
		return failProviderDebug(&result, 8, "debug_breakpoint_evidence_incomplete", "debug_capture_at did not return session/thread/frame identity")
	}
	setProviderPhase(&result, 8, StatusPass, Cause{})
	if err := validateDebugVariables(data["variables"], request.Fixture.Debug.ExpectedVariables); err != nil {
		_ = r.stopService(ctx, request, debugKey, &result)
		return failProviderDebug(&result, 9, "debug_variables_mismatch", err.Error())
	}
	setProviderPhase(&result, 9, StatusPass, Cause{})
	if _, err := r.callSupporting(ctx, request, &result, "debug.stop", "debug_continue", map[string]any{
		"deployment_id": providerDeploymentID(request.Fixture.Provider), "thread_id": threadID,
	}); err != nil {
		_ = r.stopService(ctx, request, debugKey, &result)
		return failProviderDebug(&result, 10, "debug_continue_failed", err.Error())
	}
	if err := r.stopService(ctx, request, debugKey, &result); err != nil {
		return failProviderDebug(&result, 10, "debug_stop_failed", err.Error())
	}
	setProviderPhase(&result, 10, StatusPass, Cause{})
	result.DebugStatus = StatusPass
	log.WithFields(map[string]any{"runtime_status": result.RuntimeStatus, "debug_status": result.DebugStatus}).Info("语言 runtime/debug provider 执行完成")
	return result
}

type debugTriggerResult struct {
	Attempts           int
	CompletedResponses int
	LastError          error
}

// pumpDebugTrigger 在 debug_capture_at 设置断点期间重复触发 fixture 请求。
//
// 首次请求可能早于 adapter 完成 attach/setBreakpoints，因此成功响应不能视为已触发断点；
// 循环持续到 capture 返回并取消 context。命中断点的请求会阻塞，取消后由后续 debug_continue 恢复服务端 handler。
func (r *ProviderRunner) pumpDebugTrigger(ctx context.Context, request ProviderRequest, baseURL string) debugTriggerResult {
	result := debugTriggerResult{}
	log := logger.GetLogger().WithEntryName("RuntimeValidationProviderDebugTrigger").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "provider": request.Fixture.Provider,
	})
	log.Info("开始循环触发 fixture 断点请求")
	if r.debugTriggerDelay > 0 {
		timer := time.NewTimer(r.debugTriggerDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result
		case <-timer.C:
		}
	}
	interval := r.debugTriggerInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	for {
		result.Attempts++
		err := r.http.Probe(ctx, HTTPProbeRequest{
			URL: baseURL + request.Fixture.NormalProbe.Path, Probe: request.Fixture.NormalProbe, Quiet: true,
		})
		if err == nil {
			result.CompletedResponses++
		} else {
			result.LastError = err
		}
		if ctx.Err() != nil {
			return result
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result
		case <-timer.C:
		}
	}
}

// OSCommandExecutor 使用 ManagedProcess 执行 target-native preflight/build，并把输出交给脱敏 sink。
type OSCommandExecutor struct {
	Output io.Writer
}

// NewOSCommandExecutor 创建一个不经 shell 的 fixture command executor。
func NewOSCommandExecutor(output io.Writer) *OSCommandExecutor {
	if output == nil {
		output = io.Discard
	}
	return &OSCommandExecutor{Output: output}
}

// Run 执行并等待一个 fixture preflight/build 命令。
func (e *OSCommandExecutor) Run(ctx context.Context, request CommandRunRequest) error {
	executable := request.Command.Executable
	if !filepath.IsAbs(executable) {
		resolved, err := exec.LookPath(executable)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", executable, err)
		}
		executable = resolved
	}
	env := map[string]string{}
	for key, value := range request.Env {
		env[key] = value
	}
	for key, value := range request.Command.Env {
		env[key] = value
	}
	process, err := StartManagedProcess(ctx, ProcessSpec{
		Name: request.Name, Executable: executable, Arguments: request.Command.Arguments,
		Directory: request.Directory, Env: env, Stdout: e.Output, Stderr: e.Output,
	})
	if err != nil {
		return err
	}
	if err := process.Wait(ctx); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), processGracePeriod)
		defer cancel()
		_ = process.Close(cleanupCtx)
		return err
	}
	return nil
}

// DefaultHTTPProber 使用标准库客户端验证 loopback fixture status 和顶层业务字段。
type DefaultHTTPProber struct {
	Client *http.Client
}

// Probe 执行一次 HTTP 请求并验证 expected status/fields。
func (p *DefaultHTTPProber) Probe(ctx context.Context, request HTTPProbeRequest) error {
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	httpRequest, err := http.NewRequestWithContext(ctx, request.Probe.Method, request.URL, nil)
	if err != nil {
		return err
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationProviderHTTP").WithFields(map[string]any{"method": request.Probe.Method, "url": request.URL, "expected_status": request.Probe.ExpectedStatus})
	if !request.Quiet {
		log.Info("开始调用 fixture loopback HTTP probe")
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		if !request.Quiet {
			log.WithErr(err).Error("fixture loopback HTTP probe 失败")
		}
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != request.Probe.ExpectedStatus {
		return fmt.Errorf("fixture probe status=%d, want %d", response.StatusCode, request.Probe.ExpectedStatus)
	}
	if len(request.Probe.ExpectedFields) > 0 {
		var body map[string]any
		if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
			return fmt.Errorf("decode fixture probe: %w", err)
		}
		for key, expected := range request.Probe.ExpectedFields {
			if fmt.Sprint(body[key]) != fmt.Sprint(expected) {
				return fmt.Errorf("fixture probe field %s=%v, want %v", key, body[key], expected)
			}
		}
	}
	if !request.Quiet {
		log.WithField("status", response.StatusCode).Info("fixture loopback HTTP probe 成功")
	}
	return nil
}

func (r *ProviderRunner) waitForReadiness(ctx context.Context, request ProviderRequest, baseURL, phase string) error {
	const (
		readinessTimeout      = 30 * time.Second
		readinessPollInterval = 100 * time.Millisecond
	)
	probe := HTTPProbeRequest{
		URL: baseURL + request.Fixture.Readiness.Path, Probe: request.Fixture.Readiness, Quiet: true,
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationProviderReadiness").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "provider": request.Fixture.Provider,
		"phase": phase, "url": probe.URL, "timeout": readinessTimeout.String(),
	})
	log.Info("开始等待异步 provider readiness")
	readinessCtx, cancel := context.WithTimeout(ctx, readinessTimeout)
	defer cancel()
	var lastErr error
	for attempts := 1; ; attempts++ {
		if err := r.http.Probe(readinessCtx, probe); err == nil {
			log.WithField("attempts", attempts).Info("异步 provider readiness 已确认")
			return nil
		} else {
			lastErr = err
		}
		timer := time.NewTimer(readinessPollInterval)
		select {
		case <-readinessCtx.Done():
			timer.Stop()
			log.WithErr(lastErr).WithField("attempts", attempts).Error("等待异步 provider readiness 失败")
			return fmt.Errorf("wait for %s readiness after %d attempts: %v; last probe: %w", phase, attempts, readinessCtx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func (r *ProviderRunner) configureProvider(ctx context.Context, request ProviderRequest, service map[string]any, result *LanguageResult) error {
	if _, err := r.callSupporting(ctx, request, result, "runtime.start", "describe_language_runtime_schema", map[string]any{"language": request.Fixture.Provider}); err != nil {
		return err
	}
	config := providerRuntimeConfig(request, request.Fixture.Platforms[request.Platform])
	if _, err := r.callSupporting(ctx, request, result, "runtime.start", "validate_service_runtime", map[string]any{
		"language": request.Fixture.Provider, "project_root": request.ProjectRoot,
		"cwd": filepath.ToSlash(filepath.Join("fixtures", request.Fixture.Provider)), "env": providerEnvironment(request), "config": config,
	}); err != nil {
		return err
	}
	change := map[string]any{
		"kind": "config.service.upsert", "project_id": request.ProjectID, "root_path": request.ProjectRoot, "service": service,
	}
	if _, err := r.callSupporting(ctx, request, result, "runtime.start", "preview_config_change", change); err != nil {
		return err
	}
	apply := cloneMap(change)
	if request.ApprovalToken != "" {
		apply["approval_token"] = request.ApprovalToken
	}
	_, err := r.callSupporting(ctx, request, result, "runtime.start", "apply_config_change", apply)
	return err
}

func (r *ProviderRunner) startService(ctx context.Context, request ProviderRequest, acquisitionID string, result *LanguageResult) error {
	start := func() (CleanupAction, error) {
		_, err := r.callSupporting(ctx, request, result, "runtime.start", "start_service", map[string]any{
			"project_id": request.ProjectID, "deployment_id": providerDeploymentID(request.Fixture.Provider), "approval_wait_seconds": 300,
		})
		if err != nil {
			return nil, err
		}
		return &providerStopAction{runner: r, request: request, result: result, acquisitionID: acquisitionID}, nil
	}
	if request.Cleanup == nil {
		_, err := start()
		return err
	}
	_, err := request.Cleanup.Acquire("service-runtime", acquisitionID, map[string]any{"state": "stopped"}, start)
	return err
}

func (r *ProviderRunner) stopService(ctx context.Context, request ProviderRequest, acquisitionID string, result *LanguageResult) error {
	if request.Cleanup != nil {
		return request.Cleanup.ReleaseTracked(ctx, "service-runtime", acquisitionID)
	}
	_, err := r.callSupporting(ctx, request, result, "runtime.stop", "stop_service", map[string]any{
		"project_id": request.ProjectID, "deployment_id": providerDeploymentID(request.Fixture.Provider), "approval_wait_seconds": 300,
	})
	return err
}

type providerStopAction struct {
	runner        *ProviderRunner
	request       ProviderRequest
	result        *LanguageResult
	acquisitionID string
}

func (a *providerStopAction) Kind() string { return "service-runtime" }
func (a *providerStopAction) ID() string   { return a.acquisitionID }
func (a *providerStopAction) Release(ctx context.Context) error {
	_, err := a.runner.callSupporting(ctx, a.request, a.result, "cleanup", "stop_service", map[string]any{
		"project_id": a.request.ProjectID, "deployment_id": providerDeploymentID(a.request.Fixture.Provider), "approval_wait_seconds": 300,
	})
	return err
}

func (r *ProviderRunner) callSupporting(ctx context.Context, request ProviderRequest, result *LanguageResult, phase, tool string, arguments map[string]any) (ToolCallResult, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationProviderMCP").WithFields(map[string]any{"campaign_id": request.CampaignID, "provider": request.Fixture.Provider, "phase": phase, "tool": tool})
	log.Info("开始 provider supporting MCP 调用")
	response, err := r.tools.CallTool(ctx, tool, arguments)
	if err != nil {
		log.WithErr(err).Error("provider supporting MCP 调用失败")
		return response, err
	}
	structured := RawMessageMap(response.StructuredContent)
	if response.IsError || structured["ok"] == false {
		applicationErr := toolApplicationError(tool, response)
		log.WithErr(applicationErr).Error("provider supporting MCP 返回应用错误")
		return response, applicationErr
	}
	result.SupportingCalls = append(result.SupportingCalls, ToolEvidence{
		CampaignID: request.CampaignID, ScenarioID: "provider:" + request.Fixture.Provider,
		StepID: phase + ":" + tool, Tool: tool, Outcome: ExpectedOutcomeSuccess,
		Assertions: []AssertionResult{{Path: "tools/call.success", Passed: true}},
	})
	log.Info("provider supporting MCP 调用成功")
	return response, nil
}

func providerServiceConfig(request ProviderRequest, platform FixturePlatform) map[string]any {
	adapterCommand := request.AdapterCommand
	if adapterCommand == "" {
		adapterCommand = request.AdapterPath
	}
	return map[string]any{
		"id": providerServiceID(request.Fixture.Provider), "name": "runtime-validation-" + request.Fixture.Provider,
		"language": request.Fixture.Provider, "required": true, "order": 1,
		"deployments": []any{map[string]any{
			"id": providerDeploymentID(request.Fixture.Provider), "env_name": "validation", "location": "local", "control_mode": "managed",
			"runtime": map[string]any{
				"type": "language", "cwd": filepath.ToSlash(filepath.Join("fixtures", request.Fixture.Provider)),
				"env": providerEnvironment(request), "config": providerRuntimeConfig(request, platform),
			},
			"logs": map[string]any{"type": "process"},
			"readiness": map[string]any{
				"type": "http", "target": "http://127.0.0.1:" + strconv.Itoa(request.Port) + request.Fixture.Readiness.Path, "timeout_seconds": 30,
			},
			// adapter_command 绑定 active marker 前已检查的 target-native launcher，禁止运行期回退 PATH 或下载。
			"code_debug": map[string]any{"policy": "auto", "adapter_command": adapterCommand},
		}},
	}
}

func providerRuntimeConfig(request ProviderRequest, platform FixturePlatform) map[string]any {
	config := cloneMap(request.Fixture.Runtime.Config)
	if request.Fixture.Provider == "rust" || request.Fixture.Provider == "cpp" {
		config["program"] = filepath.ToSlash(platform.Executable)
	}
	return config
}

func providerEnvironment(request ProviderRequest) map[string]string {
	env := map[string]string{}
	for key, value := range request.Fixture.Runtime.Env {
		env[key] = strings.ReplaceAll(strings.ReplaceAll(value, "${PORT}", strconv.Itoa(request.Port)), "${CAMPAIGN_ID}", request.CampaignID)
	}
	if request.Fixture.Provider == "go" && strings.TrimSpace(request.ProjectRoot) != "" {
		// Go 默认缓存位于用户目录；strict campaign 必须把 build、runtime 和 Delve
		// 统一约束到随 campaign workdir 一起清理的目录，避免读写宿主机持久状态。
		env["GOCACHE"] = filepath.Join(request.ProjectRoot, ".runtime-validation-cache", "go-build")
	}
	return env
}

func prepareProviderEnvironment(request ProviderRequest, env map[string]string) error {
	cacheRoot := strings.TrimSpace(env["GOCACHE"])
	if request.Fixture.Provider != "go" || cacheRoot == "" {
		return nil
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationProviderEnvironment").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "provider": request.Fixture.Provider, "cache_root": cacheRoot,
	})
	log.Info("开始准备 campaign-owned provider cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		log.WithErr(err).Error("准备 campaign-owned provider cache 失败")
		return fmt.Errorf("prepare campaign-owned Go cache: %w", err)
	}
	log.Info("campaign-owned provider cache 准备完成")
	return nil
}

func providerServiceID(provider string) string    { return "runtime-validation-" + provider }
func providerDeploymentID(provider string) string { return providerServiceID(provider) + "-validation" }

func newLanguageResult(provider string) LanguageResult {
	result := LanguageResult{Provider: provider, RuntimeStatus: StatusNotRun, DebugStatus: StatusNotRun, Phases: make([]ProviderPhaseResult, len(ProviderPhaseOrder))}
	for index, phase := range ProviderPhaseOrder {
		result.Phases[index] = ProviderPhaseResult{Phase: phase, Status: StatusNotRun}
	}
	return result
}

func setProviderPhase(result *LanguageResult, index int, status Status, cause Cause) {
	result.Phases[index].Status = status
	result.Phases[index].Cause = cause
	fields := map[string]any{"provider": result.Provider, "phase": result.Phases[index].Phase, "status": status}
	if status == StatusPass {
		logger.GetLogger().WithEntryName("RuntimeValidationProviderStage").WithFields(fields).Info("provider phase 完成")
		return
	}
	logger.GetLogger().WithEntryName("RuntimeValidationProviderStage").WithFields(fields).WithField("cause_code", cause.Code).Error("provider phase 未通过")
}

func failProviderFrom(result *LanguageResult, index int, status Status, code, message string) LanguageResult {
	cause := Cause{Code: code, Message: message, Source: result.Phases[index].Phase}
	setProviderPhase(result, index, status, cause)
	for current := index + 1; current < len(result.Phases); current++ {
		result.Phases[current].Cause = Cause{Code: "upstream_not_passed", Message: "not run after " + result.Phases[index].Phase, Source: result.Phases[index].Phase}
	}
	if index <= 6 {
		result.RuntimeStatus = status
		result.RuntimeCause = cause
		result.DebugStatus = StatusNotRun
		result.DebugCause = Cause{Code: "runtime_not_passed", Message: "debug not run after runtime failure", Source: result.Phases[index].Phase}
	} else {
		result.RuntimeStatus = StatusPass
		result.DebugStatus = status
		result.DebugCause = cause
	}
	return *result
}

func blockProviderDebug(result *LanguageResult, code, message string) LanguageResult {
	return failProviderFrom(result, 7, StatusBlocked, code, message)
}

func failProviderDebug(result *LanguageResult, index int, code, message string) LanguageResult {
	return failProviderFrom(result, index, StatusFail, code, message)
}

func structuredData(result ToolCallResult) map[string]any {
	structured := RawMessageMap(result.StructuredContent)
	data, _ := structured["data"].(map[string]any)
	return data
}

func integerValue(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return int(result)
	default:
		return 0
	}
}

func numericIdentity(value any) bool {
	switch typed := value.(type) {
	case float64:
		return typed == float64(int(typed))
	case int:
		return true
	case json.Number:
		_, err := typed.Int64()
		return err == nil
	default:
		return false
	}
}

func validateDebugVariables(raw any, expected map[string]any) error {
	items, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("debug variables are not an array")
	}
	observed := map[string]any{}
	for _, item := range items {
		variable, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := variable["name"].(string)
		observed[name] = variable["value"]
	}
	for name, wanted := range expected {
		actual, ok := observed[name]
		if !ok {
			return fmt.Errorf("debug variable %s is missing", name)
		}
		if !debugVariableValueEqual(actual, wanted) {
			return fmt.Errorf("debug variable %s=%v, want %v", name, actual, wanted)
		}
	}
	return nil
}

func debugVariableValueEqual(actual, wanted any) bool {
	wantedString, stringExpected := wanted.(string)
	actualString, stringObserved := actual.(string)
	if !stringExpected || !stringObserved {
		return fmt.Sprint(actual) == fmt.Sprint(wanted)
	}
	if actualString == wantedString {
		return true
	}
	if len(actualString) < 2 {
		return false
	}
	quote := actualString[0]
	if actualString[len(actualString)-1] != quote || (quote != '\'' && quote != '"') {
		return false
	}
	if actualString[1:len(actualString)-1] == wantedString {
		return true
	}
	if quote == '"' {
		unquoted, err := strconv.Unquote(actualString)
		return err == nil && unquoted == wantedString
	}
	return false
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
