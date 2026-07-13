// runner.go 执行固定 Windows campaign 场景并保存逐工具证据。
//
// 职责：
//   - 建立 campaign 变量、按依赖顺序执行固定 MCP steps
//   - 处理有限的状态轮询、变量捕获、严格策略拒绝和精确 cleanup
//   - 生成恰好 75 条 primary 工具结果行

// 边界：
//   - 不接受任意命令、插件、脚本步骤或动态图
//   - 不自动批准安全操作，不复用或持久化 approval token
//   - 不在非 windows/amd64 运行
package windowsvalidation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	verdictPass                = "PASS"
	verdictFail                = "FAIL"
	verdictBlocked             = "BLOCKED"
	fixtureAuthorizationPrefix = "Bearer superdev-validation-"
)

// RuntimeInput 是复制到 Windows 后由测试者填写的机器相关输入。
type RuntimeInput struct {
	SchemaVersion       int    `json:"schema_version"`
	Kind                string `json:"kind"`
	MCPPath             string `json:"mcp_path"`
	InstallerDirectory  string `json:"installer_directory"`
	CampaignRoot        string `json:"campaign_root"`
	ResultsRoot         string `json:"results_root"`
	LinuxHostID         string `json:"linux_host_id"`
	LinuxRoot           string `json:"linux_root"`
	ApprovalWaitSeconds int    `json:"approval_wait_seconds"`
	Lane                string `json:"lane,omitempty"`
	CampaignID          string `json:"campaign_id,omitempty"`
}

// RunOptions 描述一次 Windows campaign 的包和输入位置。
type RunOptions struct {
	PackageRoot  string
	InputPath    string
	MCPPath      string
	AgentURL     string
	ResultsRoot  string
	InstallerDir string
}

// ToolEvidenceRow 是最终 75 工具表中的一行。
type ToolEvidenceRow struct {
	Tool          string `json:"tool"`
	ScenarioID    string `json:"scenario_id"`
	StepID        string `json:"step_id"`
	Verdict       string `json:"verdict"`
	Outcome       string `json:"outcome,omitempty"`
	EvidencePath  string `json:"evidence_path,omitempty"`
	Error         string `json:"error,omitempty"`
	StartedAtUTC  string `json:"started_at_utc,omitempty"`
	FinishedAtUTC string `json:"finished_at_utc,omitempty"`
}

// StepExecution 保存 supporting 与 primary 步骤的统一执行摘要。
type StepExecution struct {
	StepID       string `json:"step_id"`
	Tool         string `json:"tool"`
	Coverage     string `json:"coverage"`
	Verdict      string `json:"verdict"`
	Outcome      string `json:"outcome,omitempty"`
	EvidencePath string `json:"evidence_path,omitempty"`
	Error        string `json:"error,omitempty"`
}

// ScenarioExecution 保存一个固定能力场景的步骤与 cleanup 结果。
type ScenarioExecution struct {
	ID      string          `json:"id"`
	Title   string          `json:"title"`
	Verdict string          `json:"verdict"`
	Steps   []StepExecution `json:"steps"`
	Cleanup []StepExecution `json:"cleanup"`
}

