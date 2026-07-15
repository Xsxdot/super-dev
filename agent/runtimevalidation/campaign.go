// campaign.go 串联 target-native bundle、foundation clone、packaged Agent/MCP 与 strict scenario campaign。
//
// 职责：
//   - 按 bundle→native host→input→foundation 顺序执行 fail-closed preflight
//   - 启动真实 Agent、MCP、auth sidecar，执行七语言 provider 与全量 live tool 场景
//   - 在统一 journal/cleanup 后生成 authoritative report
//
// 边界：
//   - 不自动下载 toolchain、adapter、browser 或 remote Agent
//   - 不修改 foundation/borrowed topology，不恢复旧 active marker
//   - 不把 package verification、交叉编译或非原生宿主当作 target PASS
package runtimevalidation

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

// StrictCampaignOptions 提交当前 target bundle、非敏感 input 和仅存于进程内的 credential。
type StrictCampaignOptions struct {
	BundleRoot      string
	InputPath       string
	Target          Target
	CredentialValue string
	Now             func() time.Time
}

// StrictCampaignResult 返回 authoritative summary 和当前 campaign 结果目录。
type StrictCampaignResult struct {
	Summary    Summary
	ReportRoot string
}

type activeCampaignFacts struct {
	liveTools  []string
	coverage   CoverageReport
	toolResult ToolCampaignResult
	languages  []LanguageResult
	cleanup    CleanupResult
	journal    JournalSnapshot
	borrowed   BorrowedAttestation
	processLog string
	checks     []CheckResult
	runErr     error
}

