// installer_lifecycle.go owns the trusted installer lifecycle fact boundary.
//
// 职责：
//   - 把公开输入收窄为 install/start/stop/uninstall 固定动作请求
//   - 绑定 prepared campaign、冻结 installer、规范化安装根与四个普通 JSON 动作事实
//   - 逐文件校验动作身份、真实结果和 required observed state 后调用统一结果派生
//
// 边界：
//   - 不接受调用方提交 attempted/succeeded/observation 或 evidence-present 布尔值
//   - 不从 artifact、runtime attestation、cleanup 或聊天陈述反推动作发生
//   - 不实现摘要链或动作恢复平台；缺失/损坏事实直接 fail closed
//   - Windows/UAC/进程观察由 package-integrity 校验后的固定内部 executor 承担
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
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const (
	// InstallerLifecycleActionFactKind identifies one ordinary validated action fact file.
	InstallerLifecycleActionFactKind      = "superdev.windows-validation.installer-lifecycle-action-fact"
	installerLifecycleExecutorRequestKind = "superdev.windows-validation.installer-lifecycle-executor-request"
	installerLifecycleExecutorResultKind  = "superdev.windows-validation.installer-lifecycle-executor-result"
	installerLifecycleLockFilename        = ".installer-lifecycle.lock"
	installerLifecycleActiveLockFilename  = ".installer-lifecycle.active.lock"
)

// InstallerLifecycleAction is one fixed installer-lane operation.
type InstallerLifecycleAction string

const (
	// LifecycleActionInstall executes the frozen installer.
	LifecycleActionInstall InstallerLifecycleAction = "install"
	// LifecycleActionStart starts the installed Desktop as the standard user.
	LifecycleActionStart InstallerLifecycleAction = "start"
	// LifecycleActionStop closes only the Desktop process recorded by start.
	LifecycleActionStop InstallerLifecycleAction = "stop"
	// LifecycleActionUninstall executes the frozen MSI or installed official NSIS uninstaller.
	LifecycleActionUninstall InstallerLifecycleAction = "uninstall"
)

var installerLifecycleActionOrder = []InstallerLifecycleAction{
	LifecycleActionInstall, LifecycleActionStart, LifecycleActionStop, LifecycleActionUninstall,
}

var installerLifecycleMSIProductCodePattern = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

var installerLifecycleRequiredChecks = map[InstallerLifecycleAction][]string{
	LifecycleActionInstall:   {"installer_exit_success", "installed_product_present", "installed_sidecars_present", "uninstall_identity_present"},
	LifecycleActionStart:     {"desktop_process_started", "agent_listener_owned", "installed_process_identities_match"},
	LifecycleActionStop:      {"desktop_process_stopped", "agent_listener_stopped", "installed_processes_stopped"},
	LifecycleActionUninstall: {"uninstaller_exit_success", "uninstall_identity_absent", "installed_files_absent"},
}

// InstallerLifecycleBinding binds every action to one prepared campaign and installed root.
type InstallerLifecycleBinding struct {
	CampaignID             string              `json:"campaign_id"`
	Lane                   string              `json:"lane"`
	Format                 string              `json:"format"`
	UninstallerFilename    string              `json:"uninstaller_filename,omitempty"`
	BuildCommit            string              `json:"build_commit"`
	ProductVersion         string              `json:"product_version"`
	PreparedBackupSHA256   string              `json:"prepared_backup_sha256"`
	PreparedBaselineSHA256 string              `json:"prepared_baseline_sha256"`
	InstallDirectory       string              `json:"install_directory"`
	InstallDirectorySHA256 string              `json:"install_directory_sha256"`
	Artifact               PackageFileIdentity `json:"artifact"`
}

// InstallerLifecycleCommandFact records the fixed operation selected by the trusted executor.
type InstallerLifecycleCommandFact struct {
	Operation   InstallerLifecycleAction `json:"operation"`
	Method      string                   `json:"method"`
	Executable  string                   `json:"executable"`
	Target      PackageFileIdentity      `json:"target"`
	ProductCode string                   `json:"product_code,omitempty"`
	ProcessIDs  []int                    `json:"process_ids,omitempty"`
	ExitCode    *int                     `json:"exit_code,omitempty"`
}

// InstallerLifecycleStateCheck is a fixed post-action assertion name and observed outcome.
type InstallerLifecycleStateCheck struct {
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
}

// InstallerLifecycleProcessIdentity binds a process to an executable under the installed root.
type InstallerLifecycleProcessIdentity struct {
	Role            string              `json:"role"`
	ProcessID       int                 `json:"process_id"`
	ParentProcessID int                 `json:"parent_process_id,omitempty"`
	Executable      PackageFileIdentity `json:"executable"`
}

// InstallerLifecyclePortIdentity records the process that owns the Agent listener.
type InstallerLifecyclePortIdentity struct {
	Port            int  `json:"port"`
	Listening       bool `json:"listening"`
	OwningProcessID int  `json:"owning_process_id,omitempty"`
}

// InstallerLifecycleUninstallIdentity records the exact product uninstall registration.
type InstallerLifecycleUninstallIdentity struct {
	Scope                 string `json:"scope"`
	Key                   string `json:"key"`
	DisplayName           string `json:"display_name"`
	DisplayVersion        string `json:"display_version"`
	InstallLocation       string `json:"install_location"`
	UninstallExecutable   string `json:"uninstall_executable"`
	UninstallStringSHA256 string `json:"uninstall_string_sha256"`
}

// InstallerLifecycleObservation contains concrete OS facts captured after one fixed action.
type InstallerLifecycleObservation struct {
	Checks                   []InstallerLifecycleStateCheck        `json:"checks"`
	InstallPathPresent       *bool                                 `json:"install_path_present,omitempty"`
	ProductFiles             []PackageFileIdentity                 `json:"product_files,omitempty"`
	SidecarFiles             []PackageFileIdentity                 `json:"sidecar_files,omitempty"`
	UninstallerFile          *PackageFileIdentity                  `json:"uninstaller_file,omitempty"`
	UninstallEntries         []InstallerLifecycleUninstallIdentity `json:"uninstall_entries,omitempty"`
	Processes                []InstallerLifecycleProcessIdentity   `json:"processes,omitempty"`
	Port57017                *InstallerLifecyclePortIdentity       `json:"port_57017,omitempty"`
	RemainingBoundProcessIDs []int                                 `json:"remaining_bound_process_ids,omitempty"`
}

// InstallerLifecycleActionFact 是一个固定动作的普通 JSON 事实。
//
// 文件身份、campaign/lane/prepared backup、执行结果和 observed state 必须同时有效；
// 四个文件彼此独立，不携带前序摘要或恢复状态。
type InstallerLifecycleActionFact struct {
	SchemaVersion  int                           `json:"schema_version"`
	Kind           string                        `json:"kind"`
	Action         InstallerLifecycleAction      `json:"action"`
	Binding        InstallerLifecycleBinding     `json:"binding"`
	ExecutionFacts ExecutionFacts                `json:"execution_facts"`
	Command        InstallerLifecycleCommandFact `json:"command"`
	Observation    InstallerLifecycleObservation `json:"observation"`
}

// InstallerLifecycleActionEvidence 保留现有调用方名称；其值就是经校验的普通动作事实。
type InstallerLifecycleActionEvidence = InstallerLifecycleActionFact

// InstallerLifecycleExecuteOptions describes one public fixed-action request.
//
// Callers intentionally cannot provide execution facts, observations, evidence flags, or a result file.
type InstallerLifecycleExecuteOptions struct {
	PackageRoot      string
	PreparedBackup   string
	InstallerPath    string
	InstallDirectory string
	ResultsRoot      string
	Action           InstallerLifecycleAction
}

type installerLifecycleExecutorRequest struct {
	SchemaVersion           int                                  `json:"schema_version"`
	Kind                    string                               `json:"kind"`
	Action                  InstallerLifecycleAction             `json:"action"`
	Binding                 InstallerLifecycleBinding            `json:"binding"`
	PreparedBackupDirectory string                               `json:"prepared_backup_directory"`
	ActiveLockPath          string                               `json:"active_lock_path"`
	Format                  string                               `json:"format"`
	InstallerPath           string                               `json:"installer_path"`
	InstallDirectory        string                               `json:"install_directory"`
	DesktopPath             string                               `json:"desktop_path,omitempty"`
	UninstallerPath         string                               `json:"uninstaller_path,omitempty"`
	ProductVersion          string                               `json:"product_version"`
	Artifact                PackageFileIdentity                  `json:"artifact"`
	InstalledFiles          []PackageFileIdentity                `json:"installed_files,omitempty"`
	UninstallerIdentity     PackageFileIdentity                  `json:"uninstaller_identity,omitempty"`
	UninstallEntry          *InstallerLifecycleUninstallIdentity `json:"uninstall_entry,omitempty"`
	StartProcessIDs         []int                                `json:"start_process_ids,omitempty"`
}

