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
	Tool           string           `json:"tool"`
	ScenarioID     string           `json:"scenario_id"`
	StepID         string           `json:"step_id"`
	Result         ValidationResult `json:"result"`
	Outcome        string           `json:"outcome,omitempty"`
	InlineEvidence map[string]any   `json:"inline_evidence,omitempty"`
}

// StepExecution 保存 supporting 与 primary 步骤的统一执行摘要。
type StepExecution struct {
	StepID         string           `json:"step_id"`
	Tool           string           `json:"tool"`
	Coverage       string           `json:"coverage"`
	Result         ValidationResult `json:"result"`
	Outcome        string           `json:"outcome,omitempty"`
	Prerequisites  []StepExecution  `json:"prerequisites,omitempty"`
	InlineEvidence map[string]any   `json:"inline_evidence,omitempty"`
}

// ScenarioExecution 保存一个固定能力场景的步骤与 cleanup 结果。
type ScenarioExecution struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Result        ValidationResult `json:"result"`
	Prerequisites []StepExecution  `json:"prerequisites"`
	Steps         []StepExecution  `json:"steps"`
	Cleanup       []StepExecution  `json:"cleanup"`
}

// StepCatalogEntry 保存冻结场景中一个步骤不可删减的身份合同。
type StepCatalogEntry struct {
	StepID   string `json:"step_id"`
	Tool     string `json:"tool"`
	Coverage string `json:"coverage"`
}

// ScenarioCatalogEntry 保存冻结场景及其 target/cleanup 步骤目录。
type ScenarioCatalogEntry struct {
	ID      string             `json:"id"`
	Title   string             `json:"title"`
	Steps   []StepCatalogEntry `json:"steps"`
	Cleanup []StepCatalogEntry `json:"cleanup"`
}

// ValidationCatalog 把本次报告绑定到冻结 scenario、step 与 75 工具归属。
type ValidationCatalog struct {
	Scenarios []ScenarioCatalogEntry `json:"scenarios"`
	Coverage  []CoverageAssignment   `json:"coverage"`
}

// ProviderExecution 保存七语言各自的运行与调试结论。
type ProviderExecution struct {
	Provider       string           `json:"provider"`
	Result         ValidationResult `json:"result"`
	Runtime        ValidationResult `json:"runtime"`
	Debug          ValidationResult `json:"debug"`
	Prerequisites  []StepExecution  `json:"prerequisites"`
	EvidencePath   string           `json:"evidence_path,omitempty"`
	InlineEvidence map[string]any   `json:"inline_evidence,omitempty"`
	Reason         string           `json:"reason,omitempty"`
}

// RuntimeAttestation 保存 installed MCP/sidecar 与冻结源面的双向比对。
type RuntimeAttestation struct {
	ServerName      string                `json:"server_name"`
	ServerVersion   string                `json:"server_version"`
	ProtocolVersion string                `json:"protocol_version"`
	ToolNames       []string              `json:"tool_names"`
	ProviderNames   []string              `json:"provider_names"`
	Sidecars        []PackageFileIdentity `json:"sidecars"`
	Result          ValidationResult      `json:"result"`
	InlineEvidence  map[string]any        `json:"inline_evidence,omitempty"`
}

// ReportSection 保存最终报告中一个固定验收面的独立状态。
type ReportSection struct {
	Result       ValidationResult `json:"result"`
	EvidencePath string           `json:"evidence_path,omitempty"`
	Reason       string           `json:"reason,omitempty"`
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
	Result             ValidationResult      `json:"result"`
	Functional         ValidationResult      `json:"functional_result"`
	FailureStage       string                `json:"failure_stage,omitempty"`
	FailureReason      string                `json:"failure_reason,omitempty"`
	BuildCommit        string                `json:"build_commit"`
	ProductVersion     string                `json:"product_version"`
	Target             string                `json:"target"`
	Lane               string                `json:"lane"`
	Installer          InstallerExecution    `json:"installer"`
	RuntimeAttestation RuntimeAttestation    `json:"runtime_attestation"`
	InstallerChecks    []PackageFileIdentity `json:"installer_checks"`
	Prerequisites      []StepExecution       `json:"prerequisites"`
	Operations         []StepExecution       `json:"operations"`
	ValidationCatalog  ValidationCatalog     `json:"validation_catalog"`
	Scenarios          []ScenarioExecution   `json:"scenarios"`
	Providers          []ProviderExecution   `json:"providers"`
	ToolRows           []ToolEvidenceRow     `json:"tool_rows"`
	Sections           ReportSections        `json:"sections"`
	Cleanup            CleanupRecord         `json:"cleanup"`
	KnownAnomalies     []map[string]any      `json:"known_anomalies"`
	StartedAtUTC       string                `json:"started_at_utc"`
	FinishedAtUTC      string                `json:"finished_at_utc"`
}

// ScenarioExecutor 执行固定场景并持有 campaign 内存变量。
type ScenarioExecutor struct {
	client     mcpToolCaller
	redactor   *Redactor
	resultsDir string
	campaignID string
	lane       string
	variables  map[string]any
	passed     map[string]bool
	toolRows   []ToolEvidenceRow
}

func (e *ScenarioExecutor) logFields(fields map[string]any) map[string]any {
	contextual := map[string]any{"campaign_id": e.campaignID, "lane": e.lane}
	for key, value := range fields {
		contextual[key] = value
	}
	return contextual
}

