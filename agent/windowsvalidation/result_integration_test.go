// result_integration_test.go 验证统一结果合同已接管 scenario、工具行与报告 section。
//
// 职责：
//   - 防止 core_only 把安装包身份验证提升为 installer lifecycle PASS
//   - 防止具名前置失败把未调用的 pipeline 主工具合成为 FAIL
//   - 防止普通未到达步骤被伪装成具名 BLOCKED
//
// 边界：
//   - 不启动真实 MCP、安装器或 provider 进程
//   - 不替代 Windows 10 22H2 x64 (build 19045) 真机验收
package windowsvalidation

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildReportSectionsKeepsCoreOnlyInstallerLifecycleNotRun(t *testing.T) {
	t.Parallel()
	notRun := ResultInput{Facts: ExecutionFacts{NotRunReason: "core_only excludes installer lifecycle"}}
	installer, err := DeriveInstallerExecution(InstallerExecutionFacts{
		Format: "nsis", ArtifactVerified: true, InstallerExecuted: false,
		Artifact: successfulResultInput(), Install: notRun, Start: notRun, Stop: notRun, Uninstall: notRun,
	})
	if err != nil {
		t.Fatal(err)
	}
	pass := testPassResult()
	validationCatalog, scenarios, rows, toolNames := testValidationSurface(pass)
	providerNames := make([]string, 7)
	providers := make([]ProviderExecution, 7)
	for index := range providers {
		providerNames[index] = fmt.Sprintf("provider-%02d", index)
		providers[index] = ProviderExecution{Provider: providerNames[index], Runtime: pass, Debug: pass, Result: pass}
	}
	report := CampaignReport{
		SchemaVersion: 2,
		Kind:          "superdev.windows-validation.campaign-report",
		Lane:          "core_only",
		Installer:     installer,
		Functional:    pass,
		RuntimeAttestation: RuntimeAttestation{
			Result: pass, ToolNames: toolNames, ProviderNames: providerNames,
		},
		ToolRows:          rows,
		Providers:         providers,
		ValidationCatalog: validationCatalog,
		Scenarios:         scenarios,
		Cleanup:           pendingCleanupRecord("cleanup pending", ""),
	}
	sections := buildReportSections(report)
	if !report.Installer.ArtifactVerified || report.Installer.Artifact.PhaseStatus != PhaseStatusPass {
		t.Fatalf("artifact identity was lost: %#v", report.Installer)
	}
	if sections.MSIInstaller.Result.PhaseStatus != PhaseStatusNotRun || sections.NSISInstaller.Result.PhaseStatus != PhaseStatusNotRun {
		t.Fatalf("core_only synthesized installer verdict: %#v", sections)
	}
	if sections.Core.Result.PhaseStatus != PhaseStatusPass || sections.Providers.Result.PhaseStatus != PhaseStatusPass || sections.MCPTools.Result.PhaseStatus != PhaseStatusPass || sections.Pipeline.Result.PhaseStatus != PhaseStatusPass {
		t.Fatalf("core_only hid executed functional sections: %#v", sections)
	}
}

func TestAggregateToolResultRejectsDuplicateRows(t *testing.T) {
	t.Parallel()
	rows := make([]ToolEvidenceRow, 79)
	for index := range rows {
		rows[index] = ToolEvidenceRow{Tool: fmt.Sprintf("tool-%02d", index), Result: testPassResult()}
	}
	rows[len(rows)-1].Tool = rows[0].Tool
	result := aggregateToolResult(rows, nil)
	if result.PhaseStatus != PhaseStatusFail || !result.Attempted {
		t.Fatalf("duplicate tool identities passed coverage: %#v", result)
	}
}

func TestAggregateProviderResultRejectsDuplicateRows(t *testing.T) {
	t.Parallel()
	providers := make([]ProviderExecution, 7)
	for index := range providers {
		name := fmt.Sprintf("provider-%02d", index)
		providers[index] = ProviderExecution{Provider: name, Runtime: testPassResult(), Debug: testPassResult(), Result: testPassResult()}
	}
	providers[len(providers)-1].Provider = providers[0].Provider
	result := aggregateProviderResult(providers, nil)
	if result.PhaseStatus != PhaseStatusFail || !result.Attempted {
		t.Fatalf("duplicate provider identities passed coverage: %#v", result)
	}
}

