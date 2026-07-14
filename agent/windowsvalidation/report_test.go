// report_test.go 验证 cleanup 最终化与 MSI/NSIS 聚合摘要合同。
//
// 职责：
//   - 防止功能 PASS 在 cleanup 前被误报为最终 PASS
//   - 防止最终摘要遗漏独立 MSI lane、七 provider 或 75 工具
//
// 边界：
//   - 不启动 Windows 进程或真实 MCP
//   - 只使用临时结果目录验证固定报告状态转换
package windowsvalidation

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalizeCampaignCleanupRebuildsCompleteAggregateSummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	redactor := NewRedactor()
	pass := testPassResult()
	validationCatalog, scenarioExecutions, toolRows, toolNames := testValidationSurface(pass)
	msiID := "w10x64-e3cc94f-20260713T010101Z-a1b2c3"
	msi := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: msiID,
		Result: pass, Functional: pass, BuildCommit: "e3cc94f", Lane: "msi_smoke", Installer: testCompleteInstaller(t, "msi"),
		RuntimeAttestation: RuntimeAttestation{Result: pass}, Operations: []StepExecution{testStopOperation()},
		InstallerChecks:   []PackageFileIdentity{{Path: "frozen.msi"}},
		ValidationCatalog: validationCatalog, Scenarios: scenarioExecutions, ToolRows: toolRows,
		Cleanup: completedCleanupFacts(true, "", "2026-07-13T01:01:01Z"), FinishedAtUTC: "2026-07-13T01:01:01Z",
	}
	msi.Sections = buildReportSections(msi)
	if err := writeCampaignReports(filepath.Join(root, msiID), redactor, msi); err != nil {
		t.Fatal(err)
	}

	nsisID := "w10x64-e3cc94f-20260713T020202Z-d4e5f6"
	providers := make([]ProviderExecution, 7)
	providerNames := make([]string, 7)
	for index := range providers {
		providerNames[index] = fmt.Sprintf("provider-%02d", index)
		providers[index] = ProviderExecution{Provider: providerNames[index], Result: pass, Runtime: pass, Debug: pass}
	}
	nsis := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: nsisID,
		Result: notRunResult("cleanup pending"), Functional: pass, BuildCommit: "e3cc94f", Lane: "nsis_core", Installer: testCompleteInstaller(t, "nsis"),
		RuntimeAttestation: RuntimeAttestation{Result: pass, ToolNames: toolNames, ProviderNames: providerNames}, Operations: []StepExecution{testStopOperation()},
		InstallerChecks: []PackageFileIdentity{{Path: "frozen.exe"}}, Providers: providers, ToolRows: toolRows,
		ValidationCatalog: validationCatalog, Scenarios: scenarioExecutions,
		Cleanup: pendingCleanupRecord("cleanup pending", ""), FinishedAtUTC: "2026-07-13T02:02:02Z",
	}
	nsis.Sections = buildReportSections(nsis)
	nsisDir := filepath.Join(root, nsisID)
	if err := writeCampaignReports(nsisDir, redactor, nsis); err != nil {
		t.Fatal(err)
	}
	cleanupPath := filepath.Join(nsisDir, "cleanup-report.json")
	cleanup := completedCleanupFacts(true, "", "2026-07-13T02:03:03Z")
	preparedBackup := bindCleanupToPreparedBackup(t, root, nsisID, "nsis_core", &cleanup)
	cleanup.SchemaVersion, cleanup.Kind, cleanup.CampaignID = 2, "superdev.windows-validation.cleanup-report", nsisID
	if err := writeJSON(cleanupPath, RawMessageMap(cleanup)); err != nil {
		t.Fatal(err)
	}
	final, err := FinalizeCampaignCleanup(root, nsisID, cleanupPath, preparedBackup)
	if err != nil {
		t.Fatal(err)
	}
	if final.Result.PhaseStatus != PhaseStatusPass {
		t.Fatalf("final campaign status=%s, want PASS", final.Result.PhaseStatus)
	}
	var summary CampaignReport
	if err := readJSONFile(filepath.Join(root, "validation-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Result.PhaseStatus != PhaseStatusPass || summary.Sections.MSIInstaller.Result.PhaseStatus != PhaseStatusPass || summary.Sections.Cleanup.Result.PhaseStatus != PhaseStatusPass {
		t.Fatalf("aggregate summary is incomplete: %#v", summary.Sections)
	}
	if len(summary.Providers) != 7 || len(summary.ToolRows) != 75 {
		t.Fatalf("aggregate rows providers=%d tools=%d", len(summary.Providers), len(summary.ToolRows))
	}
}

