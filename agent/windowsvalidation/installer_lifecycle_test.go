// installer_lifecycle_test.go 验证四动作普通 JSON fact 与统一结果派生合同。
//
// 职责：
//   - 证明 lifecycle 只持久化 install/start/stop/uninstall 四个独立事实
//   - 覆盖完整、缺失、损坏、乱序、跨绑定、失败和 required evidence 语义
//   - 锁定公开 CLI、stock PowerShell 5.1 helper 与真实 Windows 边界
//
// 边界：
//   - fake helper 只验证跨平台事实合同，不冒充 Windows/UAC 真机证明
//   - 不测试恢复协议或摘要链，因为这些平台能力明确不属于本实现
package windowsvalidation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallerLifecycleCLIHasNoRawFactImportSurface(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "cmd", "windows-validation", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range [][]byte{
		[]byte("--import-installer-lifecycle"),
		[]byte("import-installer-lifecycle"),
		[]byte("lifecycle-fact"),
		[]byte("ImportInstallerLifecycleFact"),
		[]byte("verify-installer-lifecycle-cleanup-allowed"),
	} {
		if bytes.Contains(content, forbidden) {
			t.Fatalf("public lifecycle CLI still exposes removed surface %q", forbidden)
		}
	}
	if !bytes.Contains(content, []byte("execute-installer-lifecycle")) {
		t.Fatal("public lifecycle CLI does not expose the fixed-action request")
	}
}

func TestInstallerLifecycleUsesFourOrdinaryFactsWithoutJournalPlatform(t *testing.T) {
	t.Parallel()
	productionFiles := []string{
		"installer_lifecycle.go",
		"installer_lifecycle_executor_windows.go",
		filepath.Join("..", "cmd", "windows-validation", "main.go"),
		filepath.Join("..", "..", "validation", "windows-real", "internal", "Invoke-InstallerLifecycleAction.ps1"),
	}
	for _, path := range productionFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"InstallerLifecycleLedger", "PreviousEvidenceSHA256", "installerLifecycleWALFilename",
			"reconcileInstallerLifecycle", "ledger.json", ".intent.json", ".launched.json", ".started.json",
			"attempt_marker_path", "action_marker_path", "Write-ImmutableJson",
		} {
			if strings.Contains(string(content), forbidden) {
				t.Fatalf("%s still contains removed lifecycle journal contract %q", path, forbidden)
			}
		}
	}
	for action, filename := range map[InstallerLifecycleAction]string{
		LifecycleActionInstall: "01-install.json", LifecycleActionStart: "02-start.json",
		LifecycleActionStop: "03-stop.json", LifecycleActionUninstall: "04-uninstall.json",
	} {
		if got := installerLifecycleFactFilename(action); got != filename {
			t.Fatalf("ordinary lifecycle fact filename for %s = %q, want %q", action, got, filename)
		}
	}
}

func TestDeriveInstallerLifecycleFactsRequiresCompleteBoundFacts(t *testing.T) {
	t.Parallel()
	for _, lane := range []string{"msi_smoke", "nsis_core"} {
		lane := lane
		t.Run(lane, func(t *testing.T) {
			binding := testInstallerLifecycleBinding(t, lane)
			facts := testInstallerLifecycleFacts(binding)
			got, err := deriveInstallerLifecycleEvidence(facts, binding, successfulLifecycleArtifactInput())
			if err != nil {
				t.Fatal(err)
			}
			if !got.ArtifactVerified || !got.InstallerExecuted || got.Install.PhaseStatus != PhaseStatusPass ||
				got.Start.PhaseStatus != PhaseStatusPass || got.Stop.PhaseStatus != PhaseStatusPass ||
				got.Uninstall.PhaseStatus != PhaseStatusPass || got.Result.PhaseStatus != PhaseStatusPass {
				t.Fatalf("complete lifecycle result = %#v", got)
			}

			partial, err := deriveInstallerLifecycleEvidence(facts[:2], binding, successfulLifecycleArtifactInput())
			if err != nil {
				t.Fatal(err)
			}
			if partial.Install.PhaseStatus != PhaseStatusPass || partial.Start.PhaseStatus != PhaseStatusPass ||
				partial.Stop.PhaseStatus != PhaseStatusNotRun || partial.Uninstall.PhaseStatus != PhaseStatusNotRun ||
				partial.Result.PhaseStatus == PhaseStatusPass {
				t.Fatalf("partial lifecycle was promoted: %#v", partial)
			}
		})
	}
}