func TestBlockScenarioKeepsUncalledPipelineToolsBlockedAndUnattempted(t *testing.T) {
	t.Parallel()
	scenario := Scenario{ID: "remote-pipeline", Title: "remote", Steps: []ScenarioStep{
		{ID: "deploy", Tool: "pipeline_run", Coverage: CoveragePrimary},
		{ID: "update", Tool: "pipeline_run_update", Coverage: CoveragePrimary},
		{ID: "rollback", Tool: "pipeline_rollback", Coverage: CoveragePrimary},
		{ID: "status", Tool: "pipeline_status", Coverage: CoveragePrimary},
		{ID: "logs", Tool: "pipeline_logs", Coverage: CoveragePrimary},
		{ID: "cleanup", Tool: "pipeline_cleanup", Coverage: CoveragePrimary},
	}}
	executor := &ScenarioExecutor{}
	execution := executor.blockScenario(scenario, "remote_host_available", "dedicated Host prerequisite failed")
	if execution.Result.PhaseStatus != PhaseStatusBlocked || execution.Result.Attempted {
		t.Fatalf("scenario result=%#v, want unattempted BLOCKED", execution.Result)
	}
	if len(executor.toolRows) != 6 {
		t.Fatalf("tool rows=%d, want 6", len(executor.toolRows))
	}
	for _, row := range executor.toolRows {
		if row.Result.PhaseStatus != PhaseStatusBlocked || row.Result.Attempted {
			t.Fatalf("uncalled tool %s was misclassified: %#v", row.Tool, row.Result)
		}
	}
}

func TestEnsureAllToolRowsUsesNotRunWithoutNamedBlocker(t *testing.T) {
	t.Parallel()
	rows := ensureAllToolRows([]CoverageAssignment{{
		Tool: "pipeline_run", ScenarioID: "remote-pipeline", StepID: "deploy",
	}}, nil, notRunResult("primary step was not reached"))
	if len(rows) != 1 {
		t.Fatalf("rows=%d, want 1", len(rows))
	}
	if rows[0].Result.PhaseStatus != PhaseStatusNotRun || rows[0].Result.Attempted {
		t.Fatalf("unreached tool was misclassified: %#v", rows[0].Result)
	}
}

func TestProviderPrerequisiteFailureKeepsUncalledTargetsBlocked(t *testing.T) {
	t.Parallel()
	observed := time.Date(2026, time.July, 14, 8, 0, 0, 0, time.UTC)
	preflight := providerStageResult(false, "Go toolchain missing", observed, observed)
	evidence := providerEvidence{Provider: "go", PrerequisiteStages: []stageEvidence{{
		Stage: "runtime_preflight", Tool: "preflight.cmd", Result: preflight,
	}}}
	target := blockedResult("runtime_preflight", "Go toolchain missing")
	executor := &ScenarioExecutor{resultsDir: t.TempDir(), redactor: NewRedactor()}
	provider := executor.finishProvider(FixtureManifest{Provider: "go"}, evidence, target, target, "Go toolchain missing")
	if provider.Result.PhaseStatus != PhaseStatusFail || !provider.Result.Attempted {
		t.Fatalf("provider overall did not retain attempted prerequisite failure: %#v", provider.Result)
	}
	if provider.Runtime.PhaseStatus != PhaseStatusBlocked || provider.Runtime.Attempted || provider.Debug.PhaseStatus != PhaseStatusBlocked || provider.Debug.Attempted {
		t.Fatalf("uncalled provider targets were conflated with preflight: runtime=%#v debug=%#v", provider.Runtime, provider.Debug)
	}
	if len(provider.Prerequisites) != 1 || provider.Prerequisites[0].Result.PhaseStatus != PhaseStatusFail || !provider.Prerequisites[0].Result.Attempted {
		t.Fatalf("provider prerequisite fact missing: %#v", provider.Prerequisites)
	}
}