// RunCampaign 在 Windows x64 上执行已安装 packaged MCP 的固定验证包。
func RunCampaign(ctx context.Context, options RunOptions) (report CampaignReport, runErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationCampaign")
	stage := "platform_gate"
	campaignID := ""
	lane := ""
	defer func() {
		if runErr != nil {
			log.WithErr(runErr).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID, "lane": lane}).Error("Windows packaged MCP campaign 失败")
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
	lane = laneOrDefault(input.Lane)
	stage = "verify_lane_installer"
	installerVerificationStarted := time.Now().UTC()
	installerChecks, err := VerifyInstallerForLane(input.InstallerDirectory, lane, source.Frozen.Installers)
	if err != nil {
		return CampaignReport{}, err
	}
	installer, err := artifactOnlyInstaller(lane, installerChecks, installerVerificationStarted, time.Now().UTC())
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
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "package_root": options.PackageRoot, "results_dir": resultsDir}).Info("开始 Windows packaged MCP campaign")
	stage = "start_packaged_mcp"
	client, err := StartMCPProcess(ctx, input.MCPPath, agentURL)
	if err != nil {
		persistGateFailure(resultsDir, redactor, source, input, campaignID, started, installerChecks, RuntimeAttestation{}, stage, err)
		return CampaignReport{}, err
	}
	clientClosed := false
	defer func() {
		if !clientClosed {
			_ = client.Close()
		}
	}()
	stage = "runtime_attestation"
	attestation, err := attestRuntime(ctx, client, source, input.MCPPath, resultsDir, redactor, campaignID, lane)
	if err != nil {
		log.WithErr(err).WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "stage": stage}).Error("运行时身份门禁失败")
		persistGateFailure(resultsDir, redactor, source, input, campaignID, started, installerChecks, attestation, stage, err)
		return CampaignReport{}, err
	}
	if lane == "msi_smoke" {
		stage = "stop_packaged_mcp"
		stopExecution := recordPackagedMCPStop(client, resultsDir, redactor, campaignID, lane)
		clientClosed = true
		functional := aggregateResult("MSI packaged runtime smoke", 2, []ValidationResult{attestation.Result, stopExecution.Result})
		report := CampaignReport{
			SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
			Functional:  functional,
			BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
			Target: "Windows 10 x64", Lane: "msi_smoke", Installer: installer, RuntimeAttestation: attestation,
			InstallerChecks:   installerChecks,
			Operations:        []StepExecution{stopExecution},
			ValidationCatalog: buildValidationCatalog(source.Scenarios, source.Coverage),
			ToolRows:          ensureAllToolRows(source.Coverage, nil, notRunResult("not executed in the independent MSI smoke lane")),
			Providers:         notRunProviderMatrix(source.Fixtures, "not executed in the independent MSI smoke lane"),
			Scenarios:         notRunScenarioMatrix(source.Scenarios, "not executed in the independent MSI smoke lane"),
			Cleanup:           pendingCleanupRecord("run Cleanup-Validation.ps1 to compare and restore the prepared baseline", workspaceRoot),
			KnownAnomalies:    source.Frozen.KnownBaselineExceptions, StartedAtUTC: started.Format(time.RFC3339Nano),
			FinishedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		}
		report.Sections = buildReportSections(report)
		report.Result = deriveCampaignCompletionResult("MSI smoke campaign", report)
		stage = "write_msi_report"
		if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
			return CampaignReport{}, err
		}
		if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
			return CampaignReport{}, err
		}
		log.WithFields(map[string]any{"campaign_id": campaignID, "lane": "msi_smoke", "phase_status": report.Result.PhaseStatus, "attempted": report.Result.Attempted}).Info("Windows MSI packaged sidecar smoke 完成")
		return report, nil
	}
	executor := &ScenarioExecutor{
		client: client, redactor: redactor, resultsDir: resultsDir,
		campaignID: campaignID, lane: lane,
		variables: variables, passed: map[string]bool{},
	}
	scenarios := orderedScenarios(source.Scenarios)
	executions := make([]ScenarioExecution, 0, len(scenarios))
	campaignPrerequisites := make([]StepExecution, 0, 1)
	configReady := false
	for _, scenario := range scenarios {
		// provider 矩阵必须在远端 pipeline 之前完成；remote 场景在下方单独执行。
		if scenario.ID == "remote-pipeline" {
			continue
		}
		if !configReady && scenario.ID != "identity-observation" && scenario.ID != "config-security-lifecycle" {
			execution := executor.blockScenario(scenario, "campaign_configuration", "campaign configuration gate did not pass")
			executions = append(executions, execution)
			continue
		}
		if scenario.ID == "browser-debug" || scenario.ID == "code-debug" {
			prerequisite := executor.ensureGoRunning(ctx, scenario.ID+"-go-fixture-running")
			if prerequisite.Result.PhaseStatus != PhaseStatusPass {
				execution := executor.blockScenario(scenario, prerequisite.StepID, "Go fixture precondition failed: "+resultReason(prerequisite.Result))
				execution.Prerequisites = append(execution.Prerequisites, prerequisite)
				executions = append(executions, execution)
				continue
			}
			execution := executor.ExecuteScenario(ctx, scenario)
			execution.Prerequisites = append(execution.Prerequisites, prerequisite)
			executions = append(executions, execution)
			continue
		}
		execution := executor.ExecuteScenario(ctx, scenario)
		executions = append(executions, execution)
		if scenario.ID == "config-security-lifecycle" {
			// 冻结产品的 get_debug_credentials 只能读取持久明文，安全验证会诚实 FAIL；
			// 该产品缺口不能抹掉已经成功建立的 campaign 隔离配置，也不能阻断其余工具取证。
			configReady = executor.configurationReady()
		}
	}
	providers := blockedProviderMatrix(source.Fixtures, "campaign_configuration", "campaign configuration gate did not establish a project and Go deployment")
	if configReady {
		prerequisite := executor.ensureGoStopped(ctx, "provider-go-fixture-stopped")
		campaignPrerequisites = append(campaignPrerequisites, prerequisite)
		if prerequisite.Result.PhaseStatus != PhaseStatusPass {
			reason := "primary Go fixture cleanup gate failed: " + resultReason(prerequisite.Result)
			providers = blockedProviderMatrix(source.Fixtures, prerequisite.StepID, reason)
		} else {
			providers = executor.ExecuteProviderMatrix(ctx, source.Fixtures)
		}
	}
	if remote, found := scenarioByID(scenarios, "remote-pipeline"); found {
		if !configReady {
			executions = append(executions, executor.blockScenario(remote, "campaign_configuration", "campaign configuration gate did not pass"))
		} else {
			available, prerequisite := executor.preflightRemoteHost(ctx, input.LinuxHostID)
			switch {
			case prerequisite.Result.PhaseStatus != PhaseStatusPass:
				execution := executor.blockScenario(remote, "remote_host_available", "remote Host ID preflight failed: "+resultReason(prerequisite.Result))
				execution.Prerequisites = append(execution.Prerequisites, prerequisite)
				executions = append(executions, execution)
			case !available:
				execution := executor.blockScenario(remote, "remote_host_available", "configured dedicated Linux Host ID is not currently available as a non-self target")
				execution.Prerequisites = append(execution.Prerequisites, prerequisite)
				executions = append(executions, execution)
			default:
				execution := executor.ExecuteScenario(ctx, remote)
				execution.Prerequisites = append(execution.Prerequisites, prerequisite)
				executions = append(executions, execution)
			}
		}
	}
	toolRows := ensureAllToolRows(source.Coverage, executor.toolRows, notRunResult("primary step was not reached"))
	functionalResult := aggregateCampaignResult(toolRows, providers, executions, source.Frozen.SourceSurface.MCPTools.Names, source.Frozen.SourceSurface.LanguageRuntimeProviders.Names, source.Coverage)
	stage = "stop_packaged_mcp"
	stopExecution := recordPackagedMCPStop(client, resultsDir, redactor, campaignID, lane)
	clientClosed = true
	functionalResult = aggregateResult("NSIS functional execution and packaged MCP stop", 2, []ValidationResult{functionalResult, stopExecution.Result})
	report = CampaignReport{
		SchemaVersion:      2,
		Kind:               "superdev.windows-validation.campaign-report",
		CampaignID:         campaignID,
		Functional:         functionalResult,
		BuildCommit:        source.Frozen.Build.GitCommit,
		ProductVersion:     source.Frozen.Build.ProductVersion,
		Target:             "Windows 10 x64",
		Lane:               laneOrDefault(input.Lane),
		Installer:          installer,
		RuntimeAttestation: attestation,
		InstallerChecks:    installerChecks,
		Operations:         []StepExecution{stopExecution},
		ValidationCatalog:  buildValidationCatalog(source.Scenarios, source.Coverage),
		Scenarios:          executions,
		Providers:          providers,
		ToolRows:           toolRows,
		Prerequisites:      campaignPrerequisites,
		Cleanup:            pendingCleanupRecord("run Cleanup-Validation.ps1 to compare and restore the prepared baseline", workspaceRoot),
		KnownAnomalies:     source.Frozen.KnownBaselineExceptions,
		StartedAtUTC:       started.Format(time.RFC3339Nano),
		FinishedAtUTC:      time.Now().UTC().Format(time.RFC3339Nano),
	}
	report.Sections = buildReportSections(report)
	report.Result = deriveCampaignCompletionResult("NSIS core campaign", report)
	stage = "write_nsis_report"
	if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "complete"
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "phase_status": report.Result.PhaseStatus, "functional_status": functionalResult.PhaseStatus, "tool_rows": len(toolRows), "provider_rows": len(providers)}).Info("Windows packaged MCP campaign 执行完成；等待最终 cleanup")
	return report, nil
}