func TestInstallerLifecycleFactsFailClosedAcrossOrderBindingAndUnknownFields(t *testing.T) {
	t.Parallel()
	binding := testInstallerLifecycleBinding(t, "msi_smoke")
	facts := testInstallerLifecycleFacts(binding)

	t.Run("out_of_order", func(t *testing.T) {
		mutated := append([]InstallerLifecycleActionEvidence{}, facts...)
		mutated[1], mutated[2] = mutated[2], mutated[1]
		if _, err := deriveInstallerLifecycleEvidence(mutated, binding, successfulLifecycleArtifactInput()); err == nil {
			t.Fatal("out-of-order lifecycle facts were accepted")
		}
	})
	t.Run("cross_binding", func(t *testing.T) {
		other := testInstallerLifecycleBinding(t, "nsis_core")
		if _, err := deriveInstallerLifecycleEvidence(testInstallerLifecycleFacts(other), binding, successfulLifecycleArtifactInput()); err == nil {
			t.Fatal("cross-lane lifecycle facts were accepted")
		}
	})
	t.Run("unknown_legacy_field", func(t *testing.T) {
		backup := t.TempDir()
		directory := filepath.Join(backup, "installer-lifecycle")
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{}
		raw, _ := json.Marshal(facts[0])
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatal(err)
		}
		payload["previous_evidence_sha256"] = strings.Repeat("a", 64)
		if err := writeJSON(filepath.Join(directory, installerLifecycleFactFilename(LifecycleActionInstall)), payload); err != nil {
			t.Fatal(err)
		}
		if _, err := loadVerifiedInstallerLifecycleFacts(backup, binding, successfulLifecycleArtifactInput()); err == nil {
			t.Fatal("legacy digest-chain field was silently accepted")
		}
	})
	t.Run("extra_entry", func(t *testing.T) {
		backup := t.TempDir()
		directory := filepath.Join(backup, "installer-lifecycle")
		writeTestInstallerLifecycleFacts(t, directory, facts[:1])
		if err := os.WriteFile(filepath.Join(directory, "unexpected.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadVerifiedInstallerLifecycleFacts(backup, binding, successfulLifecycleArtifactInput()); err == nil {
			t.Fatal("unsupported lifecycle directory entry was ignored")
		}
	})
	t.Run("missing_prefix", func(t *testing.T) {
		directory := t.TempDir()
		if err := writeInstallerLifecycleJSON(filepath.Join(directory, installerLifecycleFactFilename(LifecycleActionStart)), facts[1]); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectInstallerLifecycleFactFiles(directory); err == nil {
			t.Fatal("lifecycle fact gap was accepted")
		}
	})
}