type installerLifecycleExecutorResult struct {
	SchemaVersion int                           `json:"schema_version"`
	Kind          string                        `json:"kind"`
	Action        InstallerLifecycleAction      `json:"action"`
	Attempted     bool                          `json:"attempted"`
	Succeeded     bool                          `json:"succeeded"`
	StartedAtUTC  string                        `json:"started_at_utc"`
	FinishedAtUTC string                        `json:"finished_at_utc"`
	FailureCode   string                        `json:"failure_code,omitempty"`
	Command       InstallerLifecycleCommandFact `json:"command"`
	Observation   InstallerLifecycleObservation `json:"observation"`
}

type installerLifecycleExecutionDependencies struct {
	verifyPackage    func(string) error
	verifyPreinstall func(string, string, string, string) error
	platformGate     func() error
	isElevated       func() (bool, error)
	executeHelper    func(context.Context, string, string, string) error
	writeFact        func(string, any) error
}

type verifiedInstallerLifecycle struct {
	Evidence  []InstallerLifecycleActionEvidence
	Execution InstallerExecution
}

// ExecuteInstallerLifecycleAction 校验并执行一个固定安装器生命周期动作。
//
// 参数：
//   - ctx: 控制内部 helper 的执行期限。
//   - options: 只包含 package、prepared backup、冻结 installer、安装根和固定动作选择。
//
// 返回：
//   - 从四个普通动作事实重新派生的 InstallerExecution。
//   - package、binding、动作事实或真实 Windows 执行不满足合同时的错误。
//
// 注意：production 路径拒绝 elevated parent。helper 只写临时 result；Go 校验后
// 才把当前动作发布为普通 JSON fact，缺失结果不会触发重放或恢复协议。
func ExecuteInstallerLifecycleAction(ctx context.Context, options InstallerLifecycleExecuteOptions) (InstallerExecution, error) {
	dependencies := installerLifecycleExecutionDependencies{
		verifyPackage:    VerifyPackageIntegrity,
		verifyPreinstall: VerifyPreparedEnvironmentPreinstall,
		platformGate:     validateInstallerLifecyclePlatform,
		isElevated:       installerLifecycleProcessElevated,
		executeHelper:    executeInstallerLifecycleHelper,
		writeFact:        writeInstallerLifecycleJSON,
	}
	return executeInstallerLifecycleAction(ctx, options, dependencies)
}

func executeInstallerLifecycleAction(ctx context.Context, options InstallerLifecycleExecuteOptions, dependencies installerLifecycleExecutionDependencies) (execution InstallerExecution, executionErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationInstallerLifecycle")
	stage := "validate_request"
	campaignID, lane, formatName, loggedAction := "", "", "", ""
	defer func() {
		fields := map[string]any{"campaign_id": safeCampaignID(campaignID), "lane": safeInstallerLifecycleLane(lane), "format": formatName, "action": loggedAction, "stage": stage}
		if executionErr != nil {
			log.WithFields(fields).Error("Windows installer lifecycle 固定动作失败")
			return
		}
		log.WithFields(fields).Info("Windows installer lifecycle 固定动作完成")
	}()
	if !isInstallerLifecycleAction(options.Action) {
		return InstallerExecution{}, fmt.Errorf("unsupported installer lifecycle action")
	}
	loggedAction = string(options.Action)
	if dependencies.verifyPackage == nil || dependencies.verifyPreinstall == nil || dependencies.platformGate == nil || dependencies.isElevated == nil ||
		dependencies.executeHelper == nil || dependencies.writeFact == nil {
		return InstallerExecution{}, fmt.Errorf("installer lifecycle production dependencies are incomplete")
	}

	stage = "verify_package_integrity"
	if err := dependencies.verifyPackage(options.PackageRoot); err != nil {
		return InstallerExecution{}, fmt.Errorf("verify validation package: %w", err)
	}
	stage = "windows_10_client_x64_gate"
	if err := dependencies.platformGate(); err != nil {
		return InstallerExecution{}, err
	}
	stage = "standard_user_gate"
	elevated, err := dependencies.isElevated()
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("inspect validation driver token: %w", err)
	}
	if elevated {
		return InstallerExecution{}, fmt.Errorf("installer lifecycle driver must run as a standard non-elevated user")
	}

	stage = "load_package_source"
	source, err := LoadPackageSource(options.PackageRoot)
	if err != nil {
		return InstallerExecution{}, err
	}
	stage = "verify_prepared_backup"
	backupDirectory, prepared, err := loadPreparedBackupForLifecycle(options.PreparedBackup)
	if err != nil {
		return InstallerExecution{}, err
	}
	campaignID, lane, formatName = prepared.CampaignID, prepared.Lane, installerFormatForLane(prepared.Lane)
	if err := verifyPreparedBaselineIntegrity(backupDirectory, prepared, CleanupRecord{}); err != nil {
		return InstallerExecution{}, fmt.Errorf("verify prepared baseline before lifecycle action: %w", err)
	}
	if err := validateCleanInstallerBaseline(filepath.Join(backupDirectory, "baseline.json")); err != nil {
		return InstallerExecution{}, err
	}
	stage = "verify_frozen_installer"
	checks, err := VerifyInstallerForLane(filepath.Dir(options.InstallerPath), prepared.Lane, source.Frozen.Installers)
	if err != nil {
		return InstallerExecution{}, err
	}
	if len(checks) != 1 || !strings.EqualFold(filepath.Base(options.InstallerPath), checks[0].Path) {
		return InstallerExecution{}, fmt.Errorf("selected installer does not identify the frozen lane artifact")
	}
	binding, err := installerLifecycleBinding(source.Frozen, prepared, checks, options.InstallDirectory)
	if err != nil {
		return InstallerExecution{}, err
	}
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "format": formatName, "action": loggedAction}).Info("Windows installer lifecycle 请求身份已绑定")
	stage = "verify_preinstall_environment"
	if err := dependencies.verifyPreinstall(options.PackageRoot, backupDirectory, campaignID, lane); err != nil {
		return InstallerExecution{}, fmt.Errorf("verify prepared pre-install environment before lifecycle action: %w", err)
	}
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "format": formatName, "action": loggedAction}).Info("Windows installer lifecycle 安装前环境门禁已通过")
	activeLockPath := filepath.Join(backupDirectory, installerLifecycleActiveLockFilename)
	stage = "probe_active_helper_before_admission"
	if err := probeInstallerLifecycleActiveLock(activeLockPath); err != nil {
		return InstallerExecution{}, err
	}
	stage = "acquire_action_lock"
	// admission 锁封闭正常 driver 间的 helper 交接窗口；helper 自己持有另一把活动锁，
	// 因而 driver 退出后 elevated 子进程、观察和 result 写入仍处于同一排他周期。
	// 两把锁都在 fact 目录之外且不承载 attempt、恢复或 verdict 状态。
	releaseActionLock, err := acquireInstallerLifecycleLock(filepath.Join(backupDirectory, installerLifecycleLockFilename))
	if err != nil {
		return InstallerExecution{}, err
	}
	defer releaseActionLock()
	stage = "probe_active_helper_after_admission"
	if err := probeInstallerLifecycleActiveLock(activeLockPath); err != nil {
		return InstallerExecution{}, err
	}
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "format": formatName, "action": loggedAction}).Info("Windows installer lifecycle helper 活动门禁已通过")

	stage = "load_action_facts"
	directory := filepath.Join(backupDirectory, "installer-lifecycle")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return InstallerExecution{}, fmt.Errorf("create installer lifecycle fact directory: %w", err)
	}
	artifactInput := successfulArtifactInputForVerifiedInstaller()
	verified, err := loadVerifiedInstallerLifecycleFacts(backupDirectory, binding, artifactInput)
	if err != nil {
		return InstallerExecution{}, err
	}
	evidence := verified.Evidence
	execution = verified.Execution
	if len(evidence) >= len(installerLifecycleActionOrder) || options.Action != installerLifecycleActionOrder[len(evidence)] {
		return InstallerExecution{}, fmt.Errorf("selected action is not the next fixed installer lifecycle action")
	}

	stage = "prepare_fixed_action"
	actionFact, blocked := blockedInstallerLifecycleFact(options.Action, binding, evidence)
	if !blocked {
		request, requestErr := buildInstallerLifecycleExecutorRequest(options, binding, evidence, backupDirectory)
		if requestErr != nil {
			return InstallerExecution{}, requestErr
		}
		stage = "reverify_fixed_helper"
		if err := dependencies.verifyPackage(options.PackageRoot); err != nil {
			return InstallerExecution{}, fmt.Errorf("reverify validation package before helper launch: %w", err)
		}
		temporaryDirectory, err := os.MkdirTemp("", "superdev-installer-lifecycle-")
		if err != nil {
			return InstallerExecution{}, fmt.Errorf("create lifecycle helper workspace: %w", err)
		}
		defer os.RemoveAll(temporaryDirectory)
		requestPath := filepath.Join(temporaryDirectory, "request.json")
		resultPath := filepath.Join(temporaryDirectory, "result.json")
		if err := writeInstallerLifecycleJSON(requestPath, request); err != nil {
			return InstallerExecution{}, fmt.Errorf("write lifecycle helper request: %w", err)
		}
		helperPath := filepath.Join(options.PackageRoot, "internal", "Invoke-InstallerLifecycleAction.ps1")
		stage = "execute_fixed_action"
		log.WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "format": formatName, "action": loggedAction}).Info("Windows installer lifecycle 固定动作开始")
		helperErr := dependencies.executeHelper(ctx, helperPath, requestPath, resultPath)
		var result installerLifecycleExecutorResult
		if err := readStrictInstallerLifecycleJSON(resultPath, &result); err != nil {
			if helperErr != nil {
				return InstallerExecution{}, fmt.Errorf("fixed lifecycle helper failed without a valid result")
			}
			return InstallerExecution{}, fmt.Errorf("read fixed lifecycle helper result: %w", err)
		}
		if result.SchemaVersion != 1 || result.Kind != installerLifecycleExecutorResultKind || result.Action != options.Action {
			return InstallerExecution{}, fmt.Errorf("fixed lifecycle helper result identity is invalid")
		}
		facts, err := executionFactsFromFixedExecutor(result)
		if err != nil {
			return InstallerExecution{}, err
		}
		actionFact = InstallerLifecycleActionEvidence{
			SchemaVersion: 1, Kind: InstallerLifecycleActionFactKind, Action: options.Action, Binding: binding,
			ExecutionFacts: facts, Command: result.Command, Observation: result.Observation,
		}
		if err := validateInstallerLifecycleActionEvidence(actionFact, installFact(evidence), startFact(evidence)); err != nil {
			return InstallerExecution{}, fmt.Errorf("validate fixed lifecycle helper result: %w", err)
		}
	}
	candidate := append(append([]InstallerLifecycleActionEvidence{}, evidence...), actionFact)
	if _, err := deriveInstallerLifecycleEvidence(candidate, binding, artifactInput); err != nil {
		return InstallerExecution{}, err
	}
	stage = "persist_action_fact"
	factPath := filepath.Join(directory, installerLifecycleFactFilename(options.Action))
	if err := dependencies.writeFact(factPath, actionFact); err != nil {
		return InstallerExecution{}, fmt.Errorf("persist installer lifecycle action fact: %w", err)
	}
	log.WithFields(map[string]any{
		"campaign_id": campaignID, "lane": lane, "format": formatName, "action": loggedAction,
		"fact_file": filepath.Base(factPath), "attempted": actionFact.ExecutionFacts.Attempted,
	}).Info("Windows installer lifecycle 动作事实已校验并写入")

	verified, err = loadVerifiedInstallerLifecycleFacts(backupDirectory, binding, artifactInput)
	if err != nil {
		return InstallerExecution{}, err
	}
	execution = verified.Execution
	if strings.TrimSpace(options.ResultsRoot) != "" {
		campaignReport := filepath.Join(options.ResultsRoot, campaignID, "campaign-report.json")
		if _, statErr := os.Stat(campaignReport); statErr == nil {
			stage = "finalize_campaign_report"
			if _, err := FinalizeCampaignInstallerLifecycle(options.PackageRoot, options.ResultsRoot, campaignID, backupDirectory); err != nil {
				return InstallerExecution{}, err
			}
		} else if !os.IsNotExist(statErr) {
			return InstallerExecution{}, fmt.Errorf("inspect campaign report before lifecycle finalization: %w", statErr)
		}
	}
	stage = "complete"
	current := lifecycleResultForAction(execution, options.Action)
	if current.PhaseStatus != PhaseStatusPass {
		return execution, fmt.Errorf("fixed installer lifecycle action completed with status %s", current.PhaseStatus)
	}
	return execution, nil
}