func writeCampaignReports(resultsDir string, redactor *Redactor, report CampaignReport) error {
	derived, err := rederiveCampaignReport(report)
	if err != nil {
		return fmt.Errorf("derive campaign report before persistence: %w", err)
	}
	report = derived
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

func persistGateFailure(resultsDir string, redactor *Redactor, source PackageSource, input RuntimeInput, campaignID string, started time.Time, installerChecks []PackageFileIdentity, attestation RuntimeAttestation, stage string, cause error) {
	reason := stage + ": " + cause.Error()
	installer, installerErr := artifactOnlyInstaller(laneOrDefault(input.Lane), installerChecks, started, time.Now().UTC())
	if installerErr != nil {
		logger.GetLogger().WithEntryName("WindowsValidationReport").WithErr(installerErr).WithFields(map[string]any{"campaign_id": campaignID, "lane": laneOrDefault(input.Lane), "stage": stage}).Error("身份门禁失败时无法派生安装包事实")
		return
	}
	if attestation.Result.PhaseStatus == "" {
		attestation.Result = blockedResult("packaged_mcp_started", reason)
		if stage == "runtime_attestation" {
			attestation.Result = attemptedResult(false, cause.Error(), started.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), nil)
		}
	}
	functional := blockedResult(stage, reason)
	prerequisiteResult := attemptedResult(false, cause.Error(), started.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano), nil)
	if stage == "runtime_attestation" && attestation.Result.PhaseStatus != "" {
		prerequisiteResult = attestation.Result
	}
	prerequisite := StepExecution{
		StepID: stage, Result: prerequisiteResult,
	}
	blocked := blockedResult(stage, reason)
	report := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Functional: functional, FailureStage: stage, FailureReason: cause.Error(),
		BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
		Target: "Windows 10 x64", Lane: laneOrDefault(input.Lane),
		Installer: installer, RuntimeAttestation: attestation, InstallerChecks: installerChecks,
		ValidationCatalog: buildValidationCatalog(source.Scenarios, source.Coverage),
		Prerequisites:     []StepExecution{prerequisite},
		Scenarios:         blockedScenarioMatrix(source.Scenarios, stage, reason),
		Providers:         blockedProviderMatrix(source.Fixtures, stage, reason),
		ToolRows:          ensureAllToolRows(source.Coverage, nil, blocked),
		Cleanup:           pendingCleanupRecord("run Cleanup-Validation.ps1 even after a gate failure", ""),
		KnownAnomalies:    source.Frozen.KnownBaselineExceptions,
		StartedAtUTC:      started.Format(time.RFC3339Nano), FinishedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
	}
	report.Sections = buildReportSections(report)
	report.Result = deriveCampaignCompletionResult("failed campaign gate", report)
	log := logger.GetLogger().WithEntryName("WindowsValidationReport")
	if err := writeCampaignReports(resultsDir, redactor, report); err != nil {
		log.WithErr(err).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID, "lane": laneOrDefault(input.Lane)}).Error("身份门禁失败报告写入失败")
		return
	}
	if err := writeValidationSummary(input.ResultsRoot, redactor, report); err != nil {
		log.WithErr(err).WithFields(map[string]any{"stage": stage, "campaign_id": campaignID, "lane": laneOrDefault(input.Lane)}).Error("身份门禁失败聚合摘要写入失败")
	}
}