// RunStrictCampaign 执行一次 target-native strict campaign 并写 authoritative report。
//
// 返回：
//   - PASS/FAIL/BLOCKED summary 与报告目录
//   - 无法加载 input 或写 authoritative report 时的 infrastructure 错误
func RunStrictCampaign(ctx context.Context, options StrictCampaignOptions) (StrictCampaignResult, error) {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationCampaign").WithField("target", options.Target.String())
	if !filepath.IsAbs(options.BundleRoot) || !filepath.IsAbs(options.InputPath) || !supportedTarget(options.Target) {
		return StrictCampaignResult{}, fmt.Errorf("bundle root, input path and supported target are required")
	}
	// Bundle 完整性在读取 foundation 之前完成，防止被篡改的 runner 借预检查访问受限 profile。
	bundle, err := VerifyBundle(options.BundleRoot, options.Target)
	if err != nil {
		return StrictCampaignResult{}, err
	}
	input, err := LoadRuntimeInput(options.InputPath)
	if err != nil {
		return StrictCampaignResult{}, err
	}
	campaignID, err := newRuntimeCampaignID(options.Target, now().UTC())
	if err != nil {
		return StrictCampaignResult{}, err
	}
	reportRoot := filepath.Join(input.ResultsRoot, campaignID)
	started := now().UTC()
	summary := Summary{CampaignID: campaignID, StartedAtUTC: started.Format(time.RFC3339Nano), Target: options.Target, Bundle: bundle}
	checks := []CheckResult{{ID: "bundle", Status: StatusPass}}

	host, hostErr := DetectHostIdentity()
	summary.Host = host
	if hostErr != nil {
		checks = append(checks, CheckResult{ID: "native-host", Status: StatusBlocked, Cause: Cause{Code: "native_identity_unavailable", Message: hostErr.Error(), Source: "native-host"}})
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	hostCheck := ValidateHostTarget(options.Target, host)
	checks = append(checks, hostCheck)
	if hostCheck.Status != StatusPass {
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	foundationCheck, err := ValidateFoundation(input.FoundationPath, input.ProfileID)
	if err != nil {
		return StrictCampaignResult{}, err
	}
	checks = append(checks, foundationCheck)
	if foundationCheck.Status != StatusPass {
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	stateRoot := FoundationStateRoot(input.FoundationPath, input.ProfileID)
	lock, err := AcquireFoundationLock(stateRoot)
	if err != nil {
		checks = append(checks, CheckResult{ID: "foundation-lock", Status: StatusBlocked, Cause: Cause{Code: "foundation_lock_unavailable", Message: err.Error(), Source: "foundation-lock"}})
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			log.WithErr(releaseErr).Error("释放 validation foundation runner-only 锁失败")
		}
	}()
	checks = append(checks, CheckResult{ID: "foundation-lock", Status: StatusPass})
	markerCheck, err := CheckActiveMarker(stateRoot)
	if err != nil {
		return StrictCampaignResult{}, err
	}
	checks = append(checks, markerCheck)
	if markerCheck.Status != StatusPass {
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	if options.CredentialValue == "" {
		checks = append(checks, CheckResult{ID: "credential-input", Status: StatusBlocked, Cause: Cause{Code: "credential_input_missing", Message: "one-time credential must be supplied through the process input channel", Source: "credential-input"}})
		return writePreflightSummary(reportRoot, summary, checks, "")
	}
	governanceDigest, err := validateGovernanceAttestation(input)
	if err != nil {
		checks = append(checks, CheckResult{ID: "borrowed-governance", Status: StatusBlocked, Cause: Cause{Code: "borrowed_governance_invalid", Message: err.Error(), Source: "borrowed-governance"}})
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	checks = append(checks, CheckResult{ID: "borrowed-governance", Status: StatusPass})
	dependencyCheck := RunReadOnlyPreflight(ctx, options.BundleRoot, input, options.Target, nil)
	checks = append(checks, dependencyCheck)
	if dependencyCheck.Status != StatusPass {
		return writePreflightSummary(reportRoot, summary, checks, options.CredentialValue)
	}
	log.WithFields(map[string]any{"campaign_id": campaignID, "bundle_digest": bundle.ManifestSHA256}).Info("runtime validation preflight 通过，进入 active campaign")
	facts := runActiveCampaign(ctx, options, input, campaignID, stateRoot, bundle.ManifestSHA256, governanceDigest, checks)
	summary.LiveTools = facts.liveTools
	summary.Coverage = facts.coverage
	summary.PrimaryEvidence = facts.toolResult.PrimaryEvidence
	summary.Languages = facts.languages
	summary.Borrowed = facts.borrowed
	summary.Journal = facts.journal
	summary.Cleanup = facts.cleanup
	summary.Checks = facts.checks
	verdictInput := VerdictInput{
		Checks: facts.checks, Coverage: facts.coverage, PrimaryEvidence: facts.toolResult.PrimaryEvidence,
		Cleanup: facts.cleanup, EvidenceComplete: facts.runErr == nil && facts.toolResult.Status == StatusPass,
	}
	verdict, verdictErr := DeriveVerdict(verdictInput)
	if verdictErr != nil {
		verdict = Verdict{Status: StatusFail, RootCause: Cause{Code: "verdict_contract_invalid", Message: verdictErr.Error(), Source: "verdict"}}
	}
	if facts.runErr != nil && verdict.Status == StatusPass {
		verdict = Verdict{Status: StatusFail, RootCause: Cause{Code: "campaign_execution_failed", Message: facts.runErr.Error(), Source: "campaign"}}
	}
	summary.Verdict = verdict
	written, err := WriteReport(reportRoot, ReportInput{
		Summary: summary,
		Evidence: map[string]any{
			"bundle.json": bundle, "host.json": host, "tool-campaign.json": facts.toolResult,
			"languages.json": facts.languages, "cleanup.json": facts.cleanup, "journal.json": facts.journal,
			"borrowed-attestation.json": facts.borrowed, "process-log.json": map[string]any{"redacted": facts.processLog},
		},
		ForbiddenSecrets: []string{options.CredentialValue},
	})
	if err != nil {
		return StrictCampaignResult{}, err
	}
	return StrictCampaignResult{Summary: written, ReportRoot: reportRoot}, nil
}

func runActiveCampaign(ctx context.Context, options StrictCampaignOptions, input RuntimeInput, campaignID, stateRoot, bundleDigest, governanceDigest string, checks []CheckResult) (facts activeCampaignFacts) {
	facts.checks = append([]CheckResult{}, checks...)
	logBuffer := &bytes.Buffer{}
	redactor := NewRedactingWriter(logBuffer)
	if err := redactor.RegisterSecret(options.CredentialValue); err != nil {
		facts.runErr = err
		return facts
	}
	defer func() {
		_ = redactor.Close()
		facts.processLog = logBuffer.String()
	}()
	foundationBefore, err := DigestPath(input.FoundationPath)
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("foundation-digest", "foundation_digest_failed", err))
		return facts
	}
	facts.borrowed = BorrowedAttestation{
		RemoteHostID: input.RemoteHostID, ExpectedRemoteIdentity: input.ExpectedRemoteIdentity,
		FoundationDigestBefore: foundationBefore,
	}
	cloneRoot := filepath.Join(stateRoot, "campaigns", campaignID, "profile")
	workRoot := filepath.Join(stateRoot, "campaigns", campaignID, "work")
	remoteRoot := strings.ReplaceAll(input.RemoteRootTemplate, "{campaign_id}", campaignID)
	marker := ActiveMarker{CampaignID: campaignID, BundleDigest: bundleDigest, ClonePath: cloneRoot, RemoteRoot: remoteRoot, StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := WriteActiveMarker(stateRoot, marker); err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("active-marker-write", "active_marker_write_failed", err))
		return facts
	}
	journal, err := OpenCleanupJournal(filepath.Join(stateRoot, "campaigns", campaignID, "cleanup.jsonl"), campaignID, nil)
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("cleanup-journal", "cleanup_journal_open_failed", err))
		return facts
	}
	stack := NewCleanupStack(journal)
	pipelineStarted, pipelineCleaned := false, false
	defer func() {
		foundationAfter, digestErr := DigestPath(input.FoundationPath)
		borrowedStable := digestErr == nil && facts.borrowed.FoundationDigestBefore == foundationAfter
		facts.borrowed.FoundationDigestAfter = foundationAfter
		facts.borrowed.BorrowedTopologyDigest = governanceDigest
		pipelineTerminal := !pipelineStarted || pipelineCleaned
		remoteAbsent := !pipelineStarted || pipelineCleaned
		stack.SetTerminalFacts(pipelineTerminal, remoteAbsent, borrowedStable, false)
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		facts.cleanup = stack.Cleanup(cleanupCtx)
		cancel()
		if facts.cleanup.JournalComplete && facts.cleanup.PipelineTerminal && facts.cleanup.RemoteRootAbsent && facts.cleanup.BorrowedTopologyStable && len(facts.cleanup.Residuals) == 0 {
			if removeErr := RemoveActiveMarker(stateRoot); removeErr == nil {
				facts.cleanup.ActiveMarkerRemoved = true
			} else {
				facts.cleanup.Residuals = append(facts.cleanup.Residuals, Residual{Kind: "active-marker", ID: campaignID, Detail: removeErr.Error()})
				facts.runErr = errors.Join(facts.runErr, removeErr)
			}
		}
		facts.journal = journal.Snapshot()
		if closeErr := journal.Close(); closeErr != nil {
			facts.runErr = errors.Join(facts.runErr, closeErr)
		}
	}()

	var cloneReceipt FoundationCloneReceipt
	_, err = stack.Acquire("profile-clone", campaignID, map[string]any{"path": cloneRoot}, func() (CleanupAction, error) {
		receipt, cloneErr := CloneFoundation(input.FoundationPath, cloneRoot)
		cloneReceipt = receipt
		return &removePathAction{kind: "profile-clone", id: campaignID, path: cloneRoot}, cloneErr
	})
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("profile-clone", "profile_clone_failed", err))
		return facts
	}
	if cloneReceipt.FoundationDigest != foundationBefore {
		facts.runErr = fmt.Errorf("foundation digest changed during clone")
		facts.checks = append(facts.checks, failedCampaignCheck("foundation-digest", "foundation_digest_drift", facts.runErr))
		return facts
	}
	_, err = stack.Acquire("campaign-workdir", campaignID, map[string]any{"path": workRoot}, func() (CleanupAction, error) {
		if mkdirErr := os.MkdirAll(workRoot, 0o700); mkdirErr != nil {
			return nil, mkdirErr
		}
		return &removePathAction{kind: "campaign-workdir", id: campaignID, path: workRoot}, nil
	})
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("campaign-workdir", "campaign_workdir_failed", err))
		return facts
	}
	if err := stageCampaignAssets(options.BundleRoot, cloneRoot, workRoot); err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("campaign-assets", "campaign_assets_failed", err))
		return facts
	}
	assetRoot := filepath.Join(options.BundleRoot, "validation")
	projectRoot := filepath.Join(workRoot, "project")
	scenarios, err := LoadScenarios(filepath.Join(assetRoot, "scenarios"))
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("scenario-assets", "scenario_assets_invalid", err))
		return facts
	}
	fixtures, err := LoadFixtures(filepath.Join(projectRoot, "fixtures"))
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("fixture-assets", "fixture_assets_invalid", err))
		return facts
	}
	ports, err := allocateCampaignPorts(fixtures)
	if err != nil {
		facts.runErr = err
		return facts
	}
	agentPort, err := allocateLoopbackPort()
	if err != nil {
		facts.runErr = err
		return facts
	}
	agentURL := fmt.Sprintf("http://127.0.0.1:%d", agentPort)
	agentBinary := filepath.Join(options.BundleRoot, "bin", "superdev-agent"+options.Target.ExecutableSuffix())
	var agentProcess *ManagedProcess
	_, err = stack.Acquire("process", "agent", map[string]any{"state": "stopped"}, func() (CleanupAction, error) {
		process, startErr := StartManagedProcess(ctx, ProcessSpec{
			Name: "runtime-validation-agent", Executable: agentBinary,
			Arguments: []string{"--addr", fmt.Sprintf("127.0.0.1:%d", agentPort), "--data", cloneRoot},
			Directory: workRoot, Stdout: redactor, Stderr: redactor,
		})
		agentProcess = process
		return &processCleanupAction{kind: "process", id: "agent", process: process}, startErr
	})
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("agent-process", "agent_start_failed", err))
		return facts
	}
	_ = agentProcess
	if err := waitForHTTPReady(ctx, agentURL+"/api/exec/health", 30*time.Second); err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("agent-ready", "agent_readiness_failed", err))
		return facts
	}
	facts.checks = append(facts.checks, CheckResult{ID: "agent-ready", Status: StatusPass})

	mcpBinary := filepath.Join(options.BundleRoot, "bin", "superdev-mcp"+options.Target.ExecutableSuffix())
	var mcpProcess *MCPProcess
	_, err = stack.Acquire("process", "mcp", map[string]any{"state": "stopped"}, func() (CleanupAction, error) {
		process, startErr := StartMCPProcess(ctx, MCPProcessSpec{Executable: mcpBinary, Directory: workRoot, AgentURL: agentURL, Stderr: redactor})
		mcpProcess = process
		return &mcpCleanupAction{id: "mcp", process: process}, startErr
	})
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("mcp-process", "mcp_start_failed", err))
		return facts
	}
	if _, err := mcpProcess.Initialize(ctx); err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("mcp-initialize", "mcp_initialize_failed", err))
		return facts
	}
	facts.checks = append(facts.checks, CheckResult{ID: "mcp-initialize", Status: StatusPass})

	sidecarURL, err := startAuthSidecar(ctx, stack, options.BundleRoot, workRoot, campaignID, options.CredentialValue, redactor)
	if err != nil {
		facts.runErr = err
		facts.checks = append(facts.checks, failedCampaignCheck("auth-sidecar", "auth_sidecar_failed", err))
		return facts
	}
	approvalCaller, err := NewApprovalToolCaller(mcpProcess, ApprovalActorOptions{
		AgentURL: agentURL, CampaignID: campaignID, AllowedKinds: DefaultRuntimeValidationApprovalKinds(),
	})
	if err != nil {
		facts.runErr = err
		return facts
	}
	credentialCaller, err := NewCredentialToolCaller(approvalCaller, CredentialActorOptions{
		AgentURL: agentURL, AuthSidecarURL: sidecarURL, CampaignID: campaignID,
		CredentialValue: options.CredentialValue, Redactor: redactor,
	})
	if err != nil {
		facts.runErr = err
		return facts
	}
	variables, err := campaignVariables(options.BundleRoot, projectRoot, input, campaignID, ports, fixtures)
	if err != nil {
		facts.runErr = err
		return facts
	}
	providerRunner := NewProviderRunner(approvalCaller, NewOSCommandExecutor(redactor), nil)
	executor := NewToolExecutor(mcpProcess, credentialCaller)
	facts.toolResult = executor.Run(ctx, ToolCampaignRequest{
		CampaignID: campaignID, Scenarios: scenarios, Variables: variables, Journal: journal,
		AfterBootstrap: func(callbackCtx context.Context, values map[string]any) error {
			projectID := strings.TrimSpace(fmt.Sprint(values["project_id"]))
			adapters := cloneStringMap(input.Adapters)
			adapters["resources/js-debug"] = filepath.Join(cloneRoot, "js-debug", "src", "dapDebugServer.js")
			facts.languages = providerRunner.RunMatrix(callbackCtx, ProviderMatrixRequest{
				CampaignID: campaignID, ProjectID: projectID, ProjectRoot: projectRoot, Platform: options.Target.OS,
				Fixtures: fixtures, Ports: ports, AdapterPaths: adapters, Cleanup: stack,
			})
			return validateProviderMatrixPass(facts.languages)
		},
	})
	facts.liveTools = facts.toolResult.LiveTools
	facts.coverage = facts.toolResult.Coverage
	facts.borrowed.RemoteNodeConfirmedNonSelf = remoteIdentityConfirmed(facts.toolResult)
	if facts.borrowed.RemoteNodeConfirmedNonSelf {
		facts.checks = append(facts.checks, CheckResult{ID: "borrowed-live-identity", Status: StatusPass})
	} else {
		facts.checks = append(facts.checks, failedCampaignCheck("borrowed-live-identity", "borrowed_live_identity_unconfirmed", fmt.Errorf("live list_hosts did not confirm the governed non-self node identity")))
	}
	pipelineStarted = !emptyManifestValue(facts.toolResult.Variables["pipeline_run_a"])
	// 提交 cleanup run 不等于远端已清理；只有终态和受控脚本 absence 日志都通过，才允许删除 active marker。
	pipelineCleaned = pipelineCleanupConfirmed(facts.toolResult)
	if facts.toolResult.Status != StatusPass {
		facts.runErr = fmt.Errorf("live tool campaign %s: %s", facts.toolResult.Status, facts.toolResult.Cause.Code)
		facts.checks = append(facts.checks, CheckResult{ID: "live-tools", Status: StatusFail, Cause: facts.toolResult.Cause})
		return facts
	}
	facts.checks = append(facts.checks, CheckResult{ID: "live-tools", Status: StatusPass})
	for _, language := range facts.languages {
		facts.checks = append(facts.checks,
			CheckResult{ID: "runtime-" + language.Provider, Status: language.RuntimeStatus, Cause: language.RuntimeCause},
			CheckResult{ID: "debug-" + language.Provider, Status: language.DebugStatus, Cause: language.DebugCause},
		)
	}
	return facts
}