// ProviderExecution 保存七语言各自的运行与调试结论。
type ProviderExecution struct {
	Provider       string `json:"provider"`
	RuntimeVerdict string `json:"runtime_verdict"`
	DebugVerdict   string `json:"debug_verdict"`
	EvidencePath   string `json:"evidence_path,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

// RuntimeAttestation 保存 installed MCP/sidecar 与冻结源面的双向比对。
type RuntimeAttestation struct {
	ServerName      string                `json:"server_name"`
	ServerVersion   string                `json:"server_version"`
	ProtocolVersion string                `json:"protocol_version"`
	ToolNames       []string              `json:"tool_names"`
	ProviderNames   []string              `json:"provider_names"`
	Sidecars        []PackageFileIdentity `json:"sidecars"`
	Verdict         string                `json:"verdict"`
}

// ReportSection 保存最终报告中一个固定验收面的独立状态。
type ReportSection struct {
	Status       string `json:"status"`
	EvidencePath string `json:"evidence_path,omitempty"`
	Reason       string `json:"reason,omitempty"`
}

// ReportSections 分开展示安装器、core、provider、工具、pipeline 与 cleanup。
type ReportSections struct {
	MSIInstaller  ReportSection `json:"msi_installer"`
	NSISInstaller ReportSection `json:"nsis_installer"`
	Core          ReportSection `json:"core"`
	Providers     ReportSection `json:"providers"`
	MCPTools      ReportSection `json:"mcp_tools"`
	Pipeline      ReportSection `json:"pipeline"`
	Cleanup       ReportSection `json:"cleanup"`
}

// CampaignReport 是 Windows 执行后可复查的最终结构化报告。
type CampaignReport struct {
	SchemaVersion      int                   `json:"schema_version"`
	Kind               string                `json:"kind"`
	CampaignID         string                `json:"campaign_id"`
	Status             string                `json:"status"`
	FunctionalStatus   string                `json:"functional_status"`
	FailureStage       string                `json:"failure_stage,omitempty"`
	FailureReason      string                `json:"failure_reason,omitempty"`
	BuildCommit        string                `json:"build_commit"`
	ProductVersion     string                `json:"product_version"`
	Target             string                `json:"target"`
	Lane               string                `json:"lane"`
	RuntimeAttestation RuntimeAttestation    `json:"runtime_attestation"`
	InstallerChecks    []PackageFileIdentity `json:"installer_checks"`
	Scenarios          []ScenarioExecution   `json:"scenarios"`
	Providers          []ProviderExecution   `json:"providers"`
	ToolRows           []ToolEvidenceRow     `json:"tool_rows"`
	Sections           ReportSections        `json:"sections"`
	Cleanup            map[string]any        `json:"cleanup"`
	KnownAnomalies     []map[string]any      `json:"known_anomalies"`
	StartedAtUTC       string                `json:"started_at_utc"`
	FinishedAtUTC      string                `json:"finished_at_utc"`
}

// ScenarioExecutor 执行固定场景并持有 campaign 内存变量。
type ScenarioExecutor struct {
	client     *MCPProcess
	redactor   *Redactor
	resultsDir string
	variables  map[string]any
	passed     map[string]bool
	toolRows   []ToolEvidenceRow
}

// RunCampaign 在 Windows x64 上执行已安装 packaged MCP 的固定验证包。
func RunCampaign(ctx context.Context, options RunOptions) (report CampaignReport, runErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationCampaign")
	stage := "platform_gate"
	campaignID := ""
	defer func() {
		if runErr != nil {
			log.WithErr(runErr).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID}).Error("Windows packaged MCP campaign 失败")
		}
	}()
	if err := ValidateExecutionPlatform(runtime.GOOS, runtime.GOARCH); err != nil {
		log.WithErr(err).Error("拒绝在非目标平台执行 Windows 功能验证")
		return CampaignReport{}, err
	}
	started := time.Now().UTC()
	stage = "load_package_source"
	source, err := LoadPackageSource(options.PackageRoot)
	if err != nil {
		return CampaignReport{}, err
	}
	stage = "verify_package_integrity"
	if err := VerifyPackageIntegrity(options.PackageRoot); err != nil {
		return CampaignReport{}, err
	}
	stage = "load_runtime_input"
	input, err := loadRuntimeInput(options.InputPath)
	if err != nil {
		return CampaignReport{}, err
	}
	applyRunOptionOverrides(&input, options)
	stage = "validate_runtime_input"
	if err := validateRuntimeInput(input); err != nil {
		return CampaignReport{}, err
	}
	stage = "verify_lane_installer"
	installerChecks, err := VerifyInstallerForLane(input.InstallerDirectory, laneOrDefault(input.Lane), source.Frozen.Installers)
	if err != nil {
		return CampaignReport{}, err
	}
	stage = "create_campaign_identity"
	if strings.TrimSpace(input.CampaignID) != "" {
		campaignID = input.CampaignID
	} else {
		campaignID, err = newCampaignID(source.Frozen.Build.GitCommit, started)
		if err != nil {
			return CampaignReport{}, err
		}
	}
	workspaceRoot := filepath.Join(input.CampaignRoot, campaignID, "workspace")
	resultsDir := filepath.Join(input.ResultsRoot, campaignID)
	stage = "prepare_campaign_workspace"
	if err := prepareCampaignWorkspace(options.PackageRoot, workspaceRoot, resultsDir); err != nil {
		return CampaignReport{}, err
	}
	redactor := NewRedactor()
	stage = "create_fixture_authorization"
	authValue := fixtureAuthorization(campaignID)
	redactor.RegisterSecret("AUTHORIZATION", authValue)
	variables := buildCampaignVariables(source, input, campaignID, workspaceRoot, options.PackageRoot, authValue)

	agentURL := strings.TrimSpace(options.AgentURL)
	if agentURL == "" {
		agentURL = "http://127.0.0.1:57017"
	}
	log.WithFields(map[string]any{"campaign_id": campaignID, "package_root": options.PackageRoot, "results_dir": resultsDir}).Info("开始 Windows packaged MCP campaign")
	stage = "start_packaged_mcp"
	client, err := StartMCPProcess(ctx, input.MCPPath, agentURL)
	if err != nil {
		persistGateFailure(resultsDir, redactor, source, input, campaignID, started, installerChecks, stage, err)
		return CampaignReport{}, err
	}
	defer client.Close()
	stage = "runtime_attestation"
	attestation, err := attestRuntime(ctx, client, source, input.MCPPath, resultsDir, redactor)
	if err != nil {
		log.WithErr(err).Error("运行时身份门禁失败")
		persistGateFailure(resultsDir, redactor, source, input, campaignID, started, installerChecks, stage, err)
		return CampaignReport{}, err
	}
	if laneOrDefault(input.Lane) == "msi_smoke" {
		report := CampaignReport{
			SchemaVersion: 1, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
			Status: pendingCampaignStatus(verdictPass), FunctionalStatus: verdictPass,
			BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
			Target: "Windows 10 x64", Lane: "msi_smoke", RuntimeAttestation: attestation,
			InstallerChecks: installerChecks,
			ToolRows:        ensureAllToolRows(source.Coverage, nil),
			Providers:       blockedProviderMatrix(source.Fixtures, "not executed in the independent MSI smoke lane"),
			Scenarios:       blockedScenarioMatrix(source.Scenarios, "not executed in the independent MSI smoke lane"),
			Cleanup: map[string]any{
				"status": "PENDING", "reason": "run Cleanup-Validation.ps1 to compare and restore the prepared baseline",
				"campaign_workspace": workspaceRoot,
			},
			KnownAnomalies: source.Frozen.KnownBaselineExceptions, StartedAtUTC: started.Format(time.RFC3339Nano),
			FinishedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		}
		report.Sections = buildReportSections(report)
		stage = "write_msi_report"
		if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
			return CampaignReport{}, err
		}
		if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
			return CampaignReport{}, err
		}
		log.WithFields(map[string]any{"campaign_id": campaignID, "lane": "msi_smoke", "status": report.Status}).Info("Windows MSI packaged sidecar smoke 完成")
		return report, nil
	}
	executor := &ScenarioExecutor{
		client: client, redactor: redactor, resultsDir: resultsDir,
		variables: variables, passed: map[string]bool{},
	}
	scenarios := orderedScenarios(source.Scenarios)
	executions := make([]ScenarioExecution, 0, len(scenarios))
	configReady := false
	for _, scenario := range scenarios {
		// provider 矩阵必须在远端 pipeline 之前完成；remote 场景在下方单独执行。
		if scenario.ID == "remote-pipeline" {
			continue
		}
		if !configReady && scenario.ID != "identity-observation" && scenario.ID != "config-security-lifecycle" {
			execution := executor.blockScenario(scenario, "campaign configuration gate did not pass")
			executions = append(executions, execution)
			continue
		}
		if scenario.ID == "browser-debug" || scenario.ID == "code-debug" {
			if err := executor.ensureGoRunning(ctx); err != nil {
				execution := executor.blockScenario(scenario, "Go fixture precondition failed: "+err.Error())
				executions = append(executions, execution)
				continue
			}
		}
		execution := executor.ExecuteScenario(ctx, scenario)
		executions = append(executions, execution)
		if scenario.ID == "config-security-lifecycle" {
			// 冻结产品的 get_debug_credentials 只能读取持久明文，安全验证会诚实 FAIL；
			// 该产品缺口不能抹掉已经成功建立的 campaign 隔离配置，也不能阻断其余工具取证。
			configReady = executor.configurationReady()
		}
	}
	providers := blockedProviderMatrix(source.Fixtures, "campaign configuration gate did not establish a project and Go deployment")
	if configReady {
		if err := executor.ensureGoStopped(ctx); err != nil {
			providers = blockedProviderMatrix(source.Fixtures, "primary Go fixture cleanup gate failed: "+err.Error())
			for index := range providers {
				if providers[index].Provider == "go" {
					providers[index].RuntimeVerdict = verdictFail
				}
			}
		} else {
			providers = executor.ExecuteProviderMatrix(ctx, source.Fixtures)
		}
	}
	if remote, found := scenarioByID(scenarios, "remote-pipeline"); found {
		if !configReady {
			executions = append(executions, executor.blockScenario(remote, "campaign configuration gate did not pass"))
		} else {
			available, preflightErr := executor.preflightRemoteHost(ctx, input.LinuxHostID)
			switch {
			case preflightErr != nil:
				executions = append(executions, executor.failScenario(remote, "remote Host ID preflight failed: "+preflightErr.Error()))
			case !available:
				executions = append(executions, executor.blockScenario(remote, "configured dedicated Linux Host ID is not currently available as a non-self target"))
			default:
				executions = append(executions, executor.ExecuteScenario(ctx, remote))
			}
		}
	}
	toolRows := ensureAllToolRows(source.Coverage, executor.toolRows)
	functionalStatus := aggregateCampaignStatus(toolRows, providers, executions)
	report = CampaignReport{
		SchemaVersion:      1,
		Kind:               "superdev.windows-validation.campaign-report",
		CampaignID:         campaignID,
		Status:             pendingCampaignStatus(functionalStatus),
		FunctionalStatus:   functionalStatus,
		BuildCommit:        source.Frozen.Build.GitCommit,
		ProductVersion:     source.Frozen.Build.ProductVersion,
		Target:             "Windows 10 x64",
		Lane:               laneOrDefault(input.Lane),
		RuntimeAttestation: attestation,
		InstallerChecks:    installerChecks,
		Scenarios:          executions,
		Providers:          providers,
		ToolRows:           toolRows,
		Cleanup: map[string]any{
			"status": "PENDING", "reason": "run Cleanup-Validation.ps1 to compare and restore the prepared baseline",
			"campaign_workspace": workspaceRoot,
		},
		KnownAnomalies: source.Frozen.KnownBaselineExceptions,
		StartedAtUTC:   started.Format(time.RFC3339Nano),
		FinishedAtUTC:  time.Now().UTC().Format(time.RFC3339Nano),
	}
	report.Sections = buildReportSections(report)
	stage = "write_nsis_report"
	if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "complete"
	log.WithFields(map[string]any{"campaign_id": campaignID, "status": report.Status, "functional_status": functionalStatus, "tool_rows": len(toolRows), "provider_rows": len(providers)}).Info("Windows packaged MCP campaign 执行完成；等待最终 cleanup")
	return report, nil
}

func writeCampaignReports(resultsDir string, redactor *Redactor, report CampaignReport) error {
	redacted := redactor.Redact(RawMessageMap(report))
	if err := writeJSON(filepath.Join(resultsDir, "campaign-report.json"), redacted); err != nil {
		return err
	}
	raw, err := json.Marshal(redacted)
	if err != nil {
		return fmt.Errorf("encode redacted campaign report: %w", err)
	}
	var safeReport CampaignReport
	if err := json.Unmarshal(raw, &safeReport); err != nil {
		return fmt.Errorf("decode redacted campaign report: %w", err)
	}
	return writeMarkdownReport(filepath.Join(resultsDir, "campaign-report.md"), safeReport)
}

func persistGateFailure(resultsDir string, redactor *Redactor, source PackageSource, input RuntimeInput, campaignID string, started time.Time, installerChecks []PackageFileIdentity, stage string, cause error) {
	reason := stage + ": " + cause.Error()
	report := CampaignReport{
		SchemaVersion: 1, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Status: verdictFail, FunctionalStatus: verdictFail, FailureStage: stage, FailureReason: cause.Error(),
		BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
		Target: "Windows 10 x64", Lane: laneOrDefault(input.Lane),
		RuntimeAttestation: RuntimeAttestation{Verdict: verdictFail}, InstallerChecks: installerChecks,
		Scenarios:      blockedScenarioMatrix(source.Scenarios, reason),
		Providers:      blockedProviderMatrix(source.Fixtures, reason),
		ToolRows:       ensureAllToolRows(source.Coverage, nil),
		Cleanup:        map[string]any{"status": "PENDING", "reason": "run Cleanup-Validation.ps1 even after a gate failure"},
		KnownAnomalies: source.Frozen.KnownBaselineExceptions,
		StartedAtUTC:   started.Format(time.RFC3339Nano), FinishedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
	}
	report.Sections = buildReportSections(report)
	log := logger.GetLogger().WithEntryName("WindowsValidationReport")
	if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
		log.WithErr(err).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID}).Error("身份门禁失败报告写入失败")
		return
	}
	if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
		log.WithErr(err).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID}).Error("身份门禁失败聚合摘要写入失败")
	}
}

// ExecuteScenario 执行一个固定场景；失败后仍执行受 guard 保护的 cleanup。
func (e *ScenarioExecutor) ExecuteScenario(ctx context.Context, scenario Scenario) (execution ScenarioExecution) {
	log := logger.GetLogger().WithEntryName("WindowsValidationScenario")
	log.WithFields(map[string]any{"scenario": scenario.ID, "step_count": len(scenario.Steps)}).Info("开始执行固定验证场景")
	defer func() {
		fields := map[string]any{"scenario": scenario.ID, "verdict": execution.Verdict}
		if execution.Verdict == verdictFail {
			log.WithFields(fields).Error("固定验证场景执行失败")
		} else {
			log.WithFields(fields).Info("固定验证场景执行完成")
		}
	}()
	execution = ScenarioExecution{ID: scenario.ID, Title: scenario.Title, Verdict: verdictPass}
	if err := e.mergeScenarioVariables(scenario); err != nil {
		execution = e.blockScenario(scenario, err.Error())
		return execution
	}
	failed := false
	for _, step := range scenario.Steps {
		if failed {
			blocked := StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictBlocked, Error: "blocked by an earlier step in the same scenario"}
			execution.Steps = append(execution.Steps, blocked)
			e.appendToolRow(scenario.ID, step, blocked)
			continue
		}
		result := e.executeStep(ctx, scenario.ID, step)
		execution.Steps = append(execution.Steps, result)
		e.passed[step.ID] = result.Verdict == verdictPass
		e.appendToolRow(scenario.ID, step, result)
		if result.Verdict != verdictPass {
			failed = true
			execution.Verdict = result.Verdict
		}
	}
	for _, step := range scenario.Cleanup {
		if !ShouldRunCleanup(step.RunIf, e.variables, e.passed) {
			continue
		}
		result := e.executeStep(ctx, scenario.ID+"-cleanup", step)
		execution.Cleanup = append(execution.Cleanup, result)
		if result.Verdict != verdictPass {
			execution.Verdict = verdictFail
		}
	}
	return execution
}

// EvaluateStepResult 验证工具结果；策略拒绝只接受逐工具白名单。
func EvaluateStepResult(step ScenarioStep, result ToolCallResult, variables map[string]any) (string, error) {
	root := RawMessageMap(result)
	if !result.IsError {
		if err := EvaluateAssertions(root, step.Expect.Assertions, variables); err != nil {
			return "", err
		}
		return "success", nil
	}
	if step.Expect.Outcome != "success_or_policy_denied" {
		return "", fmt.Errorf("tool returned product error: %s", toolErrorCode(result))
	}
	code := toolErrorCode(result)
	allowed := map[string]map[string]bool{
		"browser_evaluate": {"browser_evaluate_disabled": true, "operation_denied": true},
		"debug_evaluate":   {"operation_denied": true, "approval_rejected": true},
	}
	if !allowed[step.Tool][code] {
		return "", fmt.Errorf("tool error %s is not an allowed policy denial for %s", code, step.Tool)
	}
	return "expected_policy_denial", nil
}

// ShouldRunCleanup 判断资源身份和主步骤状态 guard；条件只支持固定的 AND 原语。
func ShouldRunCleanup(condition string, variables map[string]any, passed map[string]bool) bool {
	condition = strings.TrimSpace(condition)
	if condition == "" {
		return true
	}
	for _, clause := range strings.Split(condition, "&&") {
		clause = strings.TrimSpace(clause)
		switch {
		case strings.HasPrefix(clause, "variable_set:"):
			name := strings.TrimPrefix(clause, "variable_set:")
			value, ok := variables[name]
			if !ok || isEmpty(value) {
				return false
			}
		case strings.HasPrefix(clause, "variable_unset:"):
			name := strings.TrimPrefix(clause, "variable_unset:")
			value, ok := variables[name]
			if ok && !isEmpty(value) {
				return false
			}
		case strings.HasPrefix(clause, "primary_step_not_passed:"):
			name := strings.TrimPrefix(clause, "primary_step_not_passed:")
			if passed[name] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (e *ScenarioExecutor) executeStep(ctx context.Context, scenarioID string, step ScenarioStep) StepExecution {
	started := time.Now().UTC()
	log := logger.GetLogger().WithEntryName("WindowsValidationStep")
	log.WithFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool}).Info("开始执行 MCP 验证步骤")
	rendered, err := RenderValue(step.Arguments, e.variables)
	if err != nil {
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictFail, Error: err.Error()}
	}
	arguments, ok := rendered.(map[string]any)
	if !ok {
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictFail, Error: "rendered arguments are not an object"}
	}
	deadline := time.Now()
	if step.Poll != nil {
		deadline = deadline.Add(time.Duration(step.Poll.TimeoutSeconds) * time.Second)
	}
	var result ToolCallResult
	var outcome string
	for {
		result, err = e.client.CallTool(ctx, step.Tool, arguments)
		if err == nil {
			outcome, err = EvaluateStepResult(step, result, e.variables)
		}
		if err == nil || step.Poll == nil || result.IsError || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(time.Duration(step.Poll.IntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
		if err == ctx.Err() && err != nil {
			break
		}
	}
	if step.SettleMS > 0 && err == nil {
		timer := time.NewTimer(time.Duration(step.SettleMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
	}
	root := RawMessageMap(result)
	evidencePath, evidenceErr := e.recordStepEvidence(scenarioID, step, arguments, root, err)
	if err == nil && evidenceErr != nil {
		err = evidenceErr
	}
	if err == nil {
		for name, path := range step.Capture {
			value, found := LookupPath(root, path)
			if !found {
				err = fmt.Errorf("capture %s path %s was not found", name, path)
				break
			}
			e.variables[name] = value
		}
		// pipeline 配置依赖 import 返回的真实 digest；捕获后才做完整渲染，避免预先猜测身份。
		if _, captured := step.Capture["imported_template_digest"]; captured {
			renderedConfig, renderErr := RenderValue(e.variables["remote_pipeline_config"], e.variables)
			if renderErr != nil {
				err = fmt.Errorf("render imported remote pipeline config: %w", renderErr)
			} else {
				e.variables["remote_pipeline_config"] = renderedConfig
			}
		}
	}
	finished := time.Now().UTC()
	if err != nil {
		log.WithErr(err).WithFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool}).Error("MCP 验证步骤失败")
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictFail, EvidencePath: evidencePath, Error: err.Error()}
	}
	log.WithFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool, "outcome": outcome, "duration_ms": finished.Sub(started).Milliseconds()}).Info("MCP 验证步骤完成")
	return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictPass, Outcome: outcome, EvidencePath: evidencePath}
}

func (e *ScenarioExecutor) recordStepEvidence(scenarioID string, step ScenarioStep, arguments map[string]any, response map[string]any, stepErr error) (string, error) {
	root := filepath.Join(e.resultsDir, "evidence", scenarioID, step.ID)
	requestValue := map[string]any{"tool": step.Tool, "arguments": arguments}
	requestCopy := cloneJSONMap(requestValue)
	responseCopy := cloneJSONMap(response)
	applyEvidenceRedactions(requestCopy, responseCopy, step.Evidence.Redact, e.redactor)
	redactedRequest := e.redactor.Redact(requestCopy)
	redactedResponse := e.redactor.Redact(responseCopy)
	if e.redactor.containsKnownSecret(redactedRequest) || e.redactor.containsKnownSecret(redactedResponse) {
		return "", fmt.Errorf("redaction invariant failed before writing step evidence")
	}
	if err := writeJSON(filepath.Join(root, "request.json"), redactedRequest); err != nil {
		return "", err
	}
	if err := writeJSON(filepath.Join(root, "response.json"), redactedResponse); err != nil {
		return "", err
	}
	selected := map[string]any{}
	for _, record := range step.Evidence.Record {
		if strings.HasPrefix(record, "sha256:") {
			path := strings.TrimPrefix(record, "sha256:")
			value, found := LookupPath(response, path)
			if !found {
				return filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID)), fmt.Errorf("evidence hash path %s missing", path)
			}
			digest, size, err := digestEvidenceValue(value)
			if err != nil {
				return filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID)), err
			}
			selected[record] = map[string]any{"sha256": digest, "size_bytes": size}
			continue
		}
		value, found := LookupPath(responseCopy, record)
		if found {
			selected[record] = e.redactor.Redact(value)
		}
	}
	selected["assertion_error"] = ""
	if stepErr != nil {
		selected["assertion_error"] = stepErr.Error()
	}
	if e.redactor.containsKnownSecret(selected) {
		return filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID)), fmt.Errorf("selected evidence still contains a registered secret")
	}
	encoded := strings.ToLower(CanonicalJSON(selected))
	for _, forbidden := range step.Evidence.Forbid {
		if forbidden != "" && strings.Contains(encoded, strings.ToLower(forbidden)) {
			return filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID)), fmt.Errorf("selected evidence contains forbidden marker %q", forbidden)
		}
	}
	if err := writeJSON(filepath.Join(root, "evidence.json"), selected); err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID)), nil
}

func (e *ScenarioExecutor) mergeScenarioVariables(scenario Scenario) error {
	for name, raw := range scenario.Variables {
		metadata, isMetadata := raw.(map[string]any)
		if !isMetadata {
			if _, exists := e.variables[name]; !exists {
				e.variables[name] = raw
			}
			continue
		}
		if _, exists := e.variables[name]; !exists {
			if value, hasDefault := metadata["default"]; hasDefault {
				e.variables[name] = value
			}
		}
		required, _ := metadata["required"].(bool)
		if required {
			value, exists := e.variables[name]
			if !exists || isEmpty(value) {
				return fmt.Errorf("scenario %s requires runtime variable %s", scenario.ID, name)
			}
		}
	}
	return nil
}

func (e *ScenarioExecutor) appendToolRow(scenarioID string, step ScenarioStep, execution StepExecution) {
	if step.Coverage != CoveragePrimary {
		return
	}
	e.toolRows = append(e.toolRows, ToolEvidenceRow{
		Tool: step.Tool, ScenarioID: scenarioID, StepID: step.ID, Verdict: execution.Verdict,
		Outcome: execution.Outcome, EvidencePath: execution.EvidencePath, Error: execution.Error,
	})
}

func (e *ScenarioExecutor) blockScenario(scenario Scenario, reason string) ScenarioExecution {
	logger.GetLogger().WithEntryName("WindowsValidationScenario").WithFields(map[string]any{"scenario": scenario.ID, "reason": reason}).Info("固定验证场景受前置条件阻断")
	execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title, Verdict: verdictBlocked}
	for _, step := range scenario.Steps {
		blocked := StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictBlocked, Error: reason}
		execution.Steps = append(execution.Steps, blocked)
		e.appendToolRow(scenario.ID, step, blocked)
	}
	return execution
}

func blockedScenarioMatrix(scenarios []Scenario, reason string) []ScenarioExecution {
	results := make([]ScenarioExecution, 0, len(scenarios))
	for _, scenario := range orderedScenarios(scenarios) {
		execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title, Verdict: verdictBlocked}
		for _, step := range scenario.Steps {
			execution.Steps = append(execution.Steps, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictBlocked, Error: reason,
			})
		}
		results = append(results, execution)
	}
	return results
}

func (e *ScenarioExecutor) configurationReady() bool {
	projectID, _ := e.variables["project_id"].(string)
	return strings.TrimSpace(projectID) != "" && e.passed["upsert-go-service"] && e.passed["upsert-bootstrap-pipeline"]
}

func (e *ScenarioExecutor) failScenario(scenario Scenario, reason string) ScenarioExecution {
	logger.GetLogger().WithEntryName("WindowsValidationScenario").WithFields(map[string]any{"scenario": scenario.ID, "reason": reason}).Error("固定验证场景预检失败")
	execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title, Verdict: verdictFail}
	for _, step := range scenario.Steps {
		failed := StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Verdict: verdictFail, Error: reason}
		execution.Steps = append(execution.Steps, failed)
		e.appendToolRow(scenario.ID, step, failed)
	}
	return execution
}

func (e *ScenarioExecutor) preflightRemoteHost(ctx context.Context, hostID string) (bool, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationRemotePreflight")
	log.WithField("host_id", hostID).Info("开始核对专用 Linux Host ID")
	result, err := e.client.CallTool(ctx, "list_hosts", map[string]any{})
	if err != nil {
		log.WithErr(err).WithField("host_id", hostID).Error("专用 Linux Host ID 预检调用失败")
		return false, err
	}
	if result.IsError {
		err := fmt.Errorf("list_hosts returned %s", toolErrorCode(result))
		log.WithErr(err).WithField("host_id", hostID).Error("专用 Linux Host ID 预检返回产品错误")
		return false, err
	}
	root := RawMessageMap(result)
	if err := writeJSON(filepath.Join(e.resultsDir, "evidence", "remote-host-preflight.json"), e.redactor.Redact(root)); err != nil {
		return false, err
	}
	available := remoteHostPresent(root, hostID)
	log.WithFields(map[string]any{"host_id": hostID, "available": available}).Info("专用 Linux Host ID 预检完成")
	return available, nil
}

func remoteHostPresent(value any, hostID string) bool {
	hosts, found := LookupPath(value, "structuredContent.data.remote_hosts")
	if !found {
		return false
	}
	items, ok := hosts.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		host, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if fmt.Sprint(host["id"]) == hostID && host["is_self"] == false {
			return true
		}
	}
	return false
}

func (e *ScenarioExecutor) ensureGoRunning(ctx context.Context) error {
	projectID, _ := e.variables["project_id"].(string)
	deploymentID, _ := e.variables["go_deployment_id"].(string)
	if projectID == "" || deploymentID == "" {
		return fmt.Errorf("Go project/deployment identity is missing")
	}
	result, err := e.client.CallTool(ctx, "start_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300})
	if err != nil {
		return err
	}
	if result.IsError {
		code := toolErrorCode(result)
		if code != "deployment_already_running" && code != "already_running" {
			return fmt.Errorf("start Go fixture: %s", code)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, callErr := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "running" {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("Go fixture did not reach running")
}

func (e *ScenarioExecutor) ensureGoStopped(ctx context.Context) error {
	projectID, _ := e.variables["project_id"].(string)
	deploymentID, _ := e.variables["go_deployment_id"].(string)
	if projectID == "" || deploymentID == "" {
		return fmt.Errorf("Go project/deployment identity is missing")
	}
	state, stateErr := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
	if stateErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
		return nil
	}
	result, err := e.client.CallTool(ctx, "stop_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300})
	if err != nil {
		return err
	}
	if result.IsError {
		code := toolErrorCode(result)
		if code != "deployment_already_stopped" && code != "already_stopped" {
			return fmt.Errorf("stop Go fixture: %s", code)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		state, callErr := e.client.CallTool(ctx, "list_services", map[string]any{"project_id": projectID})
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
			return nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("Go fixture did not reach stopped")
}

func blockedProviderMatrix(fixtures []FixtureManifest, reason string) []ProviderExecution {
	results := make([]ProviderExecution, 0, len(fixtures))
	for _, fixture := range fixtures {
		results = append(results, ProviderExecution{Provider: fixture.Provider, RuntimeVerdict: verdictBlocked, DebugVerdict: verdictBlocked, Reason: reason})
	}
	return results
}

func toolErrorCode(result ToolCallResult) string {
	root := RawMessageMap(result)
	value, _ := LookupPath(root, "structuredContent.code")
	return fmt.Sprint(value)
}

func orderedScenarios(scenarios []Scenario) []Scenario {
	out := append([]Scenario{}, scenarios...)
	rank := map[string]int{
		"identity-observation":      10,
		"config-security-lifecycle": 20,
		"logs-diagnostics":          30,
		"browser-debug":             40,
		"code-debug":                50,
		"remote-pipeline":           60,
	}
	sort.Slice(out, func(i, j int) bool {
		if rank[out[i].ID] == rank[out[j].ID] {
			return out[i].ID < out[j].ID
		}
		return rank[out[i].ID] < rank[out[j].ID]
	})
	return out
}

func scenarioByID(scenarios []Scenario, id string) (Scenario, bool) {
	for _, scenario := range scenarios {
		if scenario.ID == id {
			return scenario, true
		}
	}
	return Scenario{}, false
}

func buildCampaignVariables(source PackageSource, input RuntimeInput, campaignID, workspaceRoot, packageRoot, authValue string) map[string]any {
	goFixture := fixtureByProvider(source.Fixtures, "go")
	linuxRoot := strings.ReplaceAll(input.LinuxRoot, "{{run_id}}", campaignID)
	return map[string]any{
		"run_id": campaignID, "campaign_id": campaignID,
		"package_root": packageRoot, "project_root": workspaceRoot,
		"project_name": "superdev-" + campaignID, "go_project_name": "superdev-" + campaignID,
		"go_service_id": "go-validation", "go_service_name": "go-validation", "go_deployment_id": "go-validation-dev",
		"go_readiness_url": goFixture.Readiness.URL, "browser_target_url": "http://127.0.0.1:18190",
		"go_source_path": goFixture.Debug.Source, "go_breakpoint_line": goFixture.Debug.Line,
		"fixture_authorization": authValue,
		"linux_host_id":         input.LinuxHostID, "linux_root": linuxRoot,
		"pipeline_id": "remote-" + campaignID, "env_name": "validation",
		"artifact_version_a": "A", "artifact_version_b": "B",
		"approval_wait_seconds":     boundedApprovalWaitSeconds(input.ApprovalWaitSeconds),
		"bootstrap_pipeline_config": bootstrapPipelineConfig(campaignID),
		// imported_template_digest 尚未产生，因此保留固定模板，导入成功后再完整渲染。
		"remote_pipeline_config": source.RemotePipelineConfig,
	}
}

func bootstrapPipelineConfig(campaignID string) map[string]any {
	return map[string]any{
		"id": "bootstrap-validation", "name": "Bootstrap validation " + campaignID,
		"artifact_kind": "file",
		"variables":     map[string]any{"campaign_id": campaignID},
		"environments":  map[string]any{"validation": map[string]any{"variables": map[string]any{}}},
		"pipeline": map[string]any{
			"build": []any{map[string]any{"name": "Validate configuration seam", "type": "local_command", "with": map[string]any{"cmd": "echo superdev-validation-config"}}},
		},
	}
}

func aggregateCampaignStatus(rows []ToolEvidenceRow, providers []ProviderExecution, scenarios []ScenarioExecution) string {
	status := verdictPass
	for _, row := range rows {
		if row.Verdict == verdictFail {
			return verdictFail
		}
		if row.Verdict == verdictBlocked {
			status = verdictBlocked
		}
	}
	for _, provider := range providers {
		if provider.RuntimeVerdict == verdictFail || provider.DebugVerdict == verdictFail {
			return verdictFail
		}
		if provider.RuntimeVerdict == verdictBlocked || provider.DebugVerdict == verdictBlocked {
			status = verdictBlocked
		}
	}
	for _, scenario := range scenarios {
		if scenario.Verdict == verdictFail {
			return verdictFail
		}
	}
	return status
}

func pendingCampaignStatus(functionalStatus string) string {
	if functionalStatus == verdictFail {
		return verdictFail
	}
	return verdictBlocked
}

func ensureAllToolRows(assignments []CoverageAssignment, rows []ToolEvidenceRow) []ToolEvidenceRow {
	byTool := map[string]ToolEvidenceRow{}
	for _, row := range rows {
		byTool[row.Tool] = row
	}
	out := make([]ToolEvidenceRow, 0, len(assignments))
	for _, assignment := range assignments {
		row, ok := byTool[assignment.Tool]
		if !ok {
			row = ToolEvidenceRow{Tool: assignment.Tool, ScenarioID: assignment.ScenarioID, StepID: assignment.StepID, Verdict: verdictBlocked, Error: "primary step was not reached"}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

func applyEvidenceRedactions(request, response map[string]any, paths []string, redactor *Redactor) {
	for _, path := range paths {
		target := response
		trimmed := path
		if strings.HasPrefix(path, "request.") {
			target = request
			trimmed = strings.TrimPrefix(path, "request.")
		}
		redactAtPath(target, strings.Split(trimmed, "."), redactor)
	}
}

func redactAtPath(current any, parts []string, redactor *Redactor) {
	if len(parts) == 0 {
		return
	}
	switch typed := current.(type) {
	case map[string]any:
		part := parts[0]
		if part == "*" {
			for key := range typed {
				redactAtPath(typed[key], parts[1:], redactor)
			}
			return
		}
		value, ok := typed[part]
		if !ok {
			return
		}
		if len(parts) == 1 {
			typed[part] = redactor.RegisterSecret("EVIDENCE", fmt.Sprint(value))
			return
		}
		redactAtPath(value, parts[1:], redactor)
	case []any:
		if parts[0] == "*" {
			for _, item := range typed {
				redactAtPath(item, parts[1:], redactor)
			}
		}
	}
}

func digestEvidenceValue(value any) (string, int, error) {
	text, ok := value.(string)
	if !ok {
		text = CanonicalJSON(value)
	}
	data, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		data = []byte(text)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), len(data), nil
}

func cloneJSONMap(value map[string]any) map[string]any {
	raw, _ := json.Marshal(value)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func deploymentStatus(result ToolCallResult, deploymentID string) string {
	root := RawMessageMap(result)
	servicesValue, ok := LookupPath(root, "structuredContent.data.services")
	if !ok {
		return ""
	}
	services, _ := servicesValue.([]any)
	for _, serviceRaw := range services {
		service, _ := serviceRaw.(map[string]any)
		deployments, _ := service["deployments"].([]any)
		for _, deploymentRaw := range deployments {
			deployment, _ := deploymentRaw.(map[string]any)
			if deployment["id"] == deploymentID {
				return fmt.Sprint(deployment["status"])
			}
		}
	}
	return ""
}

func laneOrDefault(lane string) string {
	if strings.TrimSpace(lane) == "" {
		return "nsis_core"
	}
	return lane
}

func boundedApprovalWaitSeconds(value int) int {
	if value <= 0 {
		return 300
	}
	if value > 300 {
		return 300
	}
	return value
}

func fixtureByProvider(fixtures []FixtureManifest, provider string) FixtureManifest {
	for _, fixture := range fixtures {
		if fixture.Provider == provider {
			return fixture
		}
	}
	return FixtureManifest{}
}

func newCampaignID(commit string, now time.Time) (string, error) {
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("w10x64-%s-%s-%s", shortCommit(commit), now.Format("20060102T150405Z"), hex.EncodeToString(random)), nil
}

func fixtureAuthorization(campaignID string) string {
	// campaign ID 是公开资源身份，不是凭据；Header 只用于证明七个夹具确实执行了鉴权分支，
	// 且完整 Authorization 仍只在驱动器内存中构造并按敏感值脱敏。
	return fixtureAuthorizationPrefix + campaignID
}
