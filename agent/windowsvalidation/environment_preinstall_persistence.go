// environment_preinstall_persistence.go 将安装前环境门禁绑定到 prepared backup。
//
// 职责：
//   - 在 installer lifecycle 前验证包、runtime input、安装器文件和 pre-install environment admission
//   - 把 plan、manifest、decision 与 prepared baseline/campaign/lane 固化为可重派生记录
//   - 为 PowerShell lifecycle 入口提供不依赖 MCP 或产品 runtime 的窄验证函数
//
// 边界：
//   - 不安装、启动、停止或卸载产品，也不执行任何功能 scenario
//   - 不持久化 runtime input 路径、Host 地址、凭据值或治理声明内容，只保存安全摘要
//   - 持久化记录不能重新取得 collector 的进程内 provenance；验证只做结构重派生和冻结身份绑定
package windowsvalidation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	// PreparedEnvironmentPreinstallSchemaVersion 是 prepared backup 内安装前门禁记录版本。
	PreparedEnvironmentPreinstallSchemaVersion = "superdev.windows-environment-preinstall/v2"
	// PreparedEnvironmentPreinstallKind 是 lifecycle verifier 识别该记录的稳定类型。
	PreparedEnvironmentPreinstallKind = "windows_environment_preinstall"
	// PreparedEnvironmentPreinstallDirectory 是 prepared backup 内只保存安全环境证据的目录。
	PreparedEnvironmentPreinstallDirectory = "environment-preinstall"
	// PreparedEnvironmentPreinstallRecordFilename 是两阶段环境门禁的固定绑定记录文件名。
	PreparedEnvironmentPreinstallRecordFilename = "record.json"
	maxPreparedEnvironmentPreinstallBytes       = 8 * 1024 * 1024
)

// PreparedEnvironmentPreinstall 是 lifecycle 前可重放验证的安全绑定记录。
type PreparedEnvironmentPreinstall struct {
	SchemaVersion            string                       `json:"schema_version"`
	Kind                     string                       `json:"kind"`
	CampaignID               string                       `json:"campaign_id"`
	Lane                     string                       `json:"lane"`
	PreparedBaselineSHA256   string                       `json:"prepared_baseline_sha256"`
	BuildCommit              string                       `json:"build_commit"`
	ProductVersion           string                       `json:"product_version"`
	StableRuntimeInputSHA256 string                       `json:"stable_runtime_input_sha256"`
	StablePlanSHA256         string                       `json:"stable_plan_sha256"`
	PlanFileSHA256           string                       `json:"plan_file_sha256"`
	ManifestFileSHA256       string                       `json:"manifest_file_sha256"`
	ManifestDigest           string                       `json:"manifest_digest"`
	InstallerChecks          []PackageFileIdentity        `json:"installer_checks"`
	Request                  EnvironmentAdmissionRequest  `json:"request"`
	Decision                 EnvironmentAdmissionDecision `json:"decision"`
	PackageIntegrity         ValidationResult             `json:"package_integrity"`
	InputSafety              ValidationResult             `json:"input_safety"`
	InstallerArtifact        ValidationResult             `json:"installer_artifact"`
	Result                   ValidationResult             `json:"result"`
	CollectedAtUTC           string                       `json:"collected_at_utc"`
}

// PreparedEnvironmentPreinstallEvidence 是最终 campaign 报告内嵌的 A 阶段安全证据。
type PreparedEnvironmentPreinstallEvidence struct {
	Record   PreparedEnvironmentPreinstall `json:"record"`
	Plan     EnvironmentCollectionPlan     `json:"plan"`
	Manifest EnvironmentManifest           `json:"manifest"`
}

// PreparedEnvironmentPreinstallOptions 描述 Prepare 阶段的只读环境门禁输入。
type PreparedEnvironmentPreinstallOptions struct {
	PackageRoot      string
	RuntimeInputPath string
	PreparedBackup   string
	CampaignID       string
	Lane             string
	CommandRunner    EnvironmentCommandRunner
	FileReader       EnvironmentFileReader
	BrowserInventory EnvironmentBrowserInventoryReader
	Now              func() time.Time
}

