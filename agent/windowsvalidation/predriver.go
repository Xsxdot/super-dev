// predriver.go 把 PowerShell 前门禁失败事实交给统一结果模块生成正常 campaign 报告。
//
// 职责：
//   - 校验 prepared backup、campaign 与冻结包身份
//   - 保留 pre-driver prerequisite 的独立失败事实
//   - 由统一派生模块生成七 provider、全部 scenario 和 75 工具的未尝试结果
//
// 边界：
//   - 不执行 MCP、安装器、provider 或 cleanup
//   - 不接受 PowerShell 手工填写的 Phase Status
//   - 不从安装包文件或 backup 身份推断 Installer Lifecycle 已执行
package windowsvalidation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

type preparedBackupManifest struct {
	SchemaVersion          int               `json:"schema_version"`
	Kind                   string            `json:"kind"`
	Status                 string            `json:"status"`
	CreatedAtUTC           string            `json:"created_at_utc"`
	Lane                   string            `json:"lane"`
	CampaignID             string            `json:"campaign_id"`
	BaselineSHA256         string            `json:"baseline_sha256"`
	BaselineCategorySHA256 map[string]string `json:"baseline_category_sha256"`
}

type preDriverFailure struct {
	SchemaVersion        int                   `json:"schema_version"`
	Kind                 string                `json:"kind"`
	CampaignID           string                `json:"campaign_id"`
	Lane                 string                `json:"lane"`
	Stage                string                `json:"stage"`
	Execution            ExecutionFacts        `json:"execution_facts"`
	ArtifactVerification ExecutionFacts        `json:"artifact_verification"`
	InstallerChecks      []PackageFileIdentity `json:"installer_checks"`
	Error                string                `json:"error"`
	ObservedAtUTC        string                `json:"observed_at_utc"`
}