func TestFinalizeCampaignCleanupCannotPreserveOldPassAfterCleanupFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260713T030303Z-aabbcc"
	directory := filepath.Join(root, campaignID)
	report := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Result: testPassResult(), Functional: testPassResult(), BuildCommit: "e3cc94f", Lane: "msi_smoke", Installer: testCompleteInstaller(t, "msi"),
		RuntimeAttestation: RuntimeAttestation{Result: testPassResult()}, Operations: []StepExecution{testStopOperation()},
		InstallerChecks: []PackageFileIdentity{{Path: "frozen.msi"}},
		Cleanup:         completedCleanupFacts(true, "", "2026-07-13T03:03:03Z"), FinishedAtUTC: "2026-07-13T03:03:03Z",
	}
	report.ValidationCatalog, report.Scenarios, report.ToolRows, _ = testValidationSurface(notRunResult("independent MSI lane"))
	report.Sections = buildReportSections(report)
	if err := writeCampaignReports(directory, NewRedactor(), report); err != nil {
		t.Fatal(err)
	}
	cleanupPath := filepath.Join(directory, "cleanup-report.json")
	cleanup := completedCleanupFacts(false, "baseline drift", "2026-07-13T03:04:04Z")
	preparedBackup := bindCleanupToPreparedBackup(t, root, campaignID, "msi_smoke", &cleanup)
	baselinePath := filepath.Join(preparedBackup, "baseline.json")
	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	// campaign/lane 身份仍正确，但已绑定的 baseline 完整性失败必须进入统一 FAIL，不得让 finalizer 中断在 pending。
	if err := os.WriteFile(baselinePath, append(baselineBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanup.SchemaVersion, cleanup.Kind, cleanup.CampaignID, cleanup.Error = 2, "superdev.windows-validation.cleanup-report", campaignID, "baseline drift"
	if err := writeJSON(cleanupPath, RawMessageMap(cleanup)); err != nil {
		t.Fatal(err)
	}
	final, err := FinalizeCampaignCleanup(root, campaignID, cleanupPath, preparedBackup)
	if err != nil {
		t.Fatal(err)
	}
	if final.Result.PhaseStatus != PhaseStatusFail || final.Sections.Cleanup.Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("cleanup failure retained stale PASS: status=%s cleanup=%s", final.Result.PhaseStatus, final.Sections.Cleanup.Result.PhaseStatus)
	}
	verification, found := stepExecutionByID(final.Prerequisites, "prepared-baseline-verification")
	if !found || verification.Result.PhaseStatus != PhaseStatusFail || !verification.Result.Attempted {
		t.Fatalf("baseline integrity failure was not persisted as an independent attempted fact: %#v", final.Prerequisites)
	}
	var summary CampaignReport
	if err := readJSONFile(filepath.Join(root, "validation-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("summary status=%s, want FAIL", summary.Result.PhaseStatus)
	}
}

func stepExecutionByID(steps []StepExecution, stepID string) (StepExecution, bool) {
	for _, step := range steps {
		if step.StepID == stepID {
			return step, true
		}
	}
	return StepExecution{}, false
}

func TestFinalizeCampaignCleanupRederivesTamperedPersistedStatus(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260713T040404Z-ddeeff"
	directory := filepath.Join(root, campaignID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	pass := testPassResult()
	validationCatalog, scenarios, rows, toolNames := testValidationSurface(pass)
	// 模拟持久化文件被旧代码手工改成 PASS；attempted/succeeded/failure 原始事实仍明确失败。
	rows[0].Result = attemptedResult(false, "real tool call failed", resultTestTime, resultTestTime, nil)
	rows[0].Result.PhaseStatus = PhaseStatusPass
	providerNames := make([]string, 7)
	providers := make([]ProviderExecution, 7)
	for index := range providers {
		providerNames[index] = fmt.Sprintf("provider-%02d", index)
		providers[index] = ProviderExecution{Provider: providerNames[index], Runtime: pass, Debug: pass, Result: pass}
	}
	report := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Result: pass, Functional: pass, BuildCommit: "e3cc94f", Lane: "nsis_core", Installer: testCompleteInstaller(t, "nsis"),
		RuntimeAttestation: RuntimeAttestation{Result: pass, ToolNames: toolNames, ProviderNames: providerNames},
		InstallerChecks:    []PackageFileIdentity{{Path: "frozen.exe"}}, Operations: []StepExecution{testStopOperation()},
		Providers: providers, ToolRows: rows,
		ValidationCatalog: validationCatalog, Scenarios: scenarios,
		Cleanup: pendingCleanupRecord("cleanup pending", ""), FinishedAtUTC: resultTestTime,
	}
	if err := writeJSON(filepath.Join(directory, "campaign-report.json"), RawMessageMap(report)); err != nil {
		t.Fatal(err)
	}
	cleanupPath := filepath.Join(directory, "cleanup-report.json")
	cleanup := completedCleanupFacts(true, "", resultTestTime)
	preparedBackup := bindCleanupToPreparedBackup(t, root, campaignID, "nsis_core", &cleanup)
	cleanup.SchemaVersion, cleanup.Kind, cleanup.CampaignID = 2, "superdev.windows-validation.cleanup-report", campaignID
	if err := writeJSON(cleanupPath, RawMessageMap(cleanup)); err != nil {
		t.Fatal(err)
	}
	final, err := FinalizeCampaignCleanup(root, campaignID, cleanupPath, preparedBackup)
	if err != nil {
		t.Fatal(err)
	}
	if final.ToolRows[0].Result.PhaseStatus != PhaseStatusFail || final.Functional.PhaseStatus != PhaseStatusFail || final.Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("persisted manual PASS survived fact re-derivation: tool=%#v functional=%#v result=%#v", final.ToolRows[0].Result, final.Functional, final.Result)
	}
}