// ExecuteScenario 执行一个固定场景；失败后仍执行受 guard 保护的 cleanup。
func (e *ScenarioExecutor) ExecuteScenario(ctx context.Context, scenario Scenario) (execution ScenarioExecution) {
	log := logger.GetLogger().WithEntryName("WindowsValidationScenario")
	log.WithFields(e.logFields(map[string]any{"scenario": scenario.ID, "step_count": len(scenario.Steps)})).Info("开始执行固定验证场景")
	defer func() {
		fields := e.logFields(map[string]any{"scenario": scenario.ID, "phase_status": execution.Result.PhaseStatus, "attempted": execution.Result.Attempted})
		if execution.Result.PhaseStatus == PhaseStatusFail {
			log.WithFields(fields).Error("固定验证场景执行失败")
		} else {
			log.WithFields(fields).Info("固定验证场景执行完成")
		}
	}()
	execution = ScenarioExecution{ID: scenario.ID, Title: scenario.Title}
	variableCheckStarted := time.Now().UTC()
	if err := e.mergeScenarioVariables(scenario); err != nil {
		execution = e.blockScenario(scenario, "scenario_variables", err.Error())
		execution.Prerequisites = append(execution.Prerequisites, e.recordLocalPrerequisiteFailure(scenario.ID, "scenario-variables", "validate_scenario_variables", variableCheckStarted, err))
		return execution
	}
	blockingStep := ""
	for _, step := range scenario.Steps {
		if blockingStep != "" {
			reason := "blocked by earlier step " + blockingStep + " in the same scenario"
			blocked := StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: blockedResult(blockingStep, reason)}
			execution.Steps = append(execution.Steps, blocked)
			e.appendToolRow(scenario.ID, step, blocked)
			continue
		}
		result := e.executeStep(ctx, scenario.ID, step)
		execution.Steps = append(execution.Steps, result)
		e.passed[step.ID] = result.Result.PhaseStatus == PhaseStatusPass
		e.appendToolRow(scenario.ID, step, result)
		if result.Result.PhaseStatus != PhaseStatusPass {
			blockingStep = step.ID
		}
	}
	for _, step := range scenario.Cleanup {
		if !ShouldRunCleanup(step.RunIf, e.variables, e.passed) {
			execution.Cleanup = append(execution.Cleanup, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage,
				Result: notRunResult("cleanup guard did not require this fixed step"),
			})
			continue
		}
		result := e.executeStep(ctx, scenario.ID+"-cleanup", step)
		execution.Cleanup = append(execution.Cleanup, result)
	}
	children := make([]ValidationResult, 0, len(execution.Steps)+len(execution.Cleanup))
	for _, step := range execution.Steps {
		children = append(children, step.Result)
	}
	for _, step := range execution.Cleanup {
		if step.Result.PhaseStatus != PhaseStatusNotRun {
			children = append(children, step.Result)
		}
	}
	execution.Result = aggregateResult(scenario.ID+" scenario", len(children), children)
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

type mcpCallAttempt struct {
	StartedAtUTC  string
	FinishedAtUTC string
	Response      map[string]any
	TransportErr  string
	AssertionErr  string
}