func TestProviderEvidenceWriteFailureKeepsSanitizedInlineStages(t *testing.T) {
	t.Parallel()
	blockedRoot := filepath.Join(t.TempDir(), "results-file")
	if err := os.WriteFile(blockedRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	redactor := NewRedactor()
	secret := "Bearer provider-inline-secret"
	redactor.RegisterSecret("AUTHORIZATION", secret)
	pass := testPassResult()
	evidence := providerEvidence{Provider: "go", RuntimeStages: []stageEvidence{{
		Stage: "readiness", Tool: "http", Result: pass,
		Request: map[string]any{"authorization": secret}, Response: map[string]any{"status": 500, "body": "real failure response", "observed_at_utc": resultTestTime},
	}}}
	executor := &ScenarioExecutor{resultsDir: blockedRoot, redactor: redactor, campaignID: "campaign-provider-inline", lane: "nsis_core"}
	provider := executor.finishProvider(FixtureManifest{Provider: "go"}, evidence, pass, pass, "")
	if provider.Result.PhaseStatus != PhaseStatusFail || provider.InlineEvidence == nil {
		t.Fatalf("provider evidence write failure lost contract: %#v", provider)
	}
	encoded := CanonicalJSON(provider.InlineEvidence)
	if strings.Contains(encoded, "provider-inline-secret") || !strings.Contains(encoded, "real failure response") || !strings.Contains(encoded, resultTestTime) {
		t.Fatalf("provider inline evidence is unsafe or incomplete: %s", encoded)
	}
}

func TestRecordStepEvidencePreservesResponseAfterAssertionFailure(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	executor := &ScenarioExecutor{resultsDir: directory, redactor: NewRedactor()}
	response := map[string]any{"structuredContent": map[string]any{"data": map[string]any{"state": "unexpected-but-real"}}}
	step := ScenarioStep{
		ID: "assert-state", Tool: "pipeline_status", Coverage: CoveragePrimary,
		Evidence: EvidenceContract{Record: []string{"structuredContent.data.state"}},
	}
	attempts := []mcpCallAttempt{{
		StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime, Response: response,
		AssertionErr: "state=unexpected-but-real, want succeeded",
	}}
	records, inline, err := executor.recordStepEvidence("remote-pipeline", step, map[string]any{"run_id": "run-1"}, attempts, response, assertError("post assertion mismatch"))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || !records[0].Present || !records[1].Present {
		t.Fatalf("evidence obligations were not completed: %#v", records)
	}
	if inline != nil {
		t.Fatalf("successful evidence persistence should not duplicate inline payload: %#v", inline)
	}
	for _, name := range []string{"attempts.json", "evidence.json"} {
		content, readErr := os.ReadFile(filepath.Join(directory, "evidence", "remote-pipeline", "assert-state", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := string(content)
		if !strings.Contains(text, "unexpected-but-real") {
			t.Fatalf("%s lost the real MCP response: %s", name, text)
		}
		if name == "attempts.json" && (!strings.Contains(text, resultTestTime) || !strings.Contains(text, "state=unexpected-but-real")) {
			t.Fatalf("%s lost call timing or assertion difference: %s", name, text)
		}
	}
}

func TestRecordStepEvidenceFallsBackToSanitizedInlinePayload(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blockedRoot := filepath.Join(root, "results-file")
	if err := os.WriteFile(blockedRoot, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	redactor := NewRedactor()
	secret := "Bearer inline-secret-value"
	redactor.RegisterSecret("AUTHORIZATION", secret)
	executor := &ScenarioExecutor{resultsDir: blockedRoot, redactor: redactor, campaignID: "campaign-inline", lane: "nsis_core"}
	response := map[string]any{
		"structuredContent": map[string]any{"data": map[string]any{"state": "failed-response"}},
		"authorization":     secret,
	}
	step := ScenarioStep{ID: "inline-failure", Tool: "pipeline_status", Coverage: CoveragePrimary, Evidence: EvidenceContract{Record: []string{"structuredContent.data.state"}}}
	attempts := []mcpCallAttempt{{StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime, Response: response, AssertionErr: "state mismatch"}}
	records, inline, err := executor.recordStepEvidence("remote-pipeline", step, map[string]any{"authorization": secret}, attempts, response, assertError("state mismatch"))
	if err == nil || len(records) != 2 || records[0].WriteError == "" || inline == nil {
		t.Fatalf("write failure did not preserve inline evidence: records=%#v inline=%#v err=%v", records, inline, err)
	}
	encoded := CanonicalJSON(inline)
	if strings.Contains(encoded, "inline-secret-value") || !strings.Contains(encoded, resultTestTime) || !strings.Contains(encoded, "failed-response") || !strings.Contains(encoded, "state mismatch") {
		t.Fatalf("inline evidence is unsafe or incomplete: %s", encoded)
	}
}

func TestScenarioAndRenderPrerequisitesKeepTargetsBlocked(t *testing.T) {
	t.Parallel()
	executor := &ScenarioExecutor{
		resultsDir: t.TempDir(), redactor: NewRedactor(), campaignID: "campaign-prerequisite", lane: "nsis_core",
		variables: map[string]any{}, passed: map[string]bool{},
	}
	scenario := Scenario{
		ID: "missing-variable", Title: "missing variable",
		Variables: map[string]any{"required_id": map[string]any{"required": true}},
		Steps:     []ScenarioStep{{ID: "target", Tool: "list_projects", Coverage: CoveragePrimary}},
	}
	execution := executor.ExecuteScenario(t.Context(), scenario)
	if execution.Result.PhaseStatus != PhaseStatusBlocked || execution.Result.Attempted || len(execution.Prerequisites) != 1 || execution.Prerequisites[0].Result.PhaseStatus != PhaseStatusFail || !execution.Prerequisites[0].Result.Attempted {
		t.Fatalf("scenario prerequisite and target were conflated: %#v", execution)
	}
	rendered := executor.executeStep(t.Context(), "render-scenario", ScenarioStep{
		ID: "render-target", Tool: "list_projects", Coverage: CoveragePrimary,
		Arguments: map[string]any{"project_id": "{{undefined_project_id}}"},
	})
	if rendered.Result.PhaseStatus != PhaseStatusBlocked || rendered.Result.Attempted || len(rendered.Prerequisites) != 1 || rendered.Prerequisites[0].Result.PhaseStatus != PhaseStatusFail || !rendered.Prerequisites[0].Result.Attempted {
		t.Fatalf("render prerequisite and target were conflated: %#v", rendered)
	}
}

func TestScenarioKeepsGuardedCleanupFactWithoutDowngradingPassedTarget(t *testing.T) {
	t.Parallel()
	caller := &scriptedToolCaller{testing: t, calls: []scriptedToolCall{{tool: "list_projects", result: ToolCallResult{StructuredContent: map[string]any{"ok": true}}}}}
	executor := &ScenarioExecutor{
		client: caller, resultsDir: t.TempDir(), redactor: NewRedactor(),
		campaignID: "campaign-optional-cleanup", lane: "nsis_core", variables: map[string]any{}, passed: map[string]bool{},
	}
	scenario := Scenario{
		ID: "optional-cleanup", Title: "optional cleanup",
		Steps:   []ScenarioStep{{ID: "target", Tool: "list_projects", Coverage: CoveragePrimary, Expect: StepExpectation{Outcome: "success"}}},
		Cleanup: []ScenarioStep{{ID: "cleanup", Tool: "stop_service", Coverage: CoverageSupporting, RunIf: "variable_set:created_service", Expect: StepExpectation{Outcome: "success"}}},
	}
	execution := executor.ExecuteScenario(t.Context(), scenario)
	if execution.Result.PhaseStatus != PhaseStatusPass || len(execution.Cleanup) != 1 || execution.Cleanup[0].Result.PhaseStatus != PhaseStatusNotRun {
		t.Fatalf("guarded cleanup fact changed a passed target result: %#v", execution)
	}
}

func TestProductionCodeCannotAssignPhaseStatusOutsideResultModule(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || name == "result.go" {
			continue
		}
		file, parseErr := parser.ParseFile(files, name, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CompositeLit:
				identifier, ok := typed.Type.(*ast.Ident)
				if !ok || identifier.Name != "ValidationResult" {
					return true
				}
				for _, element := range typed.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, keyOK := field.Key.(*ast.Ident)
					if keyOK && key.Name == "PhaseStatus" {
						t.Errorf("%s directly assigns ValidationResult.PhaseStatus", files.Position(field.Pos()))
					}
				}
			case *ast.AssignStmt:
				for _, expression := range typed.Lhs {
					selector, ok := expression.(*ast.SelectorExpr)
					if ok && selector.Sel.Name == "PhaseStatus" {
						t.Errorf("%s directly assigns PhaseStatus", files.Position(selector.Pos()))
					}
				}
			}
			return true
		})
	}
}