// FinalizeCampaignInstallerLifecycle 重读四个普通动作事实后更新 campaign 报告。
//
// 参数：
//   - packageRoot: 已通过 package manifest 约束的便携包根目录。
//   - resultsRoot/campaignID: 待更新的精确结果根与 campaign 身份。
//   - preparedBackup: 与 campaign/lane/baseline 绑定的 prepared backup。
//
// 返回：
//   - 重新派生并原子持久化后的 CampaignReport。
//   - package、prepared baseline、动作文件或报告身份不一致时的错误。
//
// 注意：只消费严格校验的四动作事实，不从 runtime、cleanup 或持久化 verdict 反推动作。
func FinalizeCampaignInstallerLifecycle(packageRoot, resultsRoot, campaignID, preparedBackup string) (report CampaignReport, finalizeErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationInstallerLifecycleFinalize")
	stage, lane := "validate_campaign_identity", ""
	defer func() {
		fields := map[string]any{"campaign_id": safeCampaignID(campaignID), "lane": safeInstallerLifecycleLane(lane), "stage": stage}
		if finalizeErr != nil {
			log.WithFields(fields).Error("Windows installer lifecycle 最终事实合并失败")
			return
		}
		log.WithFields(fields).Info("Windows installer lifecycle 最终事实合并完成")
	}()
	if !campaignIDPattern.MatchString(campaignID) {
		return CampaignReport{}, fmt.Errorf("invalid campaign identity")
	}
	stage = "verify_package_integrity"
	if err := VerifyPackageIntegrity(packageRoot); err != nil {
		return CampaignReport{}, fmt.Errorf("verify validation package: %w", err)
	}
	source, err := LoadPackageSource(packageRoot)
	if err != nil {
		return CampaignReport{}, err
	}
	root, err := filepath.Abs(resultsRoot)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("resolve results root: %w", err)
	}
	campaignDirectory := filepath.Join(root, campaignID)
	stage = "read_campaign_report"
	if err := readJSONFile(filepath.Join(campaignDirectory, "campaign-report.json"), &report); err != nil {
		return CampaignReport{}, fmt.Errorf("read campaign report: %w", err)
	}
	if report.CampaignID != campaignID {
		return CampaignReport{}, fmt.Errorf("campaign report identity mismatch")
	}
	lane = report.Lane
	report, err = rederiveCampaignReport(report)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive persisted campaign report: %w", err)
	}
	backupDirectory, prepared, err := loadPreparedBackupIdentity(preparedBackup, campaignID, report.Lane)
	if err != nil {
		return CampaignReport{}, err
	}
	if err := verifyPreparedBaselineIntegrity(backupDirectory, prepared, CleanupRecord{}); err != nil {
		return CampaignReport{}, fmt.Errorf("verify prepared baseline before lifecycle finalize: %w", err)
	}
	if err := validateCleanInstallerBaseline(filepath.Join(backupDirectory, "baseline.json")); err != nil {
		return CampaignReport{}, err
	}
	if report.BuildCommit != source.Frozen.Build.GitCommit || report.ProductVersion != source.Frozen.Build.ProductVersion {
		return CampaignReport{}, fmt.Errorf("campaign report build identity does not match frozen package")
	}
	factBinding, err := readInstallerLifecycleFactBinding(backupDirectory)
	if err != nil {
		return CampaignReport{}, err
	}
	binding, err := installerLifecycleBinding(source.Frozen, prepared, report.InstallerChecks, factBinding.InstallDirectory)
	if err != nil {
		return CampaignReport{}, err
	}
	stage = "verify_action_facts"
	verified, err := loadVerifiedInstallerLifecycleFacts(backupDirectory, binding, resultInput(report.Installer.Artifact))
	if err != nil {
		return CampaignReport{}, err
	}
	report.Installer = verified.Execution
	report, err = rederiveCampaignReport(report)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive lifecycle-finalized campaign report: %w", err)
	}
	redactor := NewRedactor()
	stage = "write_campaign_report"
	if err := writeCampaignReports(campaignDirectory, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "write_validation_summary"
	if err := writeValidationSummary(root, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "complete"
	return report, nil
}

func installerExecutionFromPreparedLifecycle(frozen FrozenBuild, preparedBackup, campaignID, lane string, checks []PackageFileIdentity, artifact ResultInput, requiredActions int) (InstallerExecution, []InstallerLifecycleActionEvidence, error) {
	backupDirectory, prepared, err := loadPreparedBackupIdentity(preparedBackup, campaignID, lane)
	if err != nil {
		return InstallerExecution{}, nil, err
	}
	if err := verifyPreparedBaselineIntegrity(backupDirectory, prepared, CleanupRecord{}); err != nil {
		return InstallerExecution{}, nil, fmt.Errorf("verify prepared baseline for installer lifecycle: %w", err)
	}
	if err := validateCleanInstallerBaseline(filepath.Join(backupDirectory, "baseline.json")); err != nil {
		return InstallerExecution{}, nil, err
	}
	factBinding, err := readInstallerLifecycleFactBinding(backupDirectory)
	if err != nil {
		return InstallerExecution{}, nil, err
	}
	binding, err := installerLifecycleBinding(frozen, prepared, checks, factBinding.InstallDirectory)
	if err != nil {
		return InstallerExecution{}, nil, err
	}
	verified, err := loadVerifiedInstallerLifecycleFacts(backupDirectory, binding, artifact)
	if err != nil {
		return InstallerExecution{}, nil, err
	}
	if len(verified.Evidence) != requiredActions {
		return InstallerExecution{}, nil, fmt.Errorf("installer lifecycle has %d recorded actions, need exactly %d at functional entry", len(verified.Evidence), requiredActions)
	}
	return verified.Execution, verified.Evidence, nil
}

func installerExecutionWithoutRecordedLifecycle(lane string, artifact ResultInput, reason string) (InstallerExecution, error) {
	formatName := installerFormatForLane(lane)
	if formatName == "" {
		return InstallerExecution{}, fmt.Errorf("lane does not have an installer lifecycle")
	}
	artifactResult, err := DeriveValidationResult(artifact)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("validate installer artifact while lifecycle is missing: %w", err)
	}
	notRun := ResultInput{Facts: ExecutionFacts{NotRunReason: reason}}
	return DeriveInstallerExecution(InstallerExecutionFacts{
		Format: formatName, ArtifactVerified: artifactResult.PhaseStatus == PhaseStatusPass, InstallerExecuted: false,
		Artifact: artifact, Install: notRun, Start: notRun, Stop: notRun, Uninstall: notRun,
	})
}