func (e *ScenarioExecutor) executeStep(ctx context.Context, scenarioID string, step ScenarioStep) StepExecution {
	started := time.Now().UTC()
	log := logger.GetLogger().WithEntryName("WindowsValidationStep")
	log.WithFields(e.logFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool})).Info("开始执行 MCP 验证步骤")
	rendered, err := RenderValue(step.Arguments, e.variables)
	if err != nil {
		prerequisite := e.recordLocalPrerequisiteFailure(scenarioID, step.ID+"-rendered-arguments", "render_arguments", started, err)
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: blockedResult("rendered_arguments", err.Error()), Prerequisites: []StepExecution{prerequisite}}
	}
	arguments, ok := rendered.(map[string]any)
	if !ok {
		renderErr := fmt.Errorf("rendered arguments are not an object")
		prerequisite := e.recordLocalPrerequisiteFailure(scenarioID, step.ID+"-rendered-arguments", "validate_rendered_arguments", started, renderErr)
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: blockedResult("rendered_arguments", renderErr.Error()), Prerequisites: []StepExecution{prerequisite}}
	}
	deadline := time.Now()
	if step.Poll != nil {
		deadline = deadline.Add(time.Duration(step.Poll.TimeoutSeconds) * time.Second)
	}
	var result ToolCallResult
	var outcome string
	var attempts []mcpCallAttempt
	for {
		attemptStarted := time.Now().UTC()
		callResult, callErr := e.client.CallTool(ctx, step.Tool, arguments)
		attemptFinished := time.Now().UTC()
		assertionErr := callErr
		attemptOutcome := ""
		if assertionErr == nil {
			attemptOutcome, assertionErr = EvaluateStepResult(step, callResult, e.variables)
		}
		attempt := mcpCallAttempt{
			StartedAtUTC: attemptStarted.Format(time.RFC3339Nano), FinishedAtUTC: attemptFinished.Format(time.RFC3339Nano),
			Response: RawMessageMap(callResult),
		}
		if callErr != nil {
			attempt.TransportErr = callErr.Error()
		}
		if assertionErr != nil {
			attempt.AssertionErr = assertionErr.Error()
		}
		attempts = append(attempts, attempt)
		result = callResult
		err = assertionErr
		outcome = attemptOutcome
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
		if ctx.Err() != nil {
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
	evidence, inlineEvidence, evidenceErr := e.recordStepEvidence(scenarioID, step, arguments, attempts, root, err)
	if err == nil && evidenceErr != nil {
		err = evidenceErr
	}
	finished := time.Now().UTC()
	failure := ""
	if err != nil {
		failure = err.Error()
	}
	resultContract := attemptedResult(err == nil, failure, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), evidence)
	if err != nil {
		log.WithErr(err).WithFields(e.logFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool, "attempt_count": len(attempts), "evidence_count": len(evidence)})).Error("MCP 验证步骤失败")
		return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: resultContract, Outcome: outcome, InlineEvidence: inlineEvidence}
	}
	log.WithFields(e.logFields(map[string]any{"scenario": scenarioID, "step": step.ID, "tool": step.Tool, "outcome": outcome, "attempt_count": len(attempts), "evidence_count": len(evidence), "duration_ms": finished.Sub(started).Milliseconds()})).Info("MCP 验证步骤完成")
	return StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: resultContract, Outcome: outcome, InlineEvidence: inlineEvidence}
}

func (e *ScenarioExecutor) recordStepEvidence(scenarioID string, step ScenarioStep, arguments map[string]any, attempts []mcpCallAttempt, response map[string]any, stepErr error) ([]EvidenceRecord, map[string]any, error) {
	root := filepath.Join(e.resultsDir, "evidence", scenarioID, step.ID)
	relativeRoot := filepath.ToSlash(filepath.Join("evidence", scenarioID, step.ID))
	attemptsRef := filepath.ToSlash(filepath.Join(relativeRoot, "attempts.json"))
	selectedRef := filepath.ToSlash(filepath.Join(relativeRoot, "evidence.json"))
	records := []EvidenceRecord{
		{Name: "call_attempts", Required: true, Ref: attemptsRef},
		{Name: "selected_evidence", Required: true, Ref: selectedRef},
	}
	attemptRows := make([]any, 0, len(attempts))
	for index, attempt := range attempts {
		requestCopy := cloneJSONMap(map[string]any{"tool": step.Tool, "arguments": arguments})
		responseCopy := cloneJSONMap(attempt.Response)
		applyEvidenceRedactions(requestCopy, responseCopy, step.Evidence.Redact, e.redactor)
		attemptRows = append(attemptRows, map[string]any{
			"attempt": index + 1, "started_at_utc": attempt.StartedAtUTC, "finished_at_utc": attempt.FinishedAtUTC,
			"request": requestCopy, "normalized_response": responseCopy,
			"transport_error": attempt.TransportErr, "assertion_error": attempt.AssertionErr,
		})
	}
	redactedAttempts := e.redactor.Redact(map[string]any{"attempts": attemptRows})
	if e.redactor.containsKnownSecret(redactedAttempts) {
		err := fmt.Errorf("redaction invariant failed before writing MCP attempt evidence")
		records[0].WriteError = err.Error()
		return records, nil, err
	}
	inline := map[string]any{"call_attempts": redactedAttempts}
	if err := writeJSON(filepath.Join(root, "attempts.json"), redactedAttempts); err != nil {
		records[0].WriteError = err.Error()
		return records, inline, err
	}
	records[0].Present = true
	responseCopy := cloneJSONMap(response)
	requestCopy := cloneJSONMap(map[string]any{"tool": step.Tool, "arguments": arguments})
	applyEvidenceRedactions(requestCopy, responseCopy, step.Evidence.Redact, e.redactor)
	selected := map[string]any{}
	for _, record := range step.Evidence.Record {
		if strings.HasPrefix(record, "sha256:") {
			path := strings.TrimPrefix(record, "sha256:")
			value, found := LookupPath(response, path)
			if !found {
				err := fmt.Errorf("evidence hash path %s missing", path)
				records[1].WriteError = err.Error()
				inline["selected_evidence"] = selected
				return records, inline, err
			}
			digest, size, err := digestEvidenceValue(value)
			if err != nil {
				records[1].WriteError = err.Error()
				inline["selected_evidence"] = selected
				return records, inline, err
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
		err := fmt.Errorf("selected evidence still contains a registered secret")
		records[1].WriteError = err.Error()
		return records, nil, err
	}
	encoded := strings.ToLower(CanonicalJSON(selected))
	for _, forbidden := range step.Evidence.Forbid {
		if forbidden != "" && strings.Contains(encoded, strings.ToLower(forbidden)) {
			err := fmt.Errorf("selected evidence contains forbidden marker %q", forbidden)
			records[1].WriteError = err.Error()
			return records, nil, err
		}
	}
	inline["selected_evidence"] = selected
	if err := writeJSON(filepath.Join(root, "evidence.json"), selected); err != nil {
		records[1].WriteError = err.Error()
		return records, inline, err
	}
	records[1].Present = true
	return records, nil, nil
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
		Tool: step.Tool, ScenarioID: scenarioID, StepID: step.ID, Result: execution.Result, Outcome: execution.Outcome, InlineEvidence: execution.InlineEvidence,
	})
}