// CollectPreparedEnvironmentPreinstall 执行并持久化 installer 前的只读环境门禁。
//
// 参数：
//   - ctx: 控制包外只读命令和文件采集的取消与超时
//   - options: 包、runtime input、prepared backup 身份及可替换的只读边界
//
// 返回：
//   - 已写入 prepared backup 的绑定记录；environment 非 PASS 时仍返回完整记录
//   - 输入、持久化或 pre-install admission 拒绝错误
//
// 注意：该函数成功是任何 installer lifecycle 动作的前置条件；strict installer lane 只接受
// PASS。core_only 不执行 installer，可在显式列出的能力缺口为 BLOCKED 时继续定向诊断。
func CollectPreparedEnvironmentPreinstall(ctx context.Context, options PreparedEnvironmentPreinstallOptions) (PreparedEnvironmentPreinstall, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationPreparedEnvironmentPreinstall")
	now := options.Now
	if now == nil {
		now = time.Now
	}
	observedAt := now().UTC().Format(time.RFC3339Nano)
	fields := map[string]any{"campaign_id": options.CampaignID, "lane": options.Lane}
	log.WithFields(fields).Info("开始 prepared Windows 安装前环境门禁")

	backupDirectory, prepared, err := loadPreparingBackupIdentity(options.PreparedBackup, options.CampaignID, options.Lane)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	if err := verifyPreparedBaselineIntegrity(backupDirectory, prepared, CleanupRecord{}); err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	preinstallDirectory := filepath.Join(backupDirectory, PreparedEnvironmentPreinstallDirectory)
	if _, err := os.Stat(preinstallDirectory); err == nil {
		return PreparedEnvironmentPreinstall{}, fmt.Errorf("prepared environment pre-install record already exists")
	} else if !os.IsNotExist(err) {
		return PreparedEnvironmentPreinstall{}, fmt.Errorf("inspect prepared environment pre-install directory: %w", err)
	}

	packageStarted := now().UTC().Format(time.RFC3339Nano)
	if err := VerifyPackageIntegrity(options.PackageRoot); err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	source, err := LoadPackageSource(options.PackageRoot)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	packageResult := attemptedResult(true, "", packageStarted, now().UTC().Format(time.RFC3339Nano), []EvidenceRecord{{
		Name: "portable_package_integrity", Required: true, Present: true, Ref: "manifest/package-files.json",
	}})

	inputStarted := now().UTC().Format(time.RFC3339Nano)
	input, stableRuntimeInputDigest, err := loadPreparedEnvironmentRuntimeInput(options.RuntimeInputPath, options.CampaignID, options.Lane)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	inputResult := attemptedResult(true, "", inputStarted, now().UTC().Format(time.RFC3339Nano), []EvidenceRecord{{
		Name: "runtime_input_safety", Required: true, Present: true, Ref: "inline:prepared-environment-preinstall#stable_runtime_input_sha256",
	}})

	var installerChecks []PackageFileIdentity
	installerResult := notRunResult("core_only excludes installer artifact")
	if laneOrDefault(options.Lane) != "core_only" {
		installerStarted := now().UTC().Format(time.RFC3339Nano)
		installerChecks, err = VerifyInstallerForLane(input.InstallerDirectory, options.Lane, source.Frozen.Installers)
		if err != nil {
			return PreparedEnvironmentPreinstall{}, err
		}
		installerResult = attemptedResult(true, "", installerStarted, now().UTC().Format(time.RFC3339Nano), []EvidenceRecord{{
			Name: "lane_installer_artifact", Required: true, Present: len(installerChecks) == 1, Ref: "inline:prepared-environment-preinstall#installer_checks",
		}})
	}

	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{
		FrozenBuild: source.Frozen, AgentDataDirectory: input.AgentDataDirectory,
		JVMAdapterCommand: input.JVMAdapterCommand, JVMAdapterSHA256: input.JVMAdapterSHA256,
		GoAdapterCommand: input.GoAdapterCommand, PythonAdapterCommand: input.PythonAdapterCommand,
		NodeAdapterCommand: input.NodeAdapterCommand, NativeAdapterCommand: input.NativeAdapterCommand,
		LinuxHostID:   input.LinuxHostID,
		ChromeVersion: input.ChromeVersion, ChromeSHA256: input.ChromeSHA256, ChromeSignerIdentity: input.ChromeSignerIdentity,
		EdgeVersion: input.EdgeVersion, EdgeSHA256: input.EdgeSHA256, EdgeSignerIdentity: input.EdgeSignerIdentity,
	})
	commandRunner := options.CommandRunner
	if commandRunner == nil {
		commandRunner = SystemEnvironmentCommandRunner{}
	}
	fileReader := options.FileReader
	if fileReader == nil {
		fileReader = SystemEnvironmentFileReader{}
	}
	browserInventory := options.BrowserInventory
	if browserInventory == nil {
		browserInventory = SystemEnvironmentBrowserInventory{}
	}
	manifest, err := CollectEnvironmentPreInstallManifest(ctx, EnvironmentPreInstallCollectorOptions{
		CampaignID: options.CampaignID, Plan: plan, PackageBuild: source.Frozen,
		CommandRunner: commandRunner, FileReader: fileReader, BrowserInventory: browserInventory, Now: now,
	})
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	request := EnvironmentAdmissionRequest{
		Mode: EnvironmentAdmissionPreInstall, CollectionStage: EnvironmentCollectionStagePreInstall,
		ExpectedPlanDigest: CanonicalEnvironmentPlanDigest(plan),
	}
	if laneOrDefault(options.Lane) == "core_only" {
		request.Mode = EnvironmentAdmissionDiagnostic
		request.AllowedBlockedKeys = append([]string{}, input.AllowedEnvironmentBlockers...)
	}
	decision, err := AdmitEnvironmentManifest(manifest, request)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	persistence, err := PersistEnvironmentManifest(preinstallDirectory, manifest, plan, NewRedactor())
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	planFileDigest, err := fileSHA256(persistence.PlanPath)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	manifestFileDigest, err := fileSHA256(persistence.JSONPath)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	children := []ValidationResult{packageResult, inputResult, decision.Result}
	if laneOrDefault(options.Lane) != "core_only" {
		children = []ValidationResult{packageResult, inputResult, installerResult, decision.Result}
	}
	overall, err := DeriveAggregateResult("prepared environment pre-install", len(children), children)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	record := PreparedEnvironmentPreinstall{
		SchemaVersion: PreparedEnvironmentPreinstallSchemaVersion, Kind: PreparedEnvironmentPreinstallKind,
		CampaignID: options.CampaignID, Lane: options.Lane, PreparedBaselineSHA256: prepared.BaselineSHA256,
		BuildCommit: source.Frozen.Build.GitCommit, ProductVersion: source.Frozen.Build.ProductVersion,
		StableRuntimeInputSHA256: stableRuntimeInputDigest,
		StablePlanSHA256:         CanonicalPreInstallEnvironmentPlanDigest(plan),
		PlanFileSHA256:           planFileDigest, ManifestFileSHA256: manifestFileDigest,
		ManifestDigest:  CanonicalEnvironmentManifestDigest(persistence.Manifest),
		InstallerChecks: append([]PackageFileIdentity{}, installerChecks...), Request: request, Decision: decision,
		PackageIntegrity: packageResult, InputSafety: inputResult, InstallerArtifact: installerResult,
		Result: overall, CollectedAtUTC: observedAt,
	}
	recordPath := filepath.Join(preinstallDirectory, PreparedEnvironmentPreinstallRecordFilename)
	if err := writePreparedEnvironmentPreinstallRecord(recordPath, record); err != nil {
		return record, err
	}
	if !decision.Admitted {
		log.WithFields(map[string]any{
			"campaign_id": options.CampaignID, "lane": options.Lane, "phase_status": decision.Result.PhaseStatus,
			"blocked_count": len(decision.BlockedKeys), "failed_count": len(decision.FailedKeys),
		}).Error("prepared Windows 安装前环境门禁拒绝 installer lifecycle")
		return record, fmt.Errorf("pre-install environment admission rejected campaign continuation")
	}
	log.WithFields(map[string]any{
		"campaign_id": options.CampaignID, "lane": options.Lane, "phase_status": record.Result.PhaseStatus,
		"manifest_digest": record.ManifestDigest,
	}).Info("prepared Windows 安装前环境门禁完成")
	return record, nil
}