func TestMaterializePreDriverFailureKeepsTargetsUnattempted(t *testing.T) {
	t.Parallel()
	packageRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	backup := t.TempDir()
	results := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260714T100000Z-a1b2c3"
	if err := writeJSON(filepath.Join(backup, "backup-manifest.json"), map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.prepared-backup", "status": "ready",
		"created_at_utc": resultTestTime, "lane": "nsis_core", "campaign_id": campaignID,
		"baseline_sha256": strings.Repeat("a", 64), "baseline_category_sha256": testCleanupCategoryHashMap(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(backup, "run-failure.json"), map[string]any{
		"schema_version": 2, "kind": "superdev.windows-validation.pre-driver-failure",
		"campaign_id": campaignID, "lane": "nsis_core", "stage": "installer_artifact_verification", "error": "installer hash mismatch", "observed_at_utc": resultTestTime,
		"execution_facts": map[string]any{
			"attempted": true, "succeeded": false, "failure": "installer hash mismatch",
			"started_at_utc": resultTestTime, "finished_at_utc": resultTestTime,
		},
		"artifact_verification": map[string]any{
			"attempted": true, "succeeded": false, "failure": "installer hash mismatch",
			"started_at_utc": resultTestTime, "finished_at_utc": resultTestTime,
		},
	}); err != nil {
		t.Fatal(err)
	}
	report, err := MaterializePreDriverFailure(packageRoot, results, backup, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Target != WindowsValidationTargetLabel {
		t.Fatalf("pre-driver target=%q, want %q", report.Target, WindowsValidationTargetLabel)
	}
	if report.Result.PhaseStatus != PhaseStatusFail || report.Functional.PhaseStatus != PhaseStatusBlocked || report.Functional.Attempted {
		t.Fatalf("pre-driver target/prerequisite facts were conflated: result=%#v functional=%#v", report.Result, report.Functional)
	}
	if len(report.Prerequisites) != 1 || !report.Prerequisites[0].Result.Attempted || report.Prerequisites[0].Result.PhaseStatus != PhaseStatusFail {
		t.Fatalf("pre-driver failure fact was not retained: %#v", report.Prerequisites)
	}
	if report.Installer.ArtifactVerified || report.Installer.InstallerExecuted || report.Installer.Artifact.PhaseStatus != PhaseStatusFail || report.Installer.Lifecycle.PhaseStatus != PhaseStatusNotRun {
		t.Fatalf("pre-driver artifact and lifecycle facts were conflated: %#v", report.Installer)
	}
	if len(report.ToolRows) != 79 || len(report.Providers) != 7 {
		t.Fatalf("pre-driver matrix providers=%d tools=%d", len(report.Providers), len(report.ToolRows))
	}
	for _, row := range report.ToolRows {
		if row.Result.PhaseStatus != PhaseStatusBlocked || row.Result.Attempted {
			t.Fatalf("pre-driver target %s was misclassified: %#v", row.Tool, row.Result)
		}
	}
	raw, err := os.ReadFile(filepath.Join(results, campaignID, "campaign-report.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"verdict":`) || !strings.Contains(string(raw), `"phase_status":`) {
		t.Fatalf("campaign schema still contains a manual verdict path: %s", raw)
	}
}

func TestMaterializeWithoutRunFailureKeepsEverythingNotRun(t *testing.T) {
	t.Parallel()
	packageRoot := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	backup := t.TempDir()
	results := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260714T110000Z-d4e5f6"
	if err := writeJSON(filepath.Join(backup, "backup-manifest.json"), map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.prepared-backup", "status": "ready",
		"created_at_utc": resultTestTime, "lane": "nsis_core", "campaign_id": campaignID,
		"baseline_sha256": strings.Repeat("a", 64), "baseline_category_sha256": testCleanupCategoryHashMap(),
	}); err != nil {
		t.Fatal(err)
	}
	report, err := MaterializePreDriverFailure(packageRoot, results, backup, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if report.Result.PhaseStatus != PhaseStatusNotRun || report.Functional.PhaseStatus != PhaseStatusNotRun || len(report.Prerequisites) != 1 || report.Prerequisites[0].Result.PhaseStatus != PhaseStatusNotRun || report.Prerequisites[0].Result.Attempted {
		t.Fatalf("missing run-failure.json invented an execution: %#v", report)
	}
	if report.Installer.Artifact.PhaseStatus != PhaseStatusNotRun || report.Installer.InstallerExecuted {
		t.Fatalf("missing run-failure.json invented installer facts: %#v", report.Installer)
	}
	for _, row := range report.ToolRows {
		if row.Result.PhaseStatus != PhaseStatusNotRun || row.Result.Attempted {
			t.Fatalf("missing run-failure.json invented tool attempt %s: %#v", row.Tool, row.Result)
		}
	}
}

func TestPreDriverInstallerPreservesSuccessfulArtifactWithoutInventingLifecycle(t *testing.T) {
	t.Parallel()
	installer, err := preDriverInstaller("nsis_core", ExecutionFacts{
		Attempted: true, Succeeded: true, StartedAtUTC: resultTestTime, FinishedAtUTC: resultTestTime,
	}, []PackageFileIdentity{{Path: "frozen.exe", SizeBytes: 42, SHA256: "abc"}}, "run-failure.json", "driver failed later")
	if err != nil {
		t.Fatal(err)
	}
	if !installer.ArtifactVerified || installer.Artifact.PhaseStatus != PhaseStatusPass || installer.InstallerExecuted || installer.Lifecycle.PhaseStatus != PhaseStatusNotRun || installer.Result.PhaseStatus != PhaseStatusNotRun {
		t.Fatalf("pre-driver artifact success was conflated with installer lifecycle: %#v", installer)
	}
}

type assertError string

func (e assertError) Error() string { return string(e) }