func requireInstallerFunctionalEntry(installer InstallerExecution) error {
	if installer.Install.PhaseStatus != PhaseStatusPass || installer.Start.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("installer functional entry requires install and start PASS")
	}
	// 功能验证只能紧跟受信 start；stop/uninstall 已有事实时不能借旧 start 继续。
	if installer.Stop.PhaseStatus != PhaseStatusNotRun || installer.Uninstall.PhaseStatus != PhaseStatusNotRun {
		return fmt.Errorf("installer functional entry requires stop and uninstall to remain NOT_RUN")
	}
	return nil
}

func readInstallerLifecycleFactBinding(backupDirectory string) (InstallerLifecycleBinding, error) {
	directory := filepath.Join(backupDirectory, "installer-lifecycle")
	present, err := inspectInstallerLifecycleFactFiles(directory)
	if err != nil {
		return InstallerLifecycleBinding{}, err
	}
	if present != 1 && present != 2 && present != 3 && present != 4 {
		return InstallerLifecycleBinding{}, fmt.Errorf("installer lifecycle install fact is missing")
	}
	var fact InstallerLifecycleActionEvidence
	if err := readStrictInstallerLifecycleJSON(filepath.Join(directory, installerLifecycleFactFilename(LifecycleActionInstall)), &fact); err != nil {
		return InstallerLifecycleBinding{}, fmt.Errorf("read installer lifecycle install fact: %w", err)
	}
	if fact.SchemaVersion != 1 || fact.Kind != InstallerLifecycleActionFactKind || fact.Action != LifecycleActionInstall {
		return InstallerLifecycleBinding{}, fmt.Errorf("installer lifecycle install fact identity is invalid")
	}
	if err := validateInstallerLifecycleBinding(fact.Binding); err != nil {
		return InstallerLifecycleBinding{}, err
	}
	return fact.Binding, nil
}

func loadVerifiedInstallerLifecycleFacts(backupDirectory string, binding InstallerLifecycleBinding, artifact ResultInput) (verifiedInstallerLifecycle, error) {
	directory := filepath.Join(backupDirectory, "installer-lifecycle")
	count, err := inspectInstallerLifecycleFactFiles(directory)
	if err != nil {
		return verifiedInstallerLifecycle{}, err
	}
	evidence := make([]InstallerLifecycleActionEvidence, 0, count)
	for index := 0; index < count; index++ {
		action := installerLifecycleActionOrder[index]
		var fact InstallerLifecycleActionEvidence
		path := filepath.Join(directory, installerLifecycleFactFilename(action))
		if err := readStrictInstallerLifecycleJSON(path, &fact); err != nil {
			return verifiedInstallerLifecycle{}, fmt.Errorf("read installer lifecycle %s fact: %w", action, err)
		}
		if fact.SchemaVersion != 1 || fact.Kind != InstallerLifecycleActionFactKind || fact.Action != action || fact.Binding != binding {
			return verifiedInstallerLifecycle{}, fmt.Errorf("installer lifecycle %s fact identity does not match the prepared campaign", action)
		}
		evidence = append(evidence, fact)
	}
	execution, err := deriveInstallerLifecycleEvidence(evidence, binding, artifact)
	if err != nil {
		return verifiedInstallerLifecycle{}, err
	}
	return verifiedInstallerLifecycle{Evidence: evidence, Execution: execution}, nil
}

func inspectInstallerLifecycleFactFiles(directory string) (int, error) {
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read installer lifecycle fact directory: %w", err)
	}
	allowed := make(map[string]int, len(installerLifecycleActionOrder))
	for index, action := range installerLifecycleActionOrder {
		allowed[installerLifecycleFactFilename(action)] = index
	}
	present := make([]bool, len(installerLifecycleActionOrder))
	for _, entry := range entries {
		index, ok := allowed[entry.Name()]
		if !ok || entry.IsDir() {
			return 0, fmt.Errorf("installer lifecycle fact directory contains an unsupported entry")
		}
		present[index] = true
	}
	count := 0
	missingSeen := false
	for _, exists := range present {
		if !exists {
			missingSeen = true
			continue
		}
		if missingSeen {
			return 0, fmt.Errorf("installer lifecycle action facts are missing or out of order")
		}
		count++
	}
	return count, nil
}

func readStrictInstallerLifecycleJSON(path string, target any) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("installer lifecycle JSON contains multiple values")
		}
		return err
	}
	return nil
}

func installFact(evidence []InstallerLifecycleActionEvidence) *InstallerLifecycleActionEvidence {
	if len(evidence) == 0 {
		return nil
	}
	return &evidence[0]
}

func startFact(evidence []InstallerLifecycleActionEvidence) *InstallerLifecycleActionEvidence {
	if len(evidence) < 2 {
		return nil
	}
	return &evidence[1]
}

func executionFactsFromFixedExecutor(result installerLifecycleExecutorResult) (ExecutionFacts, error) {
	if !result.Attempted {
		return ExecutionFacts{}, fmt.Errorf("fixed lifecycle executor did not attempt the selected action")
	}
	allowedFailures := map[InstallerLifecycleAction]map[string]string{
		LifecycleActionInstall: {
			"installer_invocation_failed": "fixed frozen installer invocation failed",
			"install_observation_failed":  "installed product observation did not satisfy the fixed contract",
		},
		LifecycleActionStart: {
			"desktop_invocation_failed": "fixed installed Desktop invocation failed",
			"start_observation_failed":  "Desktop or owning Agent observation did not satisfy the fixed contract",
		},
		LifecycleActionStop: {
			"desktop_close_failed":    "fixed Desktop close operation failed",
			"stop_observation_failed": "bound processes or Agent listener did not stop",
		},
		LifecycleActionUninstall: {
			"uninstaller_invocation_failed": "fixed official uninstaller invocation failed",
			"uninstall_observation_failed":  "removed product observation did not satisfy the fixed contract",
		},
	}
	facts := ExecutionFacts{
		Attempted: true, Succeeded: result.Succeeded,
		StartedAtUTC: result.StartedAtUTC, FinishedAtUTC: result.FinishedAtUTC,
	}
	if result.Succeeded {
		if result.FailureCode != "" {
			return ExecutionFacts{}, fmt.Errorf("successful fixed lifecycle result contains a failure code")
		}
		return facts, nil
	}
	safeFailure, ok := allowedFailures[result.Action][result.FailureCode]
	if !ok {
		return ExecutionFacts{}, fmt.Errorf("fixed lifecycle executor returned an unrecognized failure code")
	}
	facts.Failure = safeFailure
	return facts, nil
}