func TestDeriveCleanupRejectsSucceededFactWithBaselineDrift(t *testing.T) {
	t.Parallel()
	cleanup := completedCleanupFacts(true, "", resultTestTime)
	cleanup.BaselineComparison.Matched = false
	cleanup.BaselineComparison.Checks[0].Matched = false
	if _, err := deriveCleanupResult(cleanup); err == nil {
		t.Fatal("cleanup succeeded fact contradicted baseline drift but was accepted")
	}
}

func TestDeriveCleanupRequiresAllUniqueHashedBaselineCategories(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*CleanupRecord)
	}{
		{name: "missing", mutate: func(cleanup *CleanupRecord) {
			cleanup.BaselineComparison.Checks = cleanup.BaselineComparison.Checks[:len(cleanup.BaselineComparison.Checks)-1]
		}},
		{name: "duplicate", mutate: func(cleanup *CleanupRecord) {
			cleanup.BaselineComparison.Checks[len(cleanup.BaselineComparison.Checks)-1] = cleanup.BaselineComparison.Checks[0]
		}},
		{name: "unhashed", mutate: func(cleanup *CleanupRecord) {
			cleanup.BaselineComparison.Checks[0].ExpectedSHA256 = ""
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			cleanup := completedCleanupFacts(true, "", resultTestTime)
			test.mutate(&cleanup)
			if _, err := deriveCleanupResult(cleanup); err == nil {
				t.Fatalf("successful cleanup with %s baseline contract was accepted", test.name)
			}
		})
	}
}

func TestVerifyPreparedBaselineRejectsSelfDeclaredCleanupExpectedHash(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260713T060606Z-a1b2c3"
	cleanup := completedCleanupFacts(true, "", resultTestTime)
	backup := bindCleanupToPreparedBackup(t, root, campaignID, "nsis_core", &cleanup)
	if _, _, err := loadPreparedBackupIdentity(backup, campaignID, "msi_smoke"); err == nil {
		t.Fatal("prepared backup lane mismatch must remain a hard identity rejection")
	}
	forged := strings.Repeat("f", 64)
	category := cleanup.BaselineComparison.Checks[0].Category
	cleanup.BaselineComparison.Checks[0].ExpectedSHA256 = forged
	cleanup.BaselineComparison.Checks[0].ActualSHA256 = forged
	var manifest preparedBackupManifest
	manifestPath := filepath.Join(backup, "backup-manifest.json")
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.BaselineCategorySHA256[category] = forged
	if err := writeJSON(manifestPath, RawMessageMap(manifest)); err != nil {
		t.Fatal(err)
	}
	if _, err := deriveCleanupResult(cleanup); err != nil {
		t.Fatalf("forged manifest/report pair is internally consistent; baseline binding must reject it later: %v", err)
	}
	if err := verifyPreparedBaseline(backup, campaignID, "nsis_core", cleanup); err == nil {
		t.Fatal("manifest and cleanup category hashes not derived from baseline.json survived finalizer binding")
	}
}