func (e *ScenarioExecutor) blockScenario(scenario Scenario, prerequisite, reason string) ScenarioExecution {
	logger.GetLogger().WithEntryName("WindowsValidationScenario").WithFields(e.logFields(map[string]any{"scenario": scenario.ID, "prerequisite": prerequisite, "reason": reason})).Info("固定验证场景受前置条件阻断")
	execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title}
	for _, step := range scenario.Steps {
		blocked := StepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: blockedResult(prerequisite, reason)}
		execution.Steps = append(execution.Steps, blocked)
		e.appendToolRow(scenario.ID, step, blocked)
	}
	for _, step := range scenario.Cleanup {
		execution.Cleanup = append(execution.Cleanup, StepExecution{
			StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage,
			Result: notRunResult("scenario was blocked before cleanup resources could exist"),
		})
	}
	execution.Result = aggregateStepExecutions(scenario.ID+" scenario", execution.Steps)
	return execution
}

func blockedScenarioMatrix(scenarios []Scenario, prerequisite, reason string) []ScenarioExecution {
	results := make([]ScenarioExecution, 0, len(scenarios))
	for _, scenario := range orderedScenarios(scenarios) {
		execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title}
		for _, step := range scenario.Steps {
			execution.Steps = append(execution.Steps, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: blockedResult(prerequisite, reason),
			})
		}
		for _, step := range scenario.Cleanup {
			execution.Cleanup = append(execution.Cleanup, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage,
				Result: notRunResult("scenario was blocked before cleanup resources could exist"),
			})
		}
		execution.Result = aggregateStepExecutions(scenario.ID+" scenario", execution.Steps)
		results = append(results, execution)
	}
	return results
}

func notRunScenarioMatrix(scenarios []Scenario, reason string) []ScenarioExecution {
	results := make([]ScenarioExecution, 0, len(scenarios))
	for _, scenario := range orderedScenarios(scenarios) {
		execution := ScenarioExecution{ID: scenario.ID, Title: scenario.Title}
		for _, step := range scenario.Steps {
			execution.Steps = append(execution.Steps, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: notRunResult(reason),
			})
		}
		for _, step := range scenario.Cleanup {
			execution.Cleanup = append(execution.Cleanup, StepExecution{
				StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Result: notRunResult(reason),
			})
		}
		execution.Result = aggregateStepExecutions(scenario.ID+" scenario", execution.Steps)
		results = append(results, execution)
	}
	return results
}

func (e *ScenarioExecutor) configurationReady() bool {
	projectID, _ := e.variables["project_id"].(string)
	return strings.TrimSpace(projectID) != "" && e.passed["upsert-go-service"] && e.passed["upsert-bootstrap-pipeline"]
}