func deriveInstallerLifecycleEvidence(evidence []InstallerLifecycleActionEvidence, binding InstallerLifecycleBinding, artifact ResultInput) (InstallerExecution, error) {
	if err := validateInstallerLifecycleBinding(binding); err != nil {
		return InstallerExecution{}, err
	}
	artifactResult, err := DeriveValidationResult(artifact)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("validate installer artifact result: %w", err)
	}
	inputs := make(map[InstallerLifecycleAction]ResultInput, len(evidence))
	var previousFinished time.Time
	var installEvidence *InstallerLifecycleActionEvidence
	var startEvidence *InstallerLifecycleActionEvidence
	attempted := false
	for index := range evidence {
		current := &evidence[index]
		if current.SchemaVersion != 1 || current.Kind != InstallerLifecycleActionFactKind ||
			current.Action != installerLifecycleActionOrder[index] || current.Binding != binding {
			return InstallerExecution{}, fmt.Errorf("installer lifecycle fact order or binding is invalid")
		}
		if err := validateInstallerLifecycleActionEvidence(*current, installEvidence, startEvidence); err != nil {
			return InstallerExecution{}, fmt.Errorf("validate installer lifecycle %s: %w", current.Action, err)
		}
		if current.ExecutionFacts.Attempted {
			attempted = true
			started, _ := time.Parse(time.RFC3339Nano, current.ExecutionFacts.StartedAtUTC)
			finished, _ := time.Parse(time.RFC3339Nano, current.ExecutionFacts.FinishedAtUTC)
			if !previousFinished.IsZero() && started.Before(previousFinished) {
				return InstallerExecution{}, fmt.Errorf("installer lifecycle action started before the prior action finished")
			}
			previousFinished = finished
		}
		ref := "prepared-backup://installer-lifecycle/" + installerLifecycleFactFilename(current.Action)
		resultPresent := current.ExecutionFacts.Attempted
		inputs[current.Action] = ResultInput{Facts: current.ExecutionFacts, Evidence: []EvidenceRecord{
			{Name: "fixed_executor_result", Required: true, Present: resultPresent, Ref: ref + "#execution_facts"},
			{Name: "command_execution", Required: true, Present: resultPresent && !emptyInstallerLifecycleCommand(current.Command), Ref: ref + "#command"},
			{Name: "observed_state", Required: true, Present: resultPresent && !emptyInstallerLifecycleObservation(current.Observation), Ref: ref + "#observation"},
		}}
		if current.Action == LifecycleActionInstall {
			installEvidence = current
		}
		if current.Action == LifecycleActionStart {
			startEvidence = current
		}
	}
	inputFor := func(action InstallerLifecycleAction) ResultInput {
		if input, ok := inputs[action]; ok {
			return input
		}
		return ResultInput{Facts: ExecutionFacts{NotRunReason: string(action) + " lifecycle action was not recorded"}}
	}
	return DeriveInstallerExecution(InstallerExecutionFacts{
		Format: binding.Format, ArtifactVerified: artifactResult.PhaseStatus == PhaseStatusPass, InstallerExecuted: attempted,
		Artifact: artifact, Install: inputFor(LifecycleActionInstall), Start: inputFor(LifecycleActionStart),
		Stop: inputFor(LifecycleActionStop), Uninstall: inputFor(LifecycleActionUninstall),
	})
}

func validateInstallerLifecycleActionEvidence(current InstallerLifecycleActionEvidence, installEvidence, startEvidence *InstallerLifecycleActionEvidence) error {
	derived, err := DeriveValidationResult(ResultInput{Facts: current.ExecutionFacts})
	if err != nil {
		return err
	}
	if !current.ExecutionFacts.Attempted {
		if derived.PhaseStatus != PhaseStatusBlocked || !emptyInstallerLifecycleCommand(current.Command) || !emptyInstallerLifecycleObservation(current.Observation) {
			return fmt.Errorf("unattempted lifecycle fact must be a named blocker without command or observation facts")
		}
		return nil
	}
	started, err := time.Parse(time.RFC3339Nano, current.ExecutionFacts.StartedAtUTC)
	if err != nil {
		return fmt.Errorf("started_at_utc is invalid")
	}
	finished, err := time.Parse(time.RFC3339Nano, current.ExecutionFacts.FinishedAtUTC)
	if err != nil || !finished.After(started) || finished.After(time.Now().UTC().Add(10*time.Minute)) {
		return fmt.Errorf("finished_at_utc is invalid")
	}
	if current.Command.Operation != current.Action {
		return fmt.Errorf("command operation does not match lifecycle action")
	}
	requireOutcome := current.ExecutionFacts.Succeeded || current.Action == LifecycleActionInstall || current.Action == LifecycleActionUninstall
	if err := validateInstallerLifecycleCommand(current.Command, current.Binding, installEvidence, requireOutcome); err != nil {
		return err
	}
	if err := validateInstallerLifecycleChecks(current.Action, current.Observation.Checks, current.ExecutionFacts.Succeeded); err != nil {
		return err
	}
	if current.ExecutionFacts.Succeeded {
		if err := validateSuccessfulInstallerLifecycleObservation(current, installEvidence, startEvidence); err != nil {
			return err
		}
	}
	return nil
}

func validateInstallerLifecycleCommand(command InstallerLifecycleCommandFact, binding InstallerLifecycleBinding, installEvidence *InstallerLifecycleActionEvidence, requireOutcome bool) error {
	wantMethod, wantExecutable := "", ""
	switch command.Operation {
	case LifecycleActionInstall:
		wantMethod = "start_process_wait_elevated"
		if binding.Format == "msi" {
			wantExecutable = "msiexec.exe"
		} else {
			wantExecutable = binding.Artifact.Path
		}
		if command.Target != binding.Artifact {
			return fmt.Errorf("installer command target does not match frozen artifact")
		}
	case LifecycleActionStart:
		wantMethod, wantExecutable = "start_process", "SuperDev.exe"
		if installEvidence == nil || len(installEvidence.Observation.ProductFiles) != 1 || command.Target != installEvidence.Observation.ProductFiles[0] {
			return fmt.Errorf("Desktop start target does not match the installed product identity")
		}
	case LifecycleActionStop:
		wantMethod, wantExecutable = "close_main_window", "SuperDev.exe"
		if installEvidence == nil || len(installEvidence.Observation.ProductFiles) != 1 || command.Target != installEvidence.Observation.ProductFiles[0] {
			return fmt.Errorf("Desktop stop target does not match the installed product identity")
		}
	case LifecycleActionUninstall:
		wantMethod = "start_process_wait_elevated"
		if binding.Format == "msi" {
			wantExecutable = "msiexec.exe"
			if command.Target != binding.Artifact {
				return fmt.Errorf("MSI uninstall target does not match frozen artifact")
			}
		} else {
			wantExecutable = binding.UninstallerFilename
			if installEvidence == nil || installEvidence.Observation.UninstallerFile == nil || command.Target != *installEvidence.Observation.UninstallerFile {
				return fmt.Errorf("NSIS uninstall target does not match installed official uninstaller identity")
			}
		}
	}
	if command.Method != wantMethod || !strings.EqualFold(command.Executable, wantExecutable) || command.Executable != filepath.Base(command.Executable) {
		return fmt.Errorf("fixed command identity is invalid")
	}
	if binding.Format == "msi" && (command.Operation == LifecycleActionInstall || command.Operation == LifecycleActionUninstall) &&
		command.ProductCode != "" && !installerLifecycleMSIProductCodePattern.MatchString(command.ProductCode) {
		return fmt.Errorf("MSI product code identity is invalid")
	}
	if !requireOutcome {
		return nil
	}
	if command.Operation == LifecycleActionInstall || command.Operation == LifecycleActionUninstall {
		if command.ExitCode == nil {
			return fmt.Errorf("installer operation did not retain an exit code")
		}
	} else if len(command.ProcessIDs) == 0 {
		return fmt.Errorf("process lifecycle operation did not retain process identities")
	}
	return nil
}

func validateSuccessfulInstallerLifecycleObservation(current InstallerLifecycleActionEvidence, installEvidence, startEvidence *InstallerLifecycleActionEvidence) error {
	switch current.Action {
	case LifecycleActionInstall:
		if current.Command.ExitCode == nil || *current.Command.ExitCode != 0 || current.Observation.InstallPathPresent == nil || !*current.Observation.InstallPathPresent {
			return fmt.Errorf("successful install requires exit 0 and an observed install root")
		}
		if err := validateInstalledFileIdentities(current.Observation.ProductFiles, current.Observation.SidecarFiles); err != nil {
			return err
		}
		if err := validateInstalledUninstallIdentity(current); err != nil {
			return err
		}
		if current.Binding.Format == "msi" && !installerLifecycleMSIProductCodePattern.MatchString(current.Command.ProductCode) {
			return fmt.Errorf("successful MSI install requires an exact product code")
		}
		if current.Binding.Format == "nsis" {
			if current.Observation.UninstallerFile == nil || !strings.EqualFold(filepath.Base(current.Observation.UninstallerFile.Path), current.Binding.UninstallerFilename) {
				return fmt.Errorf("successful NSIS install requires the official uninstaller identity")
			}
			if err := validateInstalledFileIdentity(*current.Observation.UninstallerFile); err != nil {
				return err
			}
		}
	case LifecycleActionStart:
		if installEvidence == nil || current.Observation.Port57017 == nil || !current.Observation.Port57017.Listening || current.Observation.Port57017.Port != 57017 {
			return fmt.Errorf("successful start requires a 57017 listener identity")
		}
		if !samePositiveProcessIDs(current.Command.ProcessIDs, desktopProcessIDs(current.Observation.Processes)) {
			return fmt.Errorf("started Desktop process identity does not match the fixed command")
		}
		if err := validateStartedProcessIdentities(current.Observation, *installEvidence); err != nil {
			return err
		}
	case LifecycleActionStop:
		if startEvidence == nil || !samePositiveProcessIDs(current.Command.ProcessIDs, startEvidence.Command.ProcessIDs) {
			return fmt.Errorf("stop target does not match the campaign start Desktop PID")
		}
		if current.Observation.Port57017 == nil || current.Observation.Port57017.Listening || len(current.Observation.RemainingBoundProcessIDs) != 0 {
			return fmt.Errorf("successful stop requires no bound process or Agent listener")
		}
	case LifecycleActionUninstall:
		if current.Command.ExitCode == nil || *current.Command.ExitCode != 0 || current.Observation.InstallPathPresent == nil || *current.Observation.InstallPathPresent ||
			len(current.Observation.ProductFiles) != 0 || len(current.Observation.SidecarFiles) != 0 || len(current.Observation.UninstallEntries) != 0 {
			return fmt.Errorf("successful uninstall requires exit 0 and absent installed identities")
		}
		if current.Binding.Format == "msi" {
			if !installerLifecycleMSIProductCodePattern.MatchString(current.Command.ProductCode) {
				return fmt.Errorf("MSI uninstall product code identity is invalid")
			}
			if installEvidence == nil || !strings.EqualFold(current.Command.ProductCode, installEvidence.Command.ProductCode) {
				return fmt.Errorf("MSI uninstall product code does not match the installed product identity")
			}
		}
	}
	return nil
}