func TestRederiveCampaignReportRejectsDeletedOrRemappedFrozenFacts(t *testing.T) {
	t.Parallel()
	pass := testPassResult()
	catalog, scenarios, rows, toolNames := testValidationSurface(pass)
	report := CampaignReport{
		SchemaVersion: 2, Kind: "superdev.windows-validation.campaign-report",
		CampaignID: "w10x64-e3cc94f-20260713T050505Z-a1b2c3", Lane: "core_only",
		Installer: testCompleteInstaller(t, "nsis"), RuntimeAttestation: RuntimeAttestation{Result: pass, ToolNames: toolNames},
		ValidationCatalog: catalog, Scenarios: scenarios, ToolRows: rows,
		Cleanup: pendingCleanupRecord("cleanup pending", ""),
	}

	tests := []struct {
		name   string
		mutate func(*CampaignReport)
	}{
		{name: "scenario", mutate: func(candidate *CampaignReport) { candidate.Scenarios = nil }},
		{name: "supporting cleanup step", mutate: func(candidate *CampaignReport) { candidate.Scenarios[0].Cleanup = nil }},
		{name: "tool mapping", mutate: func(candidate *CampaignReport) { candidate.ToolRows[0].StepID = "different-step" }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			candidate := report
			candidate.Scenarios = append([]ScenarioExecution{}, report.Scenarios...)
			candidate.Scenarios[0].Cleanup = append([]StepExecution{}, report.Scenarios[0].Cleanup...)
			candidate.ToolRows = append([]ToolEvidenceRow{}, report.ToolRows...)
			test.mutate(&candidate)
			if _, err := rederiveCampaignReport(candidate); err == nil {
				t.Fatalf("deleted/remapped %s survived frozen report derivation", test.name)
			}
		})
	}
}

func testPassResult() ValidationResult {
	return deriveKnown(successfulResultInput())
}

func testStopOperation() StepExecution {
	return StepExecution{StepID: "packaged-mcp-stop", Tool: "process_close", Coverage: CoverageSupporting, Result: testPassResult()}
}

func completedCleanupFacts(succeeded bool, failure, finishedAtUTC string) CleanupRecord {
	facts := ExecutionFacts{
		Attempted: true, Succeeded: succeeded, Failure: failure,
		StartedAtUTC: "2026-07-13T00:00:00Z", FinishedAtUTC: finishedAtUTC,
	}
	return CleanupRecord{
		ExecutionFacts: &facts, FinishedAtUTC: finishedAtUTC,
		PreparedBaselineSHA256:   strings.Repeat("a", 64),
		CampaignWorkspaceRemoved: true, UserStateRestored: true, ValidationStateQuarantineRemoved: true,
		BaselineComparison: &CleanupBaselineComparison{Matched: true, Checks: testCleanupBaselineChecks()},
	}
}

func testCleanupBaselineChecks() []CleanupBaselineCheck {
	checks := make([]CleanupBaselineCheck, 0, len(cleanupBaselineCategories))
	for index, category := range cleanupBaselineCategories {
		digest := fmt.Sprintf("%064x", index+1)
		checks = append(checks, CleanupBaselineCheck{Category: category, Matched: true, ExpectedSHA256: digest, ActualSHA256: digest})
	}
	return checks
}

func testCleanupCategoryHashMap() map[string]string {
	hashes := make(map[string]string, len(cleanupBaselineCategories))
	for _, check := range testCleanupBaselineChecks() {
		hashes[check.Category] = check.ExpectedSHA256
	}
	return hashes
}