// VerifyPreparedEnvironmentPreinstall 在任何 installer lifecycle 动作前重放 prepared 环境门禁。
//
// 参数：
//   - packageRoot: 当前不可变验证包根目录
//   - backupDirectory: Prepare-Validation.ps1 为本 lane 创建的 prepared backup
//   - campaignID: lifecycle 动作将要写入的 campaign 身份
//   - lane: lifecycle 动作将要执行的 installer lane
//
// 返回：
//   - strict lane 全部一致且 PASS，或 core_only 具名 BLOCKED diagnostic 已准入时为空
//   - 任一文件缺失、身份漂移、结构篡改或 admission 拒绝时的错误
//
// 注意：该函数只读且不启动 MCP；PowerShell 和 Go lifecycle 两个入口都应在第一次机器
// 变更之前调用它。
func VerifyPreparedEnvironmentPreinstall(packageRoot, backupDirectory, campaignID, lane string) error {
	log := logger.GetLogger().WithEntryName("WindowsValidationPreparedEnvironmentPreinstallVerifier")
	fields := map[string]any{"campaign_id": campaignID, "lane": lane}
	log.WithFields(fields).Info("开始验证 prepared Windows 安装前环境门禁")
	absoluteBackup, prepared, err := loadPreparedBackupIdentity(backupDirectory, campaignID, lane)
	if err != nil {
		return err
	}
	if err := verifyPreparedBaselineIntegrity(absoluteBackup, prepared, CleanupRecord{}); err != nil {
		return err
	}
	if err := VerifyPackageIntegrity(packageRoot); err != nil {
		return err
	}
	source, err := LoadPackageSource(packageRoot)
	if err != nil {
		return err
	}
	recordPath := filepath.Join(absoluteBackup, PreparedEnvironmentPreinstallDirectory, PreparedEnvironmentPreinstallRecordFilename)
	record, err := loadPreparedEnvironmentPreinstallRecord(recordPath)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(absoluteBackup, PreparedEnvironmentPreinstallDirectory, EnvironmentManifestJSONFilename)
	planPath := filepath.Join(absoluteBackup, PreparedEnvironmentPreinstallDirectory, EnvironmentPlanJSONFilename)
	manifest, err := LoadEnvironmentManifest(manifestPath)
	if err != nil {
		return err
	}
	var plan EnvironmentCollectionPlan
	if err := readJSONFile(planPath, &plan); err != nil {
		return err
	}
	if err := verifyPreparedEnvironmentPreinstallRecord(record, prepared, source.Frozen, manifest, plan, planPath, manifestPath, campaignID, lane); err != nil {
		log.WithFields(fields).WithField("cause_code", "preinstall_record_invalid").Error("prepared Windows 安装前环境门禁验证失败")
		return err
	}
	log.WithFields(fields).Info("prepared Windows 安装前环境门禁验证完成")
	return nil
}