func validateInstalledUninstallIdentity(install InstallerLifecycleActionEvidence) error {
	if len(install.Observation.UninstallEntries) != 1 {
		return fmt.Errorf("successful install requires exactly one uninstall identity")
	}
	entry := install.Observation.UninstallEntries[0]
	if (entry.Scope != "HKCU" && entry.Scope != "HKLM" && entry.Scope != "HKLM32") || entry.DisplayName != "SuperDev" ||
		entry.DisplayVersion != install.Binding.ProductVersion || !sha256Pattern.MatchString(entry.UninstallStringSHA256) {
		return fmt.Errorf("installed uninstall identity does not match the frozen product")
	}
	if install.Binding.Format == "msi" {
		if !strings.EqualFold(entry.Key, install.Command.ProductCode) || !installerLifecycleMSIProductCodePattern.MatchString(entry.Key) ||
			!strings.EqualFold(filepath.Base(entry.UninstallExecutable), "msiexec.exe") ||
			(strings.TrimSpace(entry.InstallLocation) != "" && !sameNormalizedPath(entry.InstallLocation, install.Binding.InstallDirectory)) {
			return fmt.Errorf("MSI uninstall registration is not bound to the installed product code")
		}
		return nil
	}
	if !sameNormalizedPath(entry.InstallLocation, install.Binding.InstallDirectory) {
		return fmt.Errorf("NSIS uninstall registration does not match the bound install root")
	}
	if install.Observation.UninstallerFile == nil {
		return fmt.Errorf("NSIS uninstall registration has no official uninstaller identity")
	}
	if !strings.EqualFold(filepath.Base(install.Observation.UninstallerFile.Path), install.Binding.UninstallerFilename) {
		return fmt.Errorf("NSIS uninstall identity does not name the fixed official uninstaller")
	}
	if err := validateInstalledFileIdentity(*install.Observation.UninstallerFile); err != nil {
		return err
	}
	expected := filepath.Join(install.Binding.InstallDirectory, filepath.FromSlash(install.Observation.UninstallerFile.Path))
	if !sameNormalizedPath(entry.UninstallExecutable, expected) {
		return fmt.Errorf("NSIS uninstall registration does not target the hash-verified official uninstaller")
	}
	return nil
}

func validateStartedProcessIdentities(observation InstallerLifecycleObservation, install InstallerLifecycleActionEvidence) error {
	installed := append(append([]PackageFileIdentity{}, install.Observation.ProductFiles...), install.Observation.SidecarFiles...)
	desktop, owner := false, false
	for _, process := range observation.Processes {
		if process.ProcessID <= 0 || (process.Role != "desktop" && process.Role != "desktop_child" && process.Role != "agent" && process.Role != "sidecar") {
			return fmt.Errorf("started process identity is invalid")
		}
		if !containsInstallerPackageIdentity(installed, process.Executable) {
			return fmt.Errorf("started process executable does not match an installed file identity")
		}
		if process.Role == "desktop" && containsInt(desktopProcessIDs(observation.Processes), process.ProcessID) {
			desktop = true
		}
		if observation.Port57017 != nil && process.Role == "agent" && process.ProcessID == observation.Port57017.OwningProcessID {
			owner = true
		}
	}
	if !desktop || !owner {
		return fmt.Errorf("Desktop or owning Agent process identity is missing")
	}
	return nil
}

func buildInstallerLifecycleExecutorRequest(options InstallerLifecycleExecuteOptions, binding InstallerLifecycleBinding, evidence []InstallerLifecycleActionEvidence, backupDirectory string) (installerLifecycleExecutorRequest, error) {
	preparedBackupDirectory, err := filepath.Abs(backupDirectory)
	if err != nil {
		return installerLifecycleExecutorRequest{}, fmt.Errorf("resolve lifecycle helper prepared backup: %w", err)
	}
	preparedBackupDirectory = filepath.Clean(preparedBackupDirectory)
	request := installerLifecycleExecutorRequest{
		SchemaVersion: 1, Kind: installerLifecycleExecutorRequestKind, Action: options.Action, Binding: binding,
		PreparedBackupDirectory: preparedBackupDirectory,
		ActiveLockPath:          filepath.Join(preparedBackupDirectory, installerLifecycleActiveLockFilename),
		Format:                  binding.Format, InstallerPath: options.InstallerPath, InstallDirectory: binding.InstallDirectory,
		ProductVersion: binding.ProductVersion, Artifact: binding.Artifact,
	}
	if len(evidence) > 0 {
		request.InstalledFiles = append(request.InstalledFiles, evidence[0].Observation.ProductFiles...)
		request.InstalledFiles = append(request.InstalledFiles, evidence[0].Observation.SidecarFiles...)
	}
	switch options.Action {
	case LifecycleActionStart:
		if len(evidence) == 0 {
			return installerLifecycleExecutorRequest{}, fmt.Errorf("installed Desktop identity is missing")
		}
		desktop, ok := findInstalledIdentity(evidence[0].Observation.ProductFiles, "SuperDev.exe")
		if !ok {
			return installerLifecycleExecutorRequest{}, fmt.Errorf("installed Desktop identity is missing")
		}
		request.DesktopPath = filepath.Join(binding.InstallDirectory, filepath.FromSlash(desktop.Path))
		if err := verifyInstalledIdentity(binding.InstallDirectory, request.DesktopPath, desktop); err != nil {
			return installerLifecycleExecutorRequest{}, err
		}
	case LifecycleActionStop:
		if len(evidence) < 2 {
			return installerLifecycleExecutorRequest{}, fmt.Errorf("start process identity is missing")
		}
		request.StartProcessIDs = append([]int{}, evidence[1].Command.ProcessIDs...)
	case LifecycleActionUninstall:
		if len(evidence) == 0 || validateInstalledFileIdentities(evidence[0].Observation.ProductFiles, evidence[0].Observation.SidecarFiles) != nil ||
			validateInstalledUninstallIdentity(evidence[0]) != nil {
			return installerLifecycleExecutorRequest{}, fmt.Errorf("registry-bound installed product identity is missing")
		}
		uninstallEntry := evidence[0].Observation.UninstallEntries[0]
		request.UninstallEntry = &uninstallEntry
		if binding.Format == "nsis" {
			uninstaller := evidence[0].Observation.UninstallerFile
			request.UninstallerPath = filepath.Join(binding.InstallDirectory, filepath.FromSlash(uninstaller.Path))
			request.UninstallerIdentity = *uninstaller
			if err := verifyInstalledIdentity(binding.InstallDirectory, request.UninstallerPath, *uninstaller); err != nil {
				return installerLifecycleExecutorRequest{}, err
			}
		}
	}
	return request, nil
}

func probeInstallerLifecycleActiveLock(path string) error {
	release, err := acquireInstallerLifecycleLock(path)
	if err != nil {
		return fmt.Errorf("installer lifecycle helper action is already active: %w", err)
	}
	release()
	return nil
}

func blockedInstallerLifecycleFact(action InstallerLifecycleAction, binding InstallerLifecycleBinding, evidence []InstallerLifecycleActionEvidence) (InstallerLifecycleActionEvidence, bool) {
	blockedBy, reason := "", ""
	switch action {
	case LifecycleActionStart:
		if len(evidence) != 1 || lifecycleEvidenceStatus(evidence[0]) != PhaseStatusPass {
			blockedBy, reason = "install", "install did not satisfy the fixed lifecycle contract"
		}
	case LifecycleActionStop:
		if len(evidence) != 2 || len(evidence[1].Command.ProcessIDs) == 0 {
			blockedBy, reason = "start_process_identity", "start did not retain a Desktop PID safe to close"
		}
	case LifecycleActionUninstall:
		if len(evidence) == 0 || !evidence[0].ExecutionFacts.Attempted {
			blockedBy, reason = "install_attempt", "installer was not attempted"
		} else if validateInstalledFileIdentities(evidence[0].Observation.ProductFiles, evidence[0].Observation.SidecarFiles) != nil ||
			validateInstalledUninstallIdentity(evidence[0]) != nil {
			blockedBy, reason = "official_uninstaller_identity", "bound installed product or official uninstaller identity is unavailable"
		}
	}
	if blockedBy == "" {
		return InstallerLifecycleActionEvidence{}, false
	}
	// 前置动作不足时写入明确 BLOCKED fact；旁证和机器当前状态都不能反推本动作已执行。
	return InstallerLifecycleActionEvidence{
		SchemaVersion: 1, Kind: InstallerLifecycleActionFactKind, Action: action, Binding: binding,
		ExecutionFacts: ExecutionFacts{BlockedBy: blockedBy, Failure: reason},
	}, true
}