func testValidationSurface(result ValidationResult) (ValidationCatalog, []ScenarioExecution, []ToolEvidenceRow, []string) {
	catalog := ValidationCatalog{Scenarios: []ScenarioCatalogEntry{{ID: "remote-pipeline", Title: "remote pipeline"}}}
	execution := ScenarioExecution{ID: "remote-pipeline", Title: "remote pipeline"}
	rows := make([]ToolEvidenceRow, 0, 75)
	toolNames := make([]string, 0, 75)
	for index := 0; index < 75; index++ {
		tool := fmt.Sprintf("tool-%02d", index)
		stepID := fmt.Sprintf("step-%02d", index)
		contract := StepCatalogEntry{StepID: stepID, Tool: tool, Coverage: CoveragePrimary}
		catalog.Scenarios[0].Steps = append(catalog.Scenarios[0].Steps, contract)
		catalog.Coverage = append(catalog.Coverage, CoverageAssignment{Tool: tool, ScenarioID: "remote-pipeline", StepID: stepID})
		execution.Steps = append(execution.Steps, StepExecution{StepID: stepID, Tool: tool, Coverage: CoveragePrimary, Result: result})
		rows = append(rows, ToolEvidenceRow{Tool: tool, ScenarioID: "remote-pipeline", StepID: stepID, Result: result})
		toolNames = append(toolNames, tool)
	}
	cleanupContract := StepCatalogEntry{StepID: "cleanup-supporting", Tool: "pipeline_cleanup", Coverage: CoverageSupporting}
	catalog.Scenarios[0].Cleanup = []StepCatalogEntry{cleanupContract}
	execution.Cleanup = []StepExecution{{StepID: cleanupContract.StepID, Tool: cleanupContract.Tool, Coverage: cleanupContract.Coverage, Result: notRunResult("guard not required")}}
	execution.Result = aggregateResult("remote-pipeline scenario", len(execution.Steps), stepResults(execution.Steps))
	return catalog, []ScenarioExecution{execution}, rows, toolNames
}

func stepResults(steps []StepExecution) []ValidationResult {
	results := make([]ValidationResult, 0, len(steps))
	for _, step := range steps {
		results = append(results, step.Result)
	}
	return results
}

func bindCleanupToPreparedBackup(t *testing.T, root, campaignID, lane string, cleanup *CleanupRecord) string {
	t.Helper()
	backup := filepath.Join(root, "prepared-"+campaignID)
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	baseline := map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.machine-baseline",
		"superdev_processes": []any{}, "listening_port_57017": []any{}, "install_paths": []any{},
		"uninstall_entries": []any{}, "connector_files": []any{}, "user_state": map[string]any{"present": false, "files": []any{}},
	}
	baselinePath := filepath.Join(backup, "baseline.json")
	if err := writeJSON(baselinePath, baseline); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	cleanup.PreparedBaselineSHA256 = digest
	categoryHashes, err := preparedBaselineCategoryDigests(content)
	if err != nil {
		t.Fatal(err)
	}
	if cleanup.BaselineComparison != nil {
		for index := range cleanup.BaselineComparison.Checks {
			category := cleanup.BaselineComparison.Checks[index].Category
			cleanup.BaselineComparison.Checks[index].ExpectedSHA256 = categoryHashes[category]
			cleanup.BaselineComparison.Checks[index].ActualSHA256 = categoryHashes[category]
		}
	}
	manifest := preparedBackupManifest{
		SchemaVersion: 1, Kind: "superdev.windows-validation.prepared-backup", Status: "ready",
		CreatedAtUTC: resultTestTime, Lane: lane, CampaignID: campaignID,
		BaselineSHA256: digest, BaselineCategorySHA256: categoryHashes,
	}
	if err := writeJSON(filepath.Join(backup, "backup-manifest.json"), RawMessageMap(manifest)); err != nil {
		t.Fatal(err)
	}
	return backup
}

func testCompleteInstaller(t *testing.T, format string) InstallerExecution {
	t.Helper()
	pass := successfulResultInput()
	installer, err := DeriveInstallerExecution(InstallerExecutionFacts{
		Format: format, ArtifactVerified: true, InstallerExecuted: true,
		Artifact: pass, Install: pass, Start: pass, Stop: pass, Uninstall: pass,
	})
	if err != nil {
		t.Fatal(err)
	}
	return installer
}