func (e *ScenarioExecutor) preflightRemoteHost(ctx context.Context, hostID string) (bool, StepExecution) {
	log := logger.GetLogger().WithEntryName("WindowsValidationRemotePreflight")
	log.WithFields(e.logFields(map[string]any{"host_id": hostID, "tool": "list_hosts"})).Info("开始核对专用 Linux Host ID")
	started := time.Now().UTC()
	result, err := e.client.CallTool(ctx, "list_hosts", map[string]any{})
	finished := time.Now().UTC()
	root := RawMessageMap(result)
	available := false
	failure := ""
	switch {
	case err != nil:
		failure = err.Error()
	case result.IsError:
		failure = "list_hosts returned " + toolErrorCode(result)
	default:
		available = remoteHostPresent(root, hostID)
		if !available {
			failure = "configured dedicated Linux Host ID is not available as a non-self target"
		}
	}
	relative := filepath.ToSlash(filepath.Join("evidence", "remote-host-preflight.json"))
	attempt := assertionAttempt("tools/call", map[string]any{"tool": "list_hosts", "arguments": map[string]any{}}, root, started, finished, err)
	attempt.Tool = "list_hosts"
	if result.IsError {
		attempt.ProductError = toolErrorCode(result)
	}
	if err == nil && failure != "" {
		attempt.AssertionError = failure
	}
	evidence, safePayload := persistMCPAttemptEvidence(e.resultsDir, relative, "remote_host_preflight", "superdev.windows-validation.remote-host-preflight", []mcpEvidenceAttempt{attempt}, map[string]any{
		"campaign_id": e.campaignID, "lane": e.lane, "step_id": "remote-host-preflight", "stage": "remote_host_preflight", "tool": "list_hosts",
		"execution_facts": map[string]any{
			"attempted": true, "succeeded": failure == "", "failure": failure,
			"started_at_utc": started.Format(time.RFC3339Nano), "finished_at_utc": finished.Format(time.RFC3339Nano),
		},
	}, e.redactor)
	outcome := "host_unavailable"
	if available {
		outcome = "host_available"
	}
	contract := attemptedResult(failure == "", failure, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), []EvidenceRecord{evidence})
	fields := e.logFields(map[string]any{"host_id": hostID, "tool": "list_hosts", "available": available, "phase_status": contract.PhaseStatus, "evidence_ref": relative})
	if contract.PhaseStatus == PhaseStatusPass {
		log.WithFields(fields).Info("专用 Linux Host ID 预检完成")
	} else {
		log.WithFields(fields).WithField("failure", resultReason(contract)).Error("专用 Linux Host ID 预检失败")
	}
	inline := map[string]any(nil)
	if !evidence.Present {
		inline = safePayload
	}
	return available, StepExecution{StepID: "remote-host-preflight", Tool: "list_hosts", Coverage: CoverageSupporting, Outcome: outcome, Result: contract, InlineEvidence: inline}
}

func aggregateStepExecutions(name string, steps []StepExecution) ValidationResult {
	children := make([]ValidationResult, 0, len(steps))
	for _, step := range steps {
		children = append(children, step.Result)
	}
	return aggregateResult(name, len(children), children)
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

func (e *ScenarioExecutor) ensureGoRunning(ctx context.Context, stepID string) StepExecution {
	started := time.Now().UTC()
	attempts := make([]mcpEvidenceAttempt, 0, 4)
	projectID, _ := e.variables["project_id"].(string)
	deploymentID, _ := e.variables["go_deployment_id"].(string)
	if projectID == "" || deploymentID == "" {
		return StepExecution{StepID: stepID, Coverage: CoverageSupporting, Result: blockedResult("go_fixture_identity", "Go project/deployment identity is missing")}
	}
	result, attempt, err := observeToolCall(ctx, e.client, "start_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300})
	attempts = append(attempts, attempt)
	if err != nil {
		return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("start Go fixture", err.Error()))
	}
	if result.IsError {
		code := toolErrorCode(result)
		if code != "deployment_already_running" && code != "already_running" {
			return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("start Go fixture", code))
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	lastFailure := ""
	for time.Now().Before(deadline) {
		state, stateAttempt, callErr := observeToolCall(ctx, e.client, "list_services", map[string]any{"project_id": projectID})
		attempts = append(attempts, stateAttempt)
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "running" {
			return e.finishSupportingMCPAction(stepID, started, attempts, true, "")
		}
		switch {
		case callErr != nil:
			lastFailure = callErr.Error()
		case state.IsError:
			lastFailure = "list_services returned " + toolErrorCode(state)
		default:
			lastFailure = "observed deployment status " + deploymentStatus(state, deploymentID)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("wait for Go fixture running", ctx.Err().Error()))
		case <-timer.C:
		}
	}
	return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("Go fixture did not reach running", lastFailure))
}

func (e *ScenarioExecutor) ensureGoStopped(ctx context.Context, stepID string) StepExecution {
	started := time.Now().UTC()
	attempts := make([]mcpEvidenceAttempt, 0, 4)
	projectID, _ := e.variables["project_id"].(string)
	deploymentID, _ := e.variables["go_deployment_id"].(string)
	if projectID == "" || deploymentID == "" {
		return StepExecution{StepID: stepID, Coverage: CoverageSupporting, Result: blockedResult("go_fixture_identity", "Go project/deployment identity is missing")}
	}
	state, stateAttempt, stateErr := observeToolCall(ctx, e.client, "list_services", map[string]any{"project_id": projectID})
	attempts = append(attempts, stateAttempt)
	if stateErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
		return e.finishSupportingMCPAction(stepID, started, attempts, true, "")
	}
	result, stopAttempt, err := observeToolCall(ctx, e.client, "stop_service", map[string]any{"project_id": projectID, "deployment_id": deploymentID, "approval_wait_seconds": 300})
	attempts = append(attempts, stopAttempt)
	if err != nil {
		return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("stop Go fixture", err.Error()))
	}
	if result.IsError {
		code := toolErrorCode(result)
		if code != "deployment_already_stopped" && code != "already_stopped" {
			return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("stop Go fixture", code))
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	lastFailure := ""
	for time.Now().Before(deadline) {
		state, pollAttempt, callErr := observeToolCall(ctx, e.client, "list_services", map[string]any{"project_id": projectID})
		attempts = append(attempts, pollAttempt)
		if callErr == nil && !state.IsError && deploymentStatus(state, deploymentID) == "stopped" {
			return e.finishSupportingMCPAction(stepID, started, attempts, true, "")
		}
		switch {
		case callErr != nil:
			lastFailure = callErr.Error()
		case state.IsError:
			lastFailure = "list_services returned " + toolErrorCode(state)
		default:
			lastFailure = "observed deployment status " + deploymentStatus(state, deploymentID)
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("wait for Go fixture stopped", ctx.Err().Error()))
		case <-timer.C:
		}
	}
	return e.finishSupportingMCPAction(stepID, started, attempts, false, supportingFailure("Go fixture did not reach stopped", lastFailure))
}