func stageCampaignAssets(bundleRoot, cloneRoot, workRoot string) error {
	resourceSpecs := []ResourceSpec{}
	for _, item := range []struct{ id, source, destination string }{
		{"js-debug", filepath.Join(bundleRoot, "resources", "js-debug"), "js-debug"},
		{"playwright-driver", filepath.Join(bundleRoot, "resources", "playwright-driver"), "playwright-driver"},
	} {
		digest, err := DigestPath(item.source)
		if err != nil {
			return err
		}
		resourceSpecs = append(resourceSpecs, ResourceSpec{ID: item.id, Source: item.source, Destination: item.destination, SHA256: digest})
	}
	if _, err := StageResources(cloneRoot, resourceSpecs); err != nil {
		return err
	}
	projectRoot := filepath.Join(workRoot, "project")
	if err := os.MkdirAll(projectRoot, 0o700); err != nil {
		return err
	}
	_, err := copyResource(filepath.Join(bundleRoot, "validation", "fixtures"), filepath.Join(projectRoot, "fixtures"))
	return err
}

func startAuthSidecar(ctx context.Context, stack *CleanupStack, bundleRoot, workRoot, campaignID, credential string, output io.Writer) (string, error) {
	port, err := allocateLoopbackPort()
	if err != nil {
		return "", err
	}
	executable := filepath.Join(bundleRoot, "bin", "runtime-validation-auth-sidecar")
	if isWindowsExecutableTarget() {
		executable += ".exe"
	}
	_, err = stack.Acquire("process", "auth-sidecar", map[string]any{"state": "stopped"}, func() (CleanupAction, error) {
		process, startErr := StartManagedProcess(ctx, authSidecarProcessSpec(executable, workRoot, port, campaignID, credential, output))
		return &processCleanupAction{kind: "process", id: "auth-sidecar", process: process}, startErr
	})
	if err != nil {
		return "", err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if err := waitForHTTPReady(ctx, baseURL+"/healthz", 20*time.Second); err != nil {
		return "", err
	}
	return baseURL, nil
}

func authSidecarProcessSpec(executable, root string, port int, campaignID, credential string, output io.Writer) ProcessSpec {
	return ProcessSpec{
		Name: "runtime-validation-auth-sidecar", Executable: executable, Directory: root,
		Env: map[string]string{"AUTH_SIDECAR_PORT": fmt.Sprint(port), "AUTH_SIDECAR_CAMPAIGN_ID": campaignID},
		// os/exec 为非 *os.File Reader 创建匿名 pipe；secret 不进入 argv 或环境。
		Stdin: strings.NewReader(credential + "\n"), Stdout: output, Stderr: output,
	}
}

func campaignVariables(bundleRoot, projectRoot string, input RuntimeInput, campaignID string, ports map[string]int, fixtures []Fixture) (map[string]any, error) {
	pipelineRaw, err := os.ReadFile(filepath.Join(bundleRoot, "validation", "pipeline", "project-pipeline.json"))
	if err != nil {
		return nil, err
	}
	var remotePipeline map[string]any
	if err := json.Unmarshal(pipelineRaw, &remotePipeline); err != nil {
		return nil, err
	}
	goFixture := Fixture{}
	for _, fixture := range fixtures {
		if fixture.Provider == "go" {
			goFixture = fixture
		}
	}
	if goFixture.Provider == "" {
		return nil, fmt.Errorf("Go fixture is required")
	}
	goPort := ports["go"]
	projectName := campaignID
	variables := map[string]any{
		"campaign_id": campaignID, "run_id": campaignID, "project_name": projectName, "go_project_name": projectName,
		"project_root": projectRoot, "go_port": goPort, "go_readiness_url": fmt.Sprintf("http://127.0.0.1:%d/healthz", goPort),
		"go_artifact_dir": filepath.Join(projectRoot, "fixtures", "go"), "go_deployment_id": "go-validation-dev",
		"go_source_path": goFixture.Debug.Source, "go_breakpoint_line": goFixture.Debug.Line,
		"go_adapter_command": input.Adapters["dlv"], "browser_target_url": fmt.Sprintf("http://127.0.0.1:%d", goPort),
		"package_root": filepath.Join(bundleRoot, "validation"), "linux_host_id": input.RemoteHostID,
		"expected_remote_identity": input.ExpectedRemoteIdentity,
		"linux_root":               strings.ReplaceAll(input.RemoteRootTemplate, "{campaign_id}", campaignID),
		"pipeline_id":              "runtime-validation-remote", "remote_pipeline_config": remotePipeline,
		"bootstrap_pipeline_config": map[string]any{
			"id": "bootstrap-validation", "name": "Runtime Validation Bootstrap", "services": []any{},
			"pipeline": map[string]any{"build": []any{map[string]any{"name": "Validate", "type": "local_command", "with": map[string]any{"cmd": "echo runtime-validation"}}}},
		},
	}
	return variables, nil
}

func validateGovernanceAttestation(input RuntimeInput) (string, error) {
	raw, err := os.ReadFile(input.GovernanceAttestationPath)
	if err != nil {
		return "", err
	}
	var payload struct {
		RemoteHostID           string `json:"remote_host_id"`
		ExpectedRemoteIdentity string `json:"expected_remote_identity"`
		DedicatedResettable    bool   `json:"dedicated_resettable"`
		IsSelf                 bool   `json:"is_self"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", err
	}
	if payload.RemoteHostID != input.RemoteHostID || payload.ExpectedRemoteIdentity != input.ExpectedRemoteIdentity || !payload.DedicatedResettable || payload.IsSelf {
		return "", fmt.Errorf("borrowed governance attestation identity mismatch")
	}
	digest := DigestBytes(raw)
	return digest, nil
}

// DigestBytes 返回一段非敏感 evidence 的 SHA-256 身份。
func DigestBytes(value []byte) string {
	digest := sha256Bytes(value)
	return hex.EncodeToString(digest)
}

func sha256Bytes(value []byte) []byte {
	// 独立 helper 使 campaign 不暴露原始 governance 内容，报告只保存 digest。
	hash := sha256.New()
	_, _ = hash.Write(value)
	return hash.Sum(nil)
}

func writePreflightSummary(reportRoot string, summary Summary, checks []CheckResult, secret string) (StrictCampaignResult, error) {
	status := StatusBlocked
	cause := Cause{Code: "preflight_blocked", Message: "strict campaign did not enter mutation phase", Source: "preflight"}
	for _, check := range checks {
		if check.Status == StatusFail {
			status, cause = StatusFail, check.Cause
			break
		}
		if check.Status == StatusBlocked && cause.Code == "preflight_blocked" {
			cause = check.Cause
		}
	}
	summary.Checks = checks
	summary.Cleanup = CleanupResult{JournalComplete: true, PipelineTerminal: true, RemoteRootAbsent: true, BorrowedTopologyStable: true, ActiveMarkerRemoved: true}
	summary.Verdict = Verdict{Status: status, RootCause: cause}
	written, err := WriteReport(reportRoot, ReportInput{Summary: summary, Evidence: map[string]any{"preflight.json": checks}, ForbiddenSecrets: []string{secret}})
	if err != nil {
		return StrictCampaignResult{}, err
	}
	return StrictCampaignResult{Summary: written, ReportRoot: reportRoot}, nil
}

func newRuntimeCampaignID(target Target, now time.Time) (string, error) {
	random := make([]byte, 3)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("rv-%s-%s-%s-%s", target.OS, target.Architecture, now.Format("20060102T150405Z"), hex.EncodeToString(random)), nil
}

func allocateCampaignPorts(fixtures []Fixture) (map[string]int, error) {
	result := make(map[string]int, len(fixtures))
	for _, fixture := range fixtures {
		port, err := allocateLoopbackPort()
		if err != nil {
			return nil, err
		}
		result[fixture.Provider] = port
	}
	return result, nil
}

func allocateLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func waitForHTTPReady(ctx context.Context, endpoint string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			response, callErr := client.Do(request)
			if callErr == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64*1024))
				_ = response.Body.Close()
				if response.StatusCode >= 200 && response.StatusCode < 300 {
					return nil
				}
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return fmt.Errorf("HTTP readiness timeout for %s", endpoint)
}

func validateProviderMatrixPass(results []LanguageResult) error {
	if len(results) != 7 {
		return fmt.Errorf("provider matrix returned %d languages, want 7", len(results))
	}
	for _, result := range results {
		if result.RuntimeStatus != StatusPass || result.DebugStatus != StatusPass {
			return fmt.Errorf("provider %s runtime=%s debug=%s", result.Provider, result.RuntimeStatus, result.DebugStatus)
		}
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func failedCampaignCheck(id, code string, err error) CheckResult {
	return CheckResult{ID: id, Status: StatusFail, Cause: Cause{Code: code, Message: err.Error(), Source: id}}
}

type removePathAction struct{ kind, id, path string }

func (a *removePathAction) Kind() string { return a.kind }
func (a *removePathAction) ID() string   { return a.id }
func (a *removePathAction) Release(context.Context) error {
	if err := os.RemoveAll(a.path); err != nil {
		return err
	}
	if _, err := os.Lstat(a.path); !os.IsNotExist(err) {
		return fmt.Errorf("path still exists after cleanup: %s", a.path)
	}
	return nil
}

type processCleanupAction struct {
	kind, id string
	process  Process
}

func (a *processCleanupAction) Kind() string { return a.kind }
func (a *processCleanupAction) ID() string   { return a.id }
func (a *processCleanupAction) Release(ctx context.Context) error {
	if a.process == nil {
		return nil
	}
	return a.process.Close(ctx)
}

type mcpCleanupAction struct {
	id      string
	process *MCPProcess
}

func (a *mcpCleanupAction) Kind() string { return "process" }
func (a *mcpCleanupAction) ID() string   { return a.id }
func (a *mcpCleanupAction) Release(ctx context.Context) error {
	if a.process == nil {
		return nil
	}
	return a.process.Close(ctx)
}

func isWindowsExecutableTarget() bool {
	identity, err := DetectHostIdentity()
	return err == nil && identity.OS == "windows"
}

func pipelineCleanupConfirmed(result ToolCampaignResult) bool {
	normal := []string{"pipeline-cleanup", "pipeline-wait-cleanup", "pipeline-logs-cleanup"}
	abort := []string{"pipeline-cleanup-on-abort", "pipeline-wait-cleanup-on-abort", "pipeline-logs-cleanup-on-abort"}
	return scenarioStepsPassed(result, "remote-pipeline", normal) || scenarioStepsPassed(result, "remote-pipeline", abort)
}

func remoteIdentityConfirmed(result ToolCampaignResult) bool {
	for _, scenario := range result.Scenarios {
		if scenario.ID != "remote-pipeline" {
			continue
		}
		for _, step := range scenario.Steps {
			if step.StepID == "pipeline-host-id-preflight" {
				return step.Status == StatusPass
			}
		}
	}
	return false
}

func scenarioStepsPassed(result ToolCampaignResult, scenarioID string, required []string) bool {
	passed := make(map[string]bool, len(required))
	for _, scenario := range result.Scenarios {
		if scenario.ID != scenarioID {
			continue
		}
		for _, step := range append(append([]ToolStepExecution{}, scenario.Steps...), scenario.Cleanup...) {
			if step.Status == StatusPass {
				passed[step.StepID] = true
			}
		}
	}
	for _, stepID := range required {
		if !passed[stepID] {
			return false
		}
	}
	return true
}