// MaterializePreDriverFailure 根据 PowerShell 记录的前门禁失败事实生成统一 campaign 报告。
//
// 参数：
//   - packageRoot: 已解压且包含冻结 manifest、scenario 与模板的验证包根目录
//   - resultsRoot: campaign 结果根目录
//   - backupDirectory: Prepare-Validation.ps1 创建的精确 prepared backup
//   - campaignID: 与 prepared backup 绑定的 campaign 身份
//
// 返回：
//   - 包含独立 prerequisite 失败和全部未尝试目标的 CampaignReport
//   - 身份、输入读取、结果派生或报告写入错误
//
// 注意：该入口只在正常 driver 未产生 campaign-report.json 时调用，不执行或重试任何产品动作。
func MaterializePreDriverFailure(packageRoot, resultsRoot, backupDirectory, campaignID string) (report CampaignReport, materializeErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationPreDriverReport")
	stage := "validate_campaign_identity"
	lane := ""
	defer func() {
		if materializeErr != nil {
			log.WithErr(materializeErr).WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "stage": stage}).Error("Windows pre-driver 失败报告生成失败")
		}
	}()
	log.WithFields(map[string]any{"campaign_id": campaignID, "package_root": packageRoot, "backup_directory": backupDirectory}).Info("开始生成 Windows pre-driver 失败报告")
	if !campaignIDPattern.MatchString(campaignID) {
		return CampaignReport{}, fmt.Errorf("invalid campaign id %q", campaignID)
	}
	stage = "load_prepared_backup"
	var prepared preparedBackupManifest
	if err := readJSONFile(filepath.Join(backupDirectory, "backup-manifest.json"), &prepared); err != nil {
		return CampaignReport{}, fmt.Errorf("read prepared backup manifest: %w", err)
	}
	if err := validatePreparedBackupManifest(prepared, campaignID); err != nil {
		return CampaignReport{}, err
	}
	lane = prepared.Lane
	stage = "load_package_source"
	source, err := LoadPackageSource(packageRoot)
	if err != nil {
		return CampaignReport{}, err
	}
	stage = "load_pre_driver_failure"
	var failure preDriverFailure
	failurePath := filepath.Join(backupDirectory, "run-failure.json")
	hasFailure := false
	if _, statErr := os.Stat(failurePath); statErr == nil {
		if err := readJSONFile(failurePath, &failure); err != nil {
			return CampaignReport{}, fmt.Errorf("read pre-driver failure: %w", err)
		}
		if failure.SchemaVersion != 2 || failure.Kind != "superdev.windows-validation.pre-driver-failure" || failure.CampaignID != campaignID || failure.Lane != prepared.Lane {
			return CampaignReport{}, fmt.Errorf("pre-driver failure identity does not match prepared backup")
		}
		if !failure.Execution.Attempted || failure.Execution.Succeeded || strings.TrimSpace(failure.Execution.StartedAtUTC) == "" || strings.TrimSpace(failure.Execution.FinishedAtUTC) == "" {
			return CampaignReport{}, fmt.Errorf("pre-driver failure must contain attempted failed execution facts")
		}
		hasFailure = true
	} else if !os.IsNotExist(statErr) {
		return CampaignReport{}, fmt.Errorf("inspect pre-driver failure: %w", statErr)
	}
	reason := "Run-Validation.ps1 did not leave a pre-driver execution fact"
	observedAt := prepared.CreatedAtUTC
	failureStage := ""
	prerequisiteResult := notRunResult(reason)
	targetResult := notRunResult(reason)
	attestationResult := notRunResult(reason)
	if hasFailure {
		reason = strings.TrimSpace(failure.Execution.Failure)
		if reason == "" {
			reason = strings.TrimSpace(failure.Error)
		}
		if reason == "" {
			reason = "validation driver preflight failed"
		}
		observedAt = failure.Execution.FinishedAtUTC
		failureStage = strings.TrimSpace(failure.Stage)
		if failureStage == "" {
			failureStage = "pre_driver_preflight"
		}
		prerequisiteResult = attemptedResult(false, reason, failure.Execution.StartedAtUTC, failure.Execution.FinishedAtUTC, []EvidenceRecord{{
			Name: "pre_driver_failure", Required: true, Present: true, Ref: filepath.ToSlash(failurePath),
		}})
		targetResult = blockedResult("pre_driver_preflight", reason)
		attestationResult = blockedResult("pre_driver_preflight", reason)
	}
	stage = "derive_results"
	installer, err := preDriverInstaller(prepared.Lane, failure.ArtifactVerification, failure.InstallerChecks, failurePath, reason)
	if err != nil {
		return CampaignReport{}, err
	}
	resultDirectory := filepath.Join(resultsRoot, campaignID)
	report = CampaignReport{
		SchemaVersion: CampaignReportSchemaVersion, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Functional: targetResult, FailureStage: failureStage, FailureReason: conditionalFailureReason(hasFailure, reason),
		BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
		Target: WindowsValidationTargetLabel, Lane: prepared.Lane, Installer: installer,
		RuntimeAttestation: RuntimeAttestation{Result: attestationResult},
		InstallerChecks:    append([]PackageFileIdentity{}, failure.InstallerChecks...),
		Prerequisites:      []StepExecution{{StepID: "pre_driver_preflight", Result: prerequisiteResult}},
		ValidationCatalog:  buildValidationCatalog(source.Scenarios, source.Coverage),
		Scenarios:          preDriverScenarios(source.Scenarios, hasFailure, reason),
		Providers:          preDriverProviders(source.Fixtures, hasFailure, reason),
		ToolRows:           ensureAllToolRows(source.Coverage, nil, targetResult),
		Cleanup:            pendingCleanupRecord("run Cleanup-Validation.ps1 even after a pre-driver failure", ""),
		KnownAnomalies:     source.Frozen.KnownBaselineExceptions,
		StartedAtUTC:       prepared.CreatedAtUTC, FinishedAtUTC: observedAt,
	}
	report, err = rederiveCampaignReport(report)
	if err != nil {
		return CampaignReport{}, err
	}
	redactor := NewRedactor()
	stage = "write_campaign_report"
	if err := writeCampaignReports(resultDirectory, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "write_validation_summary"
	if err := writeValidationSummary(resultsRoot, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "complete"
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": prepared.Lane, "phase_status": report.Result.PhaseStatus, "tool_rows": len(report.ToolRows), "failure_fact_present": hasFailure}).Info("Windows pre-driver 事实报告生成完成")
	return report, nil
}

func conditionalFailureReason(hasFailure bool, reason string) string {
	if hasFailure {
		return reason
	}
	return ""
}

func preDriverScenarios(scenarios []Scenario, hasFailure bool, reason string) []ScenarioExecution {
	if hasFailure {
		return blockedScenarioMatrix(scenarios, "pre_driver_preflight", reason)
	}
	return notRunScenarioMatrix(scenarios, reason)
}

func preDriverProviders(fixtures []FixtureManifest, hasFailure bool, reason string) []ProviderExecution {
	if hasFailure {
		return blockedProviderMatrix(fixtures, "pre_driver_preflight", reason)
	}
	return notRunProviderMatrix(fixtures, reason)
}

func preDriverInstaller(lane string, artifactFacts ExecutionFacts, checks []PackageFileIdentity, evidencePath, reason string) (InstallerExecution, error) {
	if !artifactFacts.Attempted && !artifactFacts.Succeeded && strings.TrimSpace(artifactFacts.BlockedBy) == "" && strings.TrimSpace(artifactFacts.NotRunReason) == "" {
		artifactFacts.NotRunReason = "installer artifact gate was not reached: " + reason
	}
	if artifactFacts.Succeeded && len(checks) != 1 {
		return InstallerExecution{}, fmt.Errorf("successful pre-driver artifact verification requires exactly one installer identity")
	}
	format := "nsis"
	if laneOrDefault(lane) == "msi_smoke" {
		format = "msi"
	}
	artifactEvidence := []EvidenceRecord{}
	if artifactFacts.Attempted {
		artifactEvidence = append(artifactEvidence, EvidenceRecord{
			Name: "pre_driver_installer_artifact", Required: true, Present: true, Ref: filepath.ToSlash(evidencePath),
		})
	}
	lifecycleReason := "pre-driver facts do not include installer lifecycle execution"
	notRun := ResultInput{Facts: ExecutionFacts{NotRunReason: lifecycleReason}}
	return DeriveInstallerExecution(InstallerExecutionFacts{
		Format: format, ArtifactVerified: artifactFacts.Attempted && artifactFacts.Succeeded, InstallerExecuted: false,
		Artifact: ResultInput{Facts: artifactFacts, Evidence: artifactEvidence},
		Install:  notRun, Start: notRun, Stop: notRun, Uninstall: notRun,
	})
}