func TestExecuteInstallerLifecycleWritesOnlyNextOrdinaryFact(t *testing.T) {
	packageRoot, backup, installerPath, installRoot, binding := prepareLifecycleExecutionFixture(t, "msi_smoke")
	calls := 0
	dependencies := installerLifecycleExecutionDependencies{
		verifyPackage:    func(string) error { return nil },
		verifyPreinstall: func(string, string, string, string) error { return nil },
		platformGate:     func() error { return nil },
		isElevated:       func() (bool, error) { return false, nil },
		writeFact:        writeInstallerLifecycleJSON,
		executeHelper: func(_ context.Context, _, requestPath, resultPath string) error {
			calls++
			var request installerLifecycleExecutorRequest
			if err := readStrictInstallerLifecycleJSON(requestPath, &request); err != nil {
				return err
			}
			fact := testInstallerLifecycleFacts(request.Binding)[0]
			result := installerLifecycleExecutorResult{
				SchemaVersion: 1, Kind: installerLifecycleExecutorResultKind, Action: request.Action,
				Attempted: true, Succeeded: true,
				StartedAtUTC: fact.ExecutionFacts.StartedAtUTC, FinishedAtUTC: fact.ExecutionFacts.FinishedAtUTC,
				Command: fact.Command, Observation: fact.Observation,
			}
			return writeInstallerLifecycleJSON(resultPath, result)
		},
	}
	options := InstallerLifecycleExecuteOptions{
		PackageRoot: packageRoot, PreparedBackup: backup, InstallerPath: installerPath,
		InstallDirectory: installRoot, Action: LifecycleActionInstall,
	}
	got, err := executeInstallerLifecycleAction(context.Background(), options, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if got.Install.PhaseStatus != PhaseStatusPass || calls != 1 {
		t.Fatalf("install result=%#v calls=%d", got.Install, calls)
	}
	directory := filepath.Join(backup, "installer-lifecycle")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "01-install.json" {
		t.Fatalf("lifecycle directory contains non-fact platform files: %#v", entries)
	}
	var persisted InstallerLifecycleActionEvidence
	if err := readStrictInstallerLifecycleJSON(filepath.Join(directory, entries[0].Name()), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Binding != binding || !persisted.ExecutionFacts.Attempted {
		t.Fatalf("persisted fact lost binding or attempt: %#v", persisted)
	}
	if _, err := executeInstallerLifecycleAction(context.Background(), options, dependencies); err == nil {
		t.Fatal("repeating an already recorded action was accepted")
	}
	if calls != 1 {
		t.Fatalf("rejected repeat reached helper; calls=%d", calls)
	}
}

func TestInstallerLifecyclePreinstallGateRunsBeforeHelperOrFactWrite(t *testing.T) {
	packageRoot, backup, installerPath, installRoot, binding := prepareLifecycleExecutionFixture(t, "msi_smoke")
	gateCalls, helperCalls, factWrites := 0, 0, 0
	dependencies := installerLifecycleExecutionDependencies{
		verifyPackage: func(string) error { return nil },
		verifyPreinstall: func(gotPackageRoot, gotBackup, campaignID, lane string) error {
			gateCalls++
			if gotPackageRoot != packageRoot || gotBackup != backup || campaignID != binding.CampaignID || lane != binding.Lane {
				t.Fatalf("pre-install gate identity = %q %q %q %q", gotPackageRoot, gotBackup, campaignID, lane)
			}
			return errors.New("prepared pre-install decision is not PASS")
		},
		platformGate: func() error { return nil },
		isElevated:   func() (bool, error) { return false, nil },
		executeHelper: func(context.Context, string, string, string) error {
			helperCalls++
			return nil
		},
		writeFact: func(string, any) error {
			factWrites++
			return nil
		},
	}
	_, err := executeInstallerLifecycleAction(context.Background(), InstallerLifecycleExecuteOptions{
		PackageRoot: packageRoot, PreparedBackup: backup, InstallerPath: installerPath,
		InstallDirectory: installRoot, Action: LifecycleActionInstall,
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "prepared pre-install environment") {
		t.Fatalf("pre-install gate failure = %v", err)
	}
	if gateCalls != 1 || helperCalls != 0 || factWrites != 0 {
		t.Fatalf("gate/helper/fact calls = %d/%d/%d", gateCalls, helperCalls, factWrites)
	}
	if _, statErr := os.Stat(filepath.Join(backup, "installer-lifecycle")); !os.IsNotExist(statErr) {
		t.Fatalf("pre-install gate failure created lifecycle state: %v", statErr)
	}
}

func TestInstallerLifecycleActiveLockRejectsBeforeHelperOrFactWrite(t *testing.T) {
	packageRoot, backup, installerPath, installRoot, _ := prepareLifecycleExecutionFixture(t, "msi_smoke")
	releaseActiveLock, err := acquireInstallerLifecycleLock(filepath.Join(backup, installerLifecycleActiveLockFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseActiveLock()

	helperCalls, factWrites := 0, 0
	dependencies := installerLifecycleExecutionDependencies{
		verifyPackage:    func(string) error { return nil },
		verifyPreinstall: func(string, string, string, string) error { return nil },
		platformGate:     func() error { return nil },
		isElevated:       func() (bool, error) { return false, nil },
		executeHelper: func(context.Context, string, string, string) error {
			helperCalls++
			return nil
		},
		writeFact: func(string, any) error {
			factWrites++
			return nil
		},
	}
	_, err = executeInstallerLifecycleAction(context.Background(), InstallerLifecycleExecuteOptions{
		PackageRoot: packageRoot, PreparedBackup: backup, InstallerPath: installerPath,
		InstallDirectory: installRoot, Action: LifecycleActionInstall,
	}, dependencies)
	if err == nil || !strings.Contains(err.Error(), "helper action is already active") {
		t.Fatalf("active helper lock error = %v", err)
	}
	if helperCalls != 0 || factWrites != 0 {
		t.Fatalf("active helper lock reached helper/fact writer: %d/%d", helperCalls, factWrites)
	}
	if _, statErr := os.Stat(filepath.Join(backup, "installer-lifecycle")); !os.IsNotExist(statErr) {
		t.Fatalf("active helper lock created lifecycle fact state: %v", statErr)
	}
}

func TestInstallerLifecycleActionLockPreventsConcurrentDuplicateSideEffects(t *testing.T) {
	packageRoot, backup, installerPath, installRoot, _ := prepareLifecycleExecutionFixture(t, "msi_smoke")
	enteredHelper := make(chan struct{}, 1)
	releaseHelper := make(chan struct{})
	var helperCalls int32
	dependencies := installerLifecycleExecutionDependencies{
		verifyPackage:    func(string) error { return nil },
		verifyPreinstall: func(string, string, string, string) error { return nil },
		platformGate:     func() error { return nil },
		isElevated:       func() (bool, error) { return false, nil },
		writeFact:        writeInstallerLifecycleJSON,
		executeHelper: func(_ context.Context, _, requestPath, resultPath string) error {
			if atomic.AddInt32(&helperCalls, 1) != 1 {
				return errors.New("concurrent lifecycle helper invocation")
			}
			enteredHelper <- struct{}{}
			<-releaseHelper
			var request installerLifecycleExecutorRequest
			if err := readStrictInstallerLifecycleJSON(requestPath, &request); err != nil {
				return err
			}
			fact := testInstallerLifecycleFacts(request.Binding)[0]
			return writeInstallerLifecycleJSON(resultPath, installerLifecycleExecutorResult{
				SchemaVersion: 1, Kind: installerLifecycleExecutorResultKind, Action: request.Action,
				Attempted: true, Succeeded: true,
				StartedAtUTC: fact.ExecutionFacts.StartedAtUTC, FinishedAtUTC: fact.ExecutionFacts.FinishedAtUTC,
				Command: fact.Command, Observation: fact.Observation,
			})
		},
	}
	options := InstallerLifecycleExecuteOptions{
		PackageRoot: packageRoot, PreparedBackup: backup, InstallerPath: installerPath,
		InstallDirectory: installRoot, Action: LifecycleActionInstall,
	}
	firstResult := make(chan error, 1)
	go func() {
		_, err := executeInstallerLifecycleAction(context.Background(), options, dependencies)
		firstResult <- err
	}()
	<-enteredHelper
	_, concurrentErr := executeInstallerLifecycleAction(context.Background(), options, dependencies)
	close(releaseHelper)
	if firstErr := <-firstResult; firstErr != nil {
		t.Fatalf("single-flight owner failed: %v", firstErr)
	}
	if concurrentErr == nil || !strings.Contains(concurrentErr.Error(), "lock installer lifecycle execution") {
		t.Fatalf("concurrent lifecycle request error = %v", concurrentErr)
	}
	if got := atomic.LoadInt32(&helperCalls); got != 1 {
		t.Fatalf("concurrent requests reached helper %d times", got)
	}
}

func TestUninstallRequestCarriesTheInstallFactRegistryIdentity(t *testing.T) {
	t.Parallel()
	binding := testInstallerLifecycleBinding(t, "msi_smoke")
	facts := testInstallerLifecycleFacts(binding)
	backup := t.TempDir()
	request, err := buildInstallerLifecycleExecutorRequest(InstallerLifecycleExecuteOptions{
		InstallerPath: filepath.Join(t.TempDir(), binding.Artifact.Path), Action: LifecycleActionUninstall,
	}, binding, facts[:3], backup)
	if err != nil {
		t.Fatal(err)
	}
	if request.UninstallEntry == nil || *request.UninstallEntry != facts[0].Observation.UninstallEntries[0] {
		t.Fatalf("uninstall request registry identity = %#v", request.UninstallEntry)
	}
	if request.PreparedBackupDirectory != backup || request.ActiveLockPath != filepath.Join(backup, installerLifecycleActiveLockFilename) {
		t.Fatalf("uninstall request active-lock binding = %q %q", request.PreparedBackupDirectory, request.ActiveLockPath)
	}
}

func TestFailedInstallerFactRemainsFAILAndDependentActionIsBLOCKED(t *testing.T) {
	t.Parallel()
	binding := testInstallerLifecycleBinding(t, "msi_smoke")
	install := testInstallerLifecycleFacts(binding)[0]
	install.ExecutionFacts.Succeeded = false
	install.ExecutionFacts.Failure = "fixed frozen installer invocation failed"
	exit := 1603
	install.Command.ExitCode = &exit
	install.Observation.Checks = failedLifecycleChecks(LifecycleActionInstall)

	blocked, ok := blockedInstallerLifecycleFact(LifecycleActionStart, binding, []InstallerLifecycleActionEvidence{install})
	if !ok {
		t.Fatal("failed install did not block start")
	}
	got, err := deriveInstallerLifecycleEvidence([]InstallerLifecycleActionEvidence{install, blocked}, binding, successfulLifecycleArtifactInput())
	if err != nil {
		t.Fatal(err)
	}
	if got.Install.PhaseStatus != PhaseStatusFail || !got.Install.Attempted ||
		got.Start.PhaseStatus != PhaseStatusBlocked || got.Start.Attempted {
		t.Fatalf("failed/blocked lifecycle facts were misderived: %#v", got)
	}
}

func TestFixedExecutorRejectsUntrustedFailureText(t *testing.T) {
	t.Parallel()
	result := installerLifecycleExecutorResult{
		Action: LifecycleActionInstall, Attempted: true, Succeeded: false,
		FailureCode: "C:\\Users\\operator\\secret-token",
	}
	if _, err := executionFactsFromFixedExecutor(result); err == nil {
		t.Fatal("untrusted helper failure text was accepted")
	}
	result.FailureCode = "installer_invocation_failed"
	facts, err := executionFactsFromFixedExecutor(result)
	if err != nil {
		t.Fatal(err)
	}
	if facts.Failure != "fixed frozen installer invocation failed" {
		t.Fatalf("stable failure mapping=%q", facts.Failure)
	}
}

func TestInstallerLifecycleBindingAnchorsPreparedBackupAndBaseline(t *testing.T) {
	t.Parallel()
	_, backup, _, _, binding := prepareLifecycleExecutionFixture(t, "nsis_core")
	var prepared preparedBackupManifest
	if err := readJSONFile(filepath.Join(backup, "backup-manifest.json"), &prepared); err != nil {
		t.Fatal(err)
	}
	if binding.PreparedBackupSHA256 != preparedBackupIdentitySHA256(prepared) ||
		binding.PreparedBaselineSHA256 != prepared.BaselineSHA256 {
		t.Fatalf("binding does not anchor prepared backup: %#v", binding)
	}
}

func TestVerifyInstalledMCPIdentityRejectsDecoyPath(t *testing.T) {
	t.Parallel()
	binding := testInstallerLifecycleBinding(t, "nsis_core")
	if err := os.MkdirAll(binding.InstallDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	mcpPath := filepath.Join(binding.InstallDirectory, "superdev-mcp.exe")
	if err := os.WriteFile(mcpPath, []byte("installed-mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	identity, err := fileIdentity(binding.InstallDirectory, mcpPath)
	if err != nil {
		t.Fatal(err)
	}
	install := testInstallerLifecycleFacts(binding)[0]
	install.Observation.SidecarFiles = []PackageFileIdentity{identity}
	if err := verifyInstalledMCPIdentity(mcpPath, []InstallerLifecycleActionEvidence{install}); err != nil {
		t.Fatal(err)
	}
	decoy := filepath.Join(t.TempDir(), "superdev-mcp.exe")
	if err := os.WriteFile(decoy, []byte("installed-mcp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstalledMCPIdentity(decoy, []InstallerLifecycleActionEvidence{install}); err == nil {
		t.Fatal("same-content MCP outside the lifecycle-bound install root was accepted")
	}
}

func TestWindowsLifecycleExecutorUsesStockPowerShellWithoutRecoveryProtocol(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile("installer_lifecycle_executor_windows.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"exec.CommandContext", "command.Process.Kill", "taskkill", "exec.LookPath",
		"installerLifecycleHelperActive", "OpenProcess(", "QueryFullProcessImageName",
	} {
		if strings.Contains(string(content), forbidden) {
			t.Fatalf("Windows lifecycle executor retains removed protocol %q", forbidden)
		}
	}
	for _, required := range []string{
		"ValidateWindows10ValidationPlatform",
		"windows.GetWindowsDirectory()",
		`filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")`,
		"exec.Command(powerShellPath",
	} {
		if !strings.Contains(string(content), required) {
			t.Fatalf("Windows lifecycle executor is missing stock PowerShell contract %q", required)
		}
	}
}

func prepareLifecycleExecutionFixture(t *testing.T, lane string) (string, string, string, string, InstallerLifecycleBinding) {
	t.Helper()
	sourceRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	packageRoot := filepath.Join(t.TempDir(), "windows-real")
	if err := copyTree(sourceRoot, packageRoot); err != nil {
		t.Fatal(err)
	}
	var frozen FrozenBuild
	manifestPath := filepath.Join(packageRoot, "manifest", "frozen-build.json")
	if err := readJSONFile(manifestPath, &frozen); err != nil {
		t.Fatal(err)
	}
	formatName, filename := "nsis", "ticket31-test-setup.exe"
	if lane == "msi_smoke" {
		formatName, filename = "msi", "ticket31-test.msi"
	}
	installerDirectory := t.TempDir()
	installerPath := filepath.Join(installerDirectory, filename)
	if err := os.WriteFile(installerPath, []byte("ticket31-installer-artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
	identity, err := fileIdentity(installerDirectory, installerPath)
	if err != nil {
		t.Fatal(err)
	}
	for index := range frozen.Installers {
		if frozen.Installers[index].Format == formatName {
			frozen.Installers[index].Filename = filename
			frozen.Installers[index].SizeBytes = identity.SizeBytes
			frozen.Installers[index].SHA256 = identity.SHA256
		}
	}
	if err := writeJSON(manifestPath, frozen); err != nil {
		t.Fatal(err)
	}
	campaignID := "w10x64-abcdef0-20260715T010203Z-abcdef"
	cleanup := CleanupRecord{}
	backup := bindCleanupToPreparedBackup(t, t.TempDir(), campaignID, lane, &cleanup)
	var prepared preparedBackupManifest
	if err := readJSONFile(filepath.Join(backup, "backup-manifest.json"), &prepared); err != nil {
		t.Fatal(err)
	}
	installRoot := filepath.Join(t.TempDir(), "SuperDev")
	binding, err := installerLifecycleBinding(frozen, prepared, []PackageFileIdentity{identity}, installRoot)
	if err != nil {
		t.Fatal(err)
	}
	return packageRoot, backup, installerPath, installRoot, binding
}

func successfulLifecycleArtifactInput() ResultInput {
	started := time.Now().UTC().Add(-2 * time.Second)
	return ResultInput{
		Facts: ExecutionFacts{
			Attempted: true, Succeeded: true,
			StartedAtUTC: started.Format(time.RFC3339Nano), FinishedAtUTC: started.Add(time.Second).Format(time.RFC3339Nano),
		},
		Evidence: []EvidenceRecord{{Name: "installer_identity", Required: true, Present: true, Ref: "campaign-report.json#installer_checks"}},
	}
}

func testInstallerLifecycleBinding(t *testing.T, lane string) InstallerLifecycleBinding {
	t.Helper()
	formatName := installerFormatForLane(lane)
	artifact := PackageFileIdentity{Path: "SuperDev_0.2.1_x64-setup.exe", SizeBytes: 22, SHA256: strings.Repeat("b", 64)}
	if lane == "msi_smoke" {
		artifact = PackageFileIdentity{Path: "SuperDev_0.2.1_x64_en-US.msi", SizeBytes: 11, SHA256: strings.Repeat("a", 64)}
	}
	installRoot, digest, err := normalizeInstallDirectory(filepath.Join(t.TempDir(), "SuperDev"))
	if err != nil {
		t.Fatal(err)
	}
	return InstallerLifecycleBinding{
		CampaignID: "w10x64-abcdef0-20260715T010203Z-abcdef", Lane: lane, Format: formatName,
		UninstallerFilename: fixedUninstallerFilename(formatName), BuildCommit: "abcdef0123456789", ProductVersion: "0.2.1",
		PreparedBackupSHA256: strings.Repeat("d", 64), PreparedBaselineSHA256: strings.Repeat("c", 64),
		InstallDirectory: installRoot, InstallDirectorySHA256: digest, Artifact: artifact,
	}
}

func testInstallerLifecycleFacts(binding InstallerLifecycleBinding) []InstallerLifecycleActionEvidence {
	product := PackageFileIdentity{Path: "SuperDev.exe", SizeBytes: 10, SHA256: strings.Repeat("1", 64)}
	agent := PackageFileIdentity{Path: "superdev-agent.exe", SizeBytes: 11, SHA256: strings.Repeat("2", 64)}
	sidecars := []PackageFileIdentity{
		agent,
		{Path: "superdev-mcp.exe", SizeBytes: 12, SHA256: strings.Repeat("3", 64)},
		{Path: "superdev-sample.exe", SizeBytes: 13, SHA256: strings.Repeat("4", 64)},
	}
	uninstaller := PackageFileIdentity{Path: "uninstall.exe", SizeBytes: 14, SHA256: strings.Repeat("5", 64)}
	started := time.Now().UTC().Add(-20 * time.Second)
	result := make([]InstallerLifecycleActionEvidence, 0, len(installerLifecycleActionOrder))
	for index, action := range installerLifecycleActionOrder {
		zero := 0
		fact := InstallerLifecycleActionEvidence{
			SchemaVersion: 1, Kind: InstallerLifecycleActionFactKind, Action: action, Binding: binding,
			ExecutionFacts: ExecutionFacts{
				Attempted: true, Succeeded: true,
				StartedAtUTC:  started.Add(time.Duration(index*2) * time.Second).Format(time.RFC3339Nano),
				FinishedAtUTC: started.Add(time.Duration(index*2+1) * time.Second).Format(time.RFC3339Nano),
			},
			Command:     InstallerLifecycleCommandFact{Operation: action},
			Observation: InstallerLifecycleObservation{Checks: successfulLifecycleChecks(action)},
		}
		switch action {
		case LifecycleActionInstall:
			fact.Command.Method, fact.Command.Target, fact.Command.ExitCode = "start_process_wait_elevated", binding.Artifact, &zero
			if binding.Format == "msi" {
				fact.Command.Executable, fact.Command.ProductCode = "msiexec.exe", "{11111111-2222-3333-4444-555555555555}"
			} else {
				fact.Command.Executable = binding.Artifact.Path
				fact.Observation.UninstallerFile = &uninstaller
			}
			present := true
			fact.Observation.InstallPathPresent = &present
			fact.Observation.ProductFiles = []PackageFileIdentity{product}
			fact.Observation.SidecarFiles = sidecars
			entryKey, uninstallExecutable := "superdev-nsis", filepath.Join(binding.InstallDirectory, "uninstall.exe")
			if binding.Format == "msi" {
				entryKey, uninstallExecutable = fact.Command.ProductCode, "msiexec.exe"
			}
			fact.Observation.UninstallEntries = []InstallerLifecycleUninstallIdentity{{
				Scope: "HKCU", Key: entryKey, DisplayName: "SuperDev", DisplayVersion: binding.ProductVersion,
				InstallLocation: binding.InstallDirectory, UninstallExecutable: uninstallExecutable,
				UninstallStringSHA256: strings.Repeat("6", 64),
			}}
		case LifecycleActionStart:
			fact.Command.Method, fact.Command.Executable, fact.Command.Target, fact.Command.ProcessIDs = "start_process", "SuperDev.exe", product, []int{42}
			fact.Observation.Processes = []InstallerLifecycleProcessIdentity{
				{Role: "desktop", ProcessID: 42, Executable: product},
				{Role: "agent", ProcessID: 43, ParentProcessID: 42, Executable: agent},
			}
			fact.Observation.Port57017 = &InstallerLifecyclePortIdentity{Port: 57017, Listening: true, OwningProcessID: 43}
		case LifecycleActionStop:
			fact.Command.Method, fact.Command.Executable, fact.Command.Target, fact.Command.ProcessIDs = "close_main_window", "SuperDev.exe", product, []int{42}
			fact.Observation.Port57017 = &InstallerLifecyclePortIdentity{Port: 57017, Listening: false}
			fact.Observation.RemainingBoundProcessIDs = []int{}
		case LifecycleActionUninstall:
			fact.Command.Method, fact.Command.ExitCode = "start_process_wait_elevated", &zero
			if binding.Format == "msi" {
				fact.Command.Executable, fact.Command.Target, fact.Command.ProductCode = "msiexec.exe", binding.Artifact, "{11111111-2222-3333-4444-555555555555}"
			} else {
				fact.Command.Executable, fact.Command.Target = "uninstall.exe", uninstaller
			}
			absent := false
			fact.Observation.InstallPathPresent = &absent
			fact.Observation.ProductFiles = []PackageFileIdentity{}
			fact.Observation.SidecarFiles = []PackageFileIdentity{}
			fact.Observation.UninstallEntries = []InstallerLifecycleUninstallIdentity{}
		}
		result = append(result, fact)
	}
	return result
}

func writeTestInstallerLifecycleFacts(t *testing.T, directory string, facts []InstallerLifecycleActionEvidence) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, fact := range facts {
		if err := writeInstallerLifecycleJSON(filepath.Join(directory, installerLifecycleFactFilename(fact.Action)), fact); err != nil {
			t.Fatal(err)
		}
	}
}

func successfulLifecycleChecks(action InstallerLifecycleAction) []InstallerLifecycleStateCheck {
	checks := make([]InstallerLifecycleStateCheck, 0, len(installerLifecycleRequiredChecks[action]))
	for _, name := range installerLifecycleRequiredChecks[action] {
		checks = append(checks, InstallerLifecycleStateCheck{Name: name, Matched: true})
	}
	return checks
}

func failedLifecycleChecks(action InstallerLifecycleAction) []InstallerLifecycleStateCheck {
	checks := make([]InstallerLifecycleStateCheck, 0, len(installerLifecycleRequiredChecks[action]))
	for _, name := range installerLifecycleRequiredChecks[action] {
		checks = append(checks, InstallerLifecycleStateCheck{Name: name, Matched: false})
	}
	return checks
}