func (e *ScenarioExecutor) finishSupportingMCPAction(stepID string, started time.Time, attempts []mcpEvidenceAttempt, succeeded bool, failure string) StepExecution {
	finished := time.Now().UTC()
	relative := filepath.ToSlash(filepath.Join("evidence", "prerequisites", stepID+".json"))
	evidence, safePayload := persistMCPAttemptEvidence(e.resultsDir, relative, stepID, "superdev.windows-validation.prerequisite-evidence", attempts, map[string]any{
		"campaign_id": e.campaignID, "lane": e.lane, "step_id": stepID,
		"execution_facts": map[string]any{
			"attempted": true, "succeeded": succeeded, "failure": failure,
			"started_at_utc": started.Format(time.RFC3339Nano), "finished_at_utc": finished.Format(time.RFC3339Nano),
		},
	}, e.redactor)
	result := attemptedResult(succeeded, failure, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), []EvidenceRecord{evidence})
	fields := e.logFields(map[string]any{"step": stepID, "phase_status": result.PhaseStatus, "attempted": result.Attempted, "attempt_count": len(attempts), "evidence_ref": relative})
	log := logger.GetLogger().WithEntryName("WindowsValidationPrerequisite")
	if result.PhaseStatus == PhaseStatusPass {
		log.WithFields(fields).Info("Windows MCP 前置动作完成")
	} else {
		log.WithFields(fields).Error("Windows MCP 前置动作失败")
	}
	inline := map[string]any(nil)
	if !evidence.Present {
		inline = safePayload
	}
	return StepExecution{StepID: stepID, Coverage: CoverageSupporting, Result: result, InlineEvidence: inline}
}

func blockedProviderMatrix(fixtures []FixtureManifest, prerequisite, reason string) []ProviderExecution {
	results := make([]ProviderExecution, 0, len(fixtures))
	for _, fixture := range fixtures {
		runtimeResult := blockedResult(prerequisite, reason)
		debugResult := blockedResult(prerequisite, reason)
		results = append(results, ProviderExecution{
			Provider: fixture.Provider, Runtime: runtimeResult, Debug: debugResult,
			Result: aggregateResult(fixture.Provider+" provider", 2, []ValidationResult{runtimeResult, debugResult}), Reason: reason,
		})
	}
	return results
}

func notRunProviderMatrix(fixtures []FixtureManifest, reason string) []ProviderExecution {
	results := make([]ProviderExecution, 0, len(fixtures))
	for _, fixture := range fixtures {
		runtimeResult := notRunResult(reason)
		debugResult := notRunResult(reason)
		results = append(results, ProviderExecution{
			Provider: fixture.Provider, Runtime: runtimeResult, Debug: debugResult,
			Result: aggregateResult(fixture.Provider+" provider", 2, []ValidationResult{runtimeResult, debugResult}), Reason: reason,
		})
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

func aggregateCampaignResult(rows []ToolEvidenceRow, providers []ProviderExecution, scenarios []ScenarioExecution, expectedToolNames, expectedProviderNames []string, expectedCoverage []CoverageAssignment) ValidationResult {
	children := make([]ValidationResult, 0, 2+len(scenarios))
	children = append(children, aggregateToolResult(rows, expectedToolNames, expectedCoverage))
	children = append(children, aggregateProviderResult(providers, expectedProviderNames))
	for _, scenario := range scenarios {
		children = append(children, scenario.Result)
	}
	return aggregateResult("Windows functional validation", len(children), children)
}

func ensureAllToolRows(assignments []CoverageAssignment, rows []ToolEvidenceRow, missing ValidationResult) []ToolEvidenceRow {
	byTool := map[string]ToolEvidenceRow{}
	for _, row := range rows {
		byTool[row.Tool] = row
	}
	out := make([]ToolEvidenceRow, 0, len(assignments))
	for _, assignment := range assignments {
		row, ok := byTool[assignment.Tool]
		if !ok {
			row = ToolEvidenceRow{Tool: assignment.Tool, ScenarioID: assignment.ScenarioID, StepID: assignment.StepID, Result: missing}
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	return out
}

func artifactOnlyInstaller(lane string, checks []PackageFileIdentity, started, finished time.Time) (InstallerExecution, error) {
	if len(checks) != 1 {
		return InstallerExecution{}, fmt.Errorf("installer artifact verification produced %d identities, want 1", len(checks))
	}
	format := "nsis"
	if laneOrDefault(lane) == "msi_smoke" {
		format = "msi"
	}
	lifecycleReason := "installer lifecycle facts were not recorded by artifact verification"
	notRun := ResultInput{Facts: ExecutionFacts{NotRunReason: lifecycleReason}}
	return DeriveInstallerExecution(InstallerExecutionFacts{
		Format: format, ArtifactVerified: true, InstallerExecuted: false,
		Artifact: ResultInput{
			Facts:    ExecutionFacts{Attempted: true, Succeeded: true, StartedAtUTC: started.Format(time.RFC3339Nano), FinishedAtUTC: finished.Format(time.RFC3339Nano)},
			Evidence: []EvidenceRecord{{Name: "installer_identity", Required: true, Present: true, Ref: "campaign-report.json#installer_checks"}},
		},
		Install: notRun, Start: notRun, Stop: notRun, Uninstall: notRun,
	})
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