func installerLifecycleBinding(frozen FrozenBuild, prepared preparedBackupManifest, checks []PackageFileIdentity, installDirectory string) (InstallerLifecycleBinding, error) {
	formatName := installerFormatForLane(prepared.Lane)
	if formatName == "" {
		return InstallerLifecycleBinding{}, fmt.Errorf("prepared lane cannot bind installer lifecycle")
	}
	var expected InstallerIdentity
	for _, candidate := range frozen.Installers {
		if candidate.Format == formatName {
			if expected.Filename != "" {
				return InstallerLifecycleBinding{}, fmt.Errorf("frozen package has duplicate installer formats")
			}
			expected = candidate
		}
	}
	if expected.Filename == "" || len(checks) != 1 {
		return InstallerLifecycleBinding{}, fmt.Errorf("campaign does not have one frozen installer identity")
	}
	actual := checks[0]
	if actual.Path != expected.Filename || actual.SizeBytes != expected.SizeBytes || !strings.EqualFold(actual.SHA256, expected.SHA256) {
		return InstallerLifecycleBinding{}, fmt.Errorf("campaign installer identity does not match the frozen artifact")
	}
	normalized, digest, err := normalizeInstallDirectory(installDirectory)
	if err != nil {
		return InstallerLifecycleBinding{}, err
	}
	actual.SHA256 = strings.ToLower(actual.SHA256)
	binding := InstallerLifecycleBinding{
		CampaignID: prepared.CampaignID, Lane: prepared.Lane, Format: formatName,
		UninstallerFilename: expected.UninstallerFilename, BuildCommit: frozen.Build.GitCommit,
		ProductVersion: frozen.Build.ProductVersion, PreparedBackupSHA256: preparedBackupIdentitySHA256(prepared), PreparedBaselineSHA256: prepared.BaselineSHA256,
		InstallDirectory: normalized, InstallDirectorySHA256: digest, Artifact: actual,
	}
	if err := validateInstallerLifecycleBinding(binding); err != nil {
		return InstallerLifecycleBinding{}, err
	}
	return binding, nil
}

func installerLifecycleBindingFromReport(report CampaignReport, prepared preparedBackupManifest, installDirectory string) (InstallerLifecycleBinding, error) {
	formatName := installerFormatForLane(report.Lane)
	if formatName == "" || len(report.InstallerChecks) != 1 {
		return InstallerLifecycleBinding{}, fmt.Errorf("campaign report cannot bind installer lifecycle")
	}
	normalized, digest, err := normalizeInstallDirectory(installDirectory)
	if err != nil {
		return InstallerLifecycleBinding{}, err
	}
	binding := InstallerLifecycleBinding{
		CampaignID: report.CampaignID, Lane: report.Lane, Format: formatName,
		UninstallerFilename: fixedUninstallerFilename(formatName), BuildCommit: report.BuildCommit,
		ProductVersion: report.ProductVersion, PreparedBackupSHA256: preparedBackupIdentitySHA256(prepared), PreparedBaselineSHA256: prepared.BaselineSHA256,
		InstallDirectory: normalized, InstallDirectorySHA256: digest, Artifact: report.InstallerChecks[0],
	}
	if err := validateInstallerLifecycleBinding(binding); err != nil {
		return InstallerLifecycleBinding{}, err
	}
	return binding, nil
}

func validateInstallerLifecycleBinding(binding InstallerLifecycleBinding) error {
	if !campaignIDPattern.MatchString(binding.CampaignID) {
		return fmt.Errorf("installer lifecycle campaign identity is invalid")
	}
	if installerFormatForLane(binding.Lane) != binding.Format || binding.UninstallerFilename != fixedUninstallerFilename(binding.Format) {
		return fmt.Errorf("installer lifecycle lane/format identity is invalid")
	}
	if strings.TrimSpace(binding.BuildCommit) == "" || strings.TrimSpace(binding.ProductVersion) == "" || !sha256Pattern.MatchString(binding.PreparedBackupSHA256) || !sha256Pattern.MatchString(binding.PreparedBaselineSHA256) {
		return fmt.Errorf("installer lifecycle frozen/prepared identity is incomplete")
	}
	normalized, digest, err := normalizeInstallDirectory(binding.InstallDirectory)
	if err != nil || normalized != binding.InstallDirectory || digest != binding.InstallDirectorySHA256 {
		return fmt.Errorf("installer lifecycle install-root binding is invalid")
	}
	if binding.Artifact.Path != filepath.Base(binding.Artifact.Path) || binding.Artifact.SizeBytes <= 0 || !sha256Pattern.MatchString(binding.Artifact.SHA256) {
		return fmt.Errorf("installer lifecycle frozen artifact identity is invalid")
	}
	return nil
}

func validateCleanInstallerBaseline(path string) error {
	type preparedWindowsPlatformFacts struct {
		ProductName       string   `json:"product_name"`
		CurrentBuild      string   `json:"current_build"`
		DisplayVersion    string   `json:"display_version"`
		InstallationType  string   `json:"installation_type"`
		Architecture      string   `json:"architecture"`
		UBR               string   `json:"ubr"`
		InstalledKBs      []string `json:"installed_kbs"`
		SupportScope      string   `json:"support_scope"`
		ESUEvidenceStatus string   `json:"esu_evidence_status"`
	}
	var baseline struct {
		SchemaVersion   int                          `json:"schema_version"`
		Kind            string                       `json:"kind"`
		WindowsPlatform preparedWindowsPlatformFacts `json:"windows_platform"`
		Processes       []json.RawMessage            `json:"superdev_processes"`
		Ports           []json.RawMessage            `json:"listening_port_57017"`
		InstallPaths    []struct {
			Present bool `json:"present"`
		} `json:"install_paths"`
		UninstallEntries []json.RawMessage `json:"uninstall_entries"`
	}
	if err := readJSONFile(path, &baseline); err != nil {
		return fmt.Errorf("read prepared installer baseline: %w", err)
	}
	if baseline.SchemaVersion != 1 || baseline.Kind != "superdev.windows-validation.machine-baseline" {
		return fmt.Errorf("prepared installer baseline identity is invalid")
	}
	platform := baseline.WindowsPlatform
	if err := validateWindowsPlatformArchiveEvidence(WindowsPlatformObservation{
		ProductName: platform.ProductName, CurrentBuild: platform.CurrentBuild,
		DisplayVersion: platform.DisplayVersion, InstallationType: platform.InstallationType,
		Architecture: platform.Architecture, UBR: platform.UBR, InstalledKBs: platform.InstalledKBs,
	}); err != nil {
		return fmt.Errorf("prepared installer baseline platform evidence is invalid: %w", err)
	}
	if platform.SupportScope != WindowsValidationSupportScope || platform.ESUEvidenceStatus != WindowsValidationESUEvidenceStatus {
		return fmt.Errorf("prepared installer baseline platform support caveat is invalid")
	}
	presentInstallPaths := 0
	for _, candidate := range baseline.InstallPaths {
		if candidate.Present {
			presentInstallPaths++
		}
	}
	if len(baseline.Processes) != 0 || len(baseline.Ports) != 0 || len(baseline.UninstallEntries) != 0 || presentInstallPaths != 0 {
		return fmt.Errorf("installer lifecycle requires a clean prepared product baseline")
	}
	return nil
}

func loadPreparedBackupForLifecycle(preparedBackup string) (string, preparedBackupManifest, error) {
	backupDirectory, err := filepath.Abs(preparedBackup)
	if err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("resolve prepared backup: %w", err)
	}
	var manifest preparedBackupManifest
	if err := readJSONFile(filepath.Join(backupDirectory, "backup-manifest.json"), &manifest); err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("read prepared backup manifest: %w", err)
	}
	if err := validatePreparedBackupIdentity(manifest, manifest.CampaignID); err != nil {
		return "", preparedBackupManifest{}, err
	}
	if installerFormatForLane(manifest.Lane) == "" {
		return "", preparedBackupManifest{}, fmt.Errorf("prepared backup lane does not support installer lifecycle")
	}
	return backupDirectory, manifest, nil
}

func preparedBackupIdentitySHA256(prepared preparedBackupManifest) string {
	content, _ := json.Marshal(prepared)
	digest := sha256.Sum256(content)
	return fmt.Sprintf("%x", digest)
}