func loadPreparingBackupIdentity(preparedBackup, campaignID, lane string) (string, preparedBackupManifest, error) {
	backupDirectory, err := filepath.Abs(preparedBackup)
	if err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("resolve preparing backup: %w", err)
	}
	var manifest preparedBackupManifest
	if err := readJSONFile(filepath.Join(backupDirectory, "backup-manifest.json"), &manifest); err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("read preparing backup manifest: %w", err)
	}
	if manifest.Status != "preparing" {
		return "", preparedBackupManifest{}, fmt.Errorf("environment pre-install collection requires preparing backup status")
	}
	// 其余身份字段与 production ready verifier 共用同一合同；这里只把状态投影成 ready
	// 复用校验，避免 Prepare 阶段在 user-state mutation 前提前声明 ready。
	readyIdentity := manifest
	readyIdentity.Status = "ready"
	if err := validatePreparedBackupIdentity(readyIdentity, campaignID); err != nil {
		return "", preparedBackupManifest{}, err
	}
	if manifest.Lane != lane {
		return "", preparedBackupManifest{}, fmt.Errorf("preparing backup lane %q does not match pre-install lane %q", manifest.Lane, lane)
	}
	if err := validatePreparedBaselineManifest(manifest); err != nil {
		return "", preparedBackupManifest{}, err
	}
	return backupDirectory, manifest, nil
}

func loadPreparedEnvironmentRuntimeInput(path, campaignID, lane string) (RuntimeInput, string, error) {
	input, err := loadRuntimeInput(path)
	if err != nil {
		return RuntimeInput{}, "", err
	}
	input.CampaignID = campaignID
	input.Lane = lane
	if err := validatePreInstallRuntimeInput(input); err != nil {
		return RuntimeInput{}, "", err
	}
	// A 只绑定路径解析与 campaign/lane 注入后的稳定语义。Host ID 与治理文件属于
	// fresh-profile bootstrap 后的 B，不读取、不摘要，也不阻断 installer 前门禁。
	return input, canonicalPreInstallRuntimeInputDigest(input), nil
}