func validateInstallerLifecycleChecks(action InstallerLifecycleAction, checks []InstallerLifecycleStateCheck, succeeded bool) error {
	required := installerLifecycleRequiredChecks[action]
	if len(checks) != len(required) {
		return fmt.Errorf("lifecycle action has an invalid fixed check set")
	}
	seen := map[string]bool{}
	allMatched := true
	for _, check := range checks {
		if seen[check.Name] {
			return fmt.Errorf("lifecycle action has a duplicate fixed check")
		}
		seen[check.Name] = true
		allMatched = allMatched && check.Matched
		if succeeded && !check.Matched {
			return fmt.Errorf("successful lifecycle action contradicts an observed check")
		}
	}
	for _, name := range required {
		if !seen[name] {
			return fmt.Errorf("lifecycle action is missing a fixed check")
		}
	}
	// executor 的 succeeded 必须与固定观察集双向一致，防止持久化结果把
	// “全部满足”伪装成失败，掩盖真实命令/观察合同的矛盾。
	if !succeeded && allMatched {
		return fmt.Errorf("failed lifecycle action contradicts fully matched observed checks")
	}
	return nil
}

func validateInstalledFileIdentities(products, sidecars []PackageFileIdentity) error {
	if len(products) != 1 || len(sidecars) < 3 {
		return fmt.Errorf("successful install requires one Desktop and three sidecar identities")
	}
	if !strings.EqualFold(filepath.Base(products[0].Path), "SuperDev.exe") {
		return fmt.Errorf("installed Desktop identity is invalid")
	}
	if err := validateInstalledFileIdentity(products[0]); err != nil {
		return err
	}
	families := map[string]bool{"superdev-agent": false, "superdev-mcp": false, "superdev-sample": false}
	for _, identity := range sidecars {
		if err := validateInstalledFileIdentity(identity); err != nil {
			return err
		}
		name := strings.ToLower(filepath.Base(identity.Path))
		for family := range families {
			if strings.HasPrefix(name, family) && strings.HasSuffix(name, ".exe") {
				families[family] = true
			}
		}
	}
	for _, present := range families {
		if !present {
			return fmt.Errorf("installed sidecar family identity is missing")
		}
	}
	return nil
}

func validateInstalledFileIdentity(identity PackageFileIdentity) error {
	normalized := filepath.ToSlash(identity.Path)
	if strings.TrimSpace(normalized) == "" || strings.HasPrefix(normalized, "/") || strings.Contains(normalized, ":") ||
		strings.Contains("/"+normalized+"/", "/../") || identity.SizeBytes <= 0 || !sha256Pattern.MatchString(identity.SHA256) {
		return fmt.Errorf("installed file identity is invalid")
	}
	return nil
}

func verifyInstalledIdentity(root, absolutePath string, expected PackageFileIdentity) error {
	actual, err := fileIdentity(root, absolutePath)
	if err != nil {
		return fmt.Errorf("verify installed executable identity: %w", err)
	}
	if filepath.ToSlash(actual.Path) != filepath.ToSlash(expected.Path) || actual.SizeBytes != expected.SizeBytes || !strings.EqualFold(actual.SHA256, expected.SHA256) {
		return fmt.Errorf("installed executable identity drifted after install evidence")
	}
	return nil
}

func verifyInstalledMCPIdentity(path string, evidence []InstallerLifecycleActionEvidence) error {
	if len(evidence) == 0 {
		return fmt.Errorf("installer lifecycle install evidence is missing")
	}
	binding := evidence[0].Binding
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve packaged MCP path: %w", err)
	}
	if !pathWithinRoot(binding.InstallDirectory, abs) {
		return fmt.Errorf("runtime MCP is outside the lifecycle-bound install root")
	}
	actual, err := fileIdentity(binding.InstallDirectory, abs)
	if err != nil {
		return fmt.Errorf("read lifecycle-bound MCP identity: %w", err)
	}
	for _, identity := range evidence[0].Observation.SidecarFiles {
		if strings.HasPrefix(strings.ToLower(filepath.Base(identity.Path)), "superdev-mcp") && actual == identity {
			return nil
		}
	}
	return fmt.Errorf("runtime MCP identity does not match the installed lifecycle evidence")
}

func installerFormatForLane(lane string) string {
	if lane == "msi_smoke" {
		return "msi"
	}
	if lane == "nsis_core" {
		return "nsis"
	}
	return ""
}

func safeInstallerLifecycleLane(lane string) string {
	switch lane {
	case "msi_smoke", "nsis_core", "core_only":
		return lane
	default:
		return ""
	}
}

func fixedUninstallerFilename(formatName string) string {
	if formatName == "nsis" {
		return "uninstall.exe"
	}
	return ""
}

func normalizeInstallDirectory(path string) (string, string, error) {
	if strings.TrimSpace(path) == "" {
		return "", "", fmt.Errorf("install directory is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("normalize install directory: %w", err)
	}
	abs = filepath.Clean(abs)
	digestInput := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		digestInput = strings.ToLower(digestInput)
	}
	digest := sha256.Sum256([]byte(digestInput))
	return abs, fmt.Sprintf("%x", digest), nil
}

func isInstallerLifecycleAction(action InstallerLifecycleAction) bool {
	for _, candidate := range installerLifecycleActionOrder {
		if action == candidate {
			return true
		}
	}
	return false
}

func installerLifecycleFactFilename(action InstallerLifecycleAction) string {
	for index, candidate := range installerLifecycleActionOrder {
		if candidate == action {
			return fmt.Sprintf("%02d-%s.json", index+1, action)
		}
	}
	return ""
}

func writeInstallerLifecycleJSON(path string, value any) error {
	content, err := marshalIndentedJSON(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".fact-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
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

func marshalIndentedJSON(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func successfulArtifactInputForVerifiedInstaller() ResultInput {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return ResultInput{Facts: ExecutionFacts{Attempted: true, Succeeded: true, StartedAtUTC: now, FinishedAtUTC: now}, Evidence: []EvidenceRecord{{Name: "installer_identity", Required: true, Present: true, Ref: "frozen-package://manifest/frozen-build.json#installer"}}}
}

func lifecycleResultForAction(execution InstallerExecution, action InstallerLifecycleAction) ValidationResult {
	switch action {
	case LifecycleActionInstall:
		return execution.Install
	case LifecycleActionStart:
		return execution.Start
	case LifecycleActionStop:
		return execution.Stop
	default:
		return execution.Uninstall
	}
}

func lifecycleEvidenceStatus(evidence InstallerLifecycleActionEvidence) PhaseStatus {
	result, err := DeriveValidationResult(ResultInput{Facts: evidence.ExecutionFacts})
	if err != nil {
		return PhaseStatusFail
	}
	return result.PhaseStatus
}

func emptyInstallerLifecycleObservation(observation InstallerLifecycleObservation) bool {
	return len(observation.Checks) == 0 && observation.InstallPathPresent == nil && len(observation.ProductFiles) == 0 &&
		len(observation.SidecarFiles) == 0 && observation.UninstallerFile == nil && len(observation.UninstallEntries) == 0 &&
		len(observation.Processes) == 0 && observation.Port57017 == nil && len(observation.RemainingBoundProcessIDs) == 0
}

func emptyInstallerLifecycleCommand(command InstallerLifecycleCommandFact) bool {
	return command.Operation == "" && command.Method == "" && command.Executable == "" &&
		command.Target == (PackageFileIdentity{}) && command.ProductCode == "" && len(command.ProcessIDs) == 0 && command.ExitCode == nil
}

func findInstalledIdentity(identities []PackageFileIdentity, basename string) (PackageFileIdentity, bool) {
	for _, identity := range identities {
		if strings.EqualFold(filepath.Base(identity.Path), basename) {
			return identity, true
		}
	}
	return PackageFileIdentity{}, false
}

func containsInstallerPackageIdentity(identities []PackageFileIdentity, candidate PackageFileIdentity) bool {
	for _, identity := range identities {
		if identity == candidate {
			return true
		}
	}
	return false
}

func desktopProcessIDs(processes []InstallerLifecycleProcessIdentity) []int {
	ids := []int{}
	for _, process := range processes {
		if process.Role == "desktop" {
			ids = append(ids, process.ProcessID)
		}
	}
	sort.Ints(ids)
	return ids
}

func samePositiveProcessIDs(left, right []int) bool {
	return len(left) > 0 && sameProcessIDs(left, right)
}

func sameProcessIDs(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	counts := map[int]int{}
	for _, id := range left {
		if id <= 0 {
			return false
		}
		counts[id]++
	}
	for _, id := range right {
		if id <= 0 || counts[id] == 0 {
			return false
		}
		counts[id]--
	}
	return true
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func sameNormalizedPath(left, right string) bool {
	leftAbs, _, leftErr := normalizeInstallDirectory(left)
	rightAbs, _, rightErr := normalizeInstallDirectory(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(leftAbs, rightAbs)
	}
	return leftAbs == rightAbs
}

func safeCampaignID(value string) string {
	if campaignIDPattern.MatchString(value) {
		return value
	}
	return ""
}