func loadVerifiedPreparedEnvironmentPreinstallEvidence(packageRoot, backupDirectory, campaignID, lane string) (PreparedEnvironmentPreinstallEvidence, error) {
	if err := VerifyPreparedEnvironmentPreinstall(packageRoot, backupDirectory, campaignID, lane); err != nil {
		return PreparedEnvironmentPreinstallEvidence{}, err
	}
	absoluteBackup, err := filepath.Abs(backupDirectory)
	if err != nil {
		return PreparedEnvironmentPreinstallEvidence{}, err
	}
	directory := filepath.Join(absoluteBackup, PreparedEnvironmentPreinstallDirectory)
	record, err := loadPreparedEnvironmentPreinstallRecord(filepath.Join(directory, PreparedEnvironmentPreinstallRecordFilename))
	if err != nil {
		return PreparedEnvironmentPreinstallEvidence{}, err
	}
	manifest, err := LoadEnvironmentManifest(filepath.Join(directory, EnvironmentManifestJSONFilename))
	if err != nil {
		return PreparedEnvironmentPreinstallEvidence{}, err
	}
	var plan EnvironmentCollectionPlan
	if err := readJSONFile(filepath.Join(directory, EnvironmentPlanJSONFilename), &plan); err != nil {
		return PreparedEnvironmentPreinstallEvidence{}, err
	}
	return PreparedEnvironmentPreinstallEvidence{Record: record, Plan: plan, Manifest: manifest}, nil
}

func verifyPreparedEnvironmentRuntimeInput(evidence PreparedEnvironmentPreinstallEvidence, input RuntimeInput) error {
	if err := validateRuntimeInput(input); err != nil {
		return err
	}
	if evidence.Record.StableRuntimeInputSHA256 != canonicalPreInstallRuntimeInputDigest(input) {
		return fmt.Errorf("current runtime input stable fields differ from prepared environment pre-install input")
	}
	if evidence.Record.CampaignID != input.CampaignID || evidence.Record.Lane != laneOrDefault(input.Lane) {
		return fmt.Errorf("current runtime input campaign or lane differs from prepared environment pre-install")
	}
	return nil
}

func canonicalPreInstallRuntimeInputDigest(input RuntimeInput) string {
	allowedBlockers := append([]string{}, input.AllowedEnvironmentBlockers...)
	sort.Strings(allowedBlockers)
	installerDirectory := input.InstallerDirectory
	if laneOrDefault(input.Lane) == "core_only" {
		// core_only 从不读取、校验或执行 installer；无关路径不能进入 A→B 稳定合同。
		installerDirectory = ""
	}
	binding := struct {
		SchemaVersion              int      `json:"schema_version"`
		Kind                       string   `json:"kind"`
		MCPPath                    string   `json:"mcp_path"`
		InstallerDirectory         string   `json:"installer_directory"`
		CampaignRoot               string   `json:"campaign_root"`
		ResultsRoot                string   `json:"results_root"`
		LinuxRoot                  string   `json:"linux_root,omitempty"`
		AgentDataDirectory         string   `json:"agent_data_directory,omitempty"`
		JVMAdapterCommand          string   `json:"jvm_adapter_command,omitempty"`
		JVMAdapterSHA256           string   `json:"jvm_adapter_sha256,omitempty"`
		GoAdapterCommand           string   `json:"go_adapter_command,omitempty"`
		PythonAdapterCommand       string   `json:"python_adapter_command,omitempty"`
		NodeAdapterCommand         string   `json:"node_adapter_command,omitempty"`
		NativeAdapterCommand       string   `json:"native_adapter_command,omitempty"`
		ChromeVersion              string   `json:"chrome_version,omitempty"`
		ChromeSHA256               string   `json:"chrome_sha256,omitempty"`
		ChromeSignerIdentity       string   `json:"chrome_signer_identity,omitempty"`
		EdgeVersion                string   `json:"edge_version,omitempty"`
		EdgeSHA256                 string   `json:"edge_sha256,omitempty"`
		EdgeSignerIdentity         string   `json:"edge_signer_identity,omitempty"`
		AllowedEnvironmentBlockers []string `json:"allowed_environment_blockers,omitempty"`
		ApprovalWaitSeconds        int      `json:"approval_wait_seconds"`
		Lane                       string   `json:"lane"`
		CampaignID                 string   `json:"campaign_id"`
	}{
		SchemaVersion: input.SchemaVersion, Kind: input.Kind,
		MCPPath: input.MCPPath, InstallerDirectory: installerDirectory,
		CampaignRoot: input.CampaignRoot, ResultsRoot: input.ResultsRoot,
		LinuxRoot: input.LinuxRoot, AgentDataDirectory: input.AgentDataDirectory,
		JVMAdapterCommand: input.JVMAdapterCommand, JVMAdapterSHA256: input.JVMAdapterSHA256,
		GoAdapterCommand: input.GoAdapterCommand, PythonAdapterCommand: input.PythonAdapterCommand,
		NodeAdapterCommand: input.NodeAdapterCommand, NativeAdapterCommand: input.NativeAdapterCommand,
		ChromeVersion: input.ChromeVersion, ChromeSHA256: input.ChromeSHA256, ChromeSignerIdentity: input.ChromeSignerIdentity,
		EdgeVersion: input.EdgeVersion, EdgeSHA256: input.EdgeSHA256, EdgeSignerIdentity: input.EdgeSignerIdentity,
		AllowedEnvironmentBlockers: allowedBlockers, ApprovalWaitSeconds: input.ApprovalWaitSeconds,
		Lane: laneOrDefault(input.Lane), CampaignID: input.CampaignID,
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(CanonicalJSON(binding))))
}

func verifyPreparedEnvironmentPreinstallRecord(
	record PreparedEnvironmentPreinstall,
	prepared preparedBackupManifest,
	frozen FrozenBuild,
	manifest EnvironmentManifest,
	plan EnvironmentCollectionPlan,
	planPath string,
	manifestPath string,
	campaignID string,
	lane string,
) error {
	if record.SchemaVersion != PreparedEnvironmentPreinstallSchemaVersion || record.Kind != PreparedEnvironmentPreinstallKind {
		return fmt.Errorf("prepared environment pre-install record identity is invalid")
	}
	if record.CampaignID != campaignID || record.Lane != lane || record.PreparedBaselineSHA256 != prepared.BaselineSHA256 {
		return fmt.Errorf("prepared environment pre-install record campaign, lane, or baseline binding differs")
	}
	if record.BuildCommit != frozen.Build.GitCommit || record.ProductVersion != frozen.Build.ProductVersion {
		return fmt.Errorf("prepared environment pre-install record build identity differs from package")
	}
	for name, value := range map[string]string{
		"stable_runtime_input_sha256": record.StableRuntimeInputSHA256, "stable_plan_sha256": record.StablePlanSHA256,
		"plan_file_sha256":     record.PlanFileSHA256,
		"manifest_file_sha256": record.ManifestFileSHA256, "manifest_digest": record.ManifestDigest,
	} {
		if !validEnvironmentSHA256(value) {
			return fmt.Errorf("prepared environment pre-install %s is invalid", name)
		}
	}
	actualPlanFileDigest, err := fileSHA256(planPath)
	if err != nil {
		return err
	}
	actualManifestFileDigest, err := fileSHA256(manifestPath)
	if err != nil {
		return err
	}
	if actualPlanFileDigest != record.PlanFileSHA256 || actualManifestFileDigest != record.ManifestFileSHA256 {
		return fmt.Errorf("prepared environment pre-install plan or manifest file digest differs")
	}
	if CanonicalPreInstallEnvironmentPlanDigest(plan) != record.StablePlanSHA256 {
		return fmt.Errorf("prepared environment pre-install stable plan digest differs")
	}
	if CanonicalEnvironmentManifestDigest(manifest) != record.ManifestDigest {
		return fmt.Errorf("prepared environment pre-install canonical manifest digest differs")
	}
	if manifest.CampaignID != campaignID || manifest.CollectionStage != EnvironmentCollectionStagePreInstall {
		return fmt.Errorf("prepared environment pre-install manifest identity or collection stage differs")
	}
	if err := validatePreInstallEnvironmentCatalog(manifest); err != nil {
		return err
	}
	if err := VerifyEnvironmentManifestPlanBinding(manifest, plan); err != nil {
		return err
	}
	expectedMode := EnvironmentAdmissionPreInstall
	if laneOrDefault(lane) == "core_only" {
		expectedMode = EnvironmentAdmissionDiagnostic
	}
	if record.Request.Mode != expectedMode || record.Request.CollectionStage != EnvironmentCollectionStagePreInstall {
		return fmt.Errorf("prepared environment pre-install admission request is invalid")
	}
	decision, err := deriveEnvironmentAdmission(manifest, record.Request)
	if err != nil {
		return err
	}
	if CanonicalJSON(decision) != CanonicalJSON(record.Decision) {
		return fmt.Errorf("prepared environment pre-install decision differs from rederived facts")
	}
	if !decision.Admitted {
		return fmt.Errorf("prepared environment pre-install admission is rejected")
	}
	if laneOrDefault(lane) != "core_only" && decision.Result.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("prepared environment pre-install admission is not PASS")
	}
	if err := verifyPreparedInstallerChecks(record.InstallerChecks, lane, frozen.Installers); err != nil {
		return err
	}
	prerequisites := []ValidationResult{record.PackageIntegrity, record.InputSafety}
	children := []ValidationResult{record.PackageIntegrity, record.InputSafety, decision.Result}
	if laneOrDefault(lane) == "core_only" {
		derivedInstaller, deriveErr := DeriveValidationResult(resultInput(record.InstallerArtifact))
		if deriveErr != nil || derivedInstaller.PhaseStatus != PhaseStatusNotRun || record.InstallerArtifact.PhaseStatus != PhaseStatusNotRun {
			return fmt.Errorf("core_only prepared environment installer artifact is not a rederived NOT_RUN")
		}
	} else {
		prerequisites = append(prerequisites, record.InstallerArtifact)
		children = []ValidationResult{record.PackageIntegrity, record.InputSafety, record.InstallerArtifact, decision.Result}
	}
	for _, child := range prerequisites {
		derived, err := DeriveValidationResult(resultInput(child))
		if err != nil || derived.PhaseStatus != PhaseStatusPass || child.PhaseStatus != PhaseStatusPass {
			return fmt.Errorf("prepared environment pre-install prerequisite is not a rederived PASS")
		}
	}
	overall, err := DeriveAggregateResult("prepared environment pre-install", len(children), children)
	if err != nil {
		return err
	}
	if laneOrDefault(lane) != "core_only" && overall.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("prepared environment pre-install overall result is not PASS")
	}
	if CanonicalJSON(overall) != CanonicalJSON(record.Result) {
		return fmt.Errorf("prepared environment pre-install overall result differs from rederived facts")
	}
	return nil
}

func verifyPreparedInstallerChecks(checks []PackageFileIdentity, lane string, frozen []InstallerIdentity) error {
	if laneOrDefault(lane) == "core_only" {
		if len(checks) != 0 {
			return fmt.Errorf("core_only prepared environment pre-install cannot contain installer checks")
		}
		return nil
	}
	if len(checks) != 1 {
		return fmt.Errorf("prepared environment pre-install requires exactly one installer check")
	}
	format := "nsis"
	if laneOrDefault(lane) == "msi_smoke" {
		format = "msi"
	}
	for _, expected := range frozen {
		if expected.Format != format {
			continue
		}
		if checks[0].Path != expected.Filename || checks[0].SizeBytes != expected.SizeBytes || !strings.EqualFold(checks[0].SHA256, expected.SHA256) {
			return fmt.Errorf("prepared environment pre-install installer identity differs from frozen %s artifact", format)
		}
		return nil
	}
	return fmt.Errorf("frozen package does not contain exactly one %s installer", format)
}

func loadPreparedEnvironmentPreinstallRecord(path string) (PreparedEnvironmentPreinstall, error) {
	raw, err := readBoundedPreparedEnvironmentFile(path, maxPreparedEnvironmentPreinstallBytes)
	if err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var record PreparedEnvironmentPreinstall
	if err := decoder.Decode(&record); err != nil {
		return PreparedEnvironmentPreinstall{}, fmt.Errorf("decode prepared environment pre-install record: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return PreparedEnvironmentPreinstall{}, err
	}
	return record, nil
}

func writePreparedEnvironmentPreinstallRecord(path string, record PreparedEnvironmentPreinstall) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("prepared environment pre-install record already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".environment-preinstall-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func readBoundedPreparedEnvironmentFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("prepared environment input exceeds %d bytes", limit)
	}
	return raw, nil
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
