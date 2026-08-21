// report_derivation.go 在报告持久化边界重新执行统一结果合同。
//
// 职责：
//   - 忽略持久化对象中的派生 Phase Status，只读取执行事实与证据义务
//   - 自底向上重建 step、scenario、provider、installer、functional 与 campaign 结果
//   - 拒绝缺项、重复项或互相矛盾的持久化事实
//
// 边界：
//   - 不执行 Windows、MCP、provider 或 cleanup 动作
//   - 不补造安装器生命周期、工具调用或 prerequisite 事实
//   - 不负责报告文件的脱敏与写入
package windowsvalidation

import (
	"fmt"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

func rederiveCampaignReport(report CampaignReport) (CampaignReport, error) {
	if (report.SchemaVersion != 2 && report.SchemaVersion != CampaignReportSchemaVersion) || report.Kind != "superdev.windows-validation.campaign-report" {
		return CampaignReport{}, fmt.Errorf("campaign report schema identity is invalid")
	}
	if !campaignIDPattern.MatchString(report.CampaignID) {
		return CampaignReport{}, fmt.Errorf("campaign report id %q is invalid", report.CampaignID)
	}
	if strings.TrimSpace(report.Target) == "" {
		report.Target = WindowsValidationTargetLabel
	} else if report.Target != WindowsValidationTargetLabel {
		return CampaignReport{}, fmt.Errorf("campaign report target must be %s", WindowsValidationTargetLabel)
	}
	if err := validateCampaignReportStructure(report); err != nil {
		return CampaignReport{}, fmt.Errorf("validate frozen report structure: %w", err)
	}
	var err error
	report.Installer, err = rederiveInstallerExecution(report.Installer)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive installer: %w", err)
	}
	report.RuntimeAttestation.Result, err = rederiveValidationResult(report.RuntimeAttestation.Result)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive runtime attestation: %w", err)
	}
	if report.EnvironmentPreinstall != nil {
		if err := verifyEmbeddedEnvironmentPreinstall(report); err != nil {
			return CampaignReport{}, fmt.Errorf("verify embedded environment pre-install: %w", err)
		}
	} else if report.SchemaVersion >= CampaignReportSchemaVersion && report.FailureStage == "" && report.RuntimeAttestation.Result.Attempted {
		return CampaignReport{}, fmt.Errorf("completed campaign report is missing prepared environment pre-install evidence")
	}
	if report.EnvironmentManifest != nil {
		if report.EnvironmentManifest.CampaignID != report.CampaignID {
			return CampaignReport{}, fmt.Errorf("environment manifest campaign identity does not match report")
		}
		if report.EnvironmentPlan == nil || report.EnvironmentPlan.SchemaVersion != EnvironmentPlanSchemaVersion || report.EnvironmentPlan.Kind != EnvironmentPlanKind {
			return CampaignReport{}, fmt.Errorf("environment frozen plan is missing or invalid")
		}
		planDigest := CanonicalEnvironmentPlanDigest(*report.EnvironmentPlan)
		if err := VerifyEnvironmentManifestPlanBinding(*report.EnvironmentManifest, *report.EnvironmentPlan); err != nil {
			return CampaignReport{}, fmt.Errorf("verify embedded environment frozen plan binding: %w", err)
		}
		if report.EnvironmentAdmissionRequest == nil {
			return CampaignReport{}, fmt.Errorf("environment admission request is missing")
		}
		if report.EnvironmentAdmission == nil {
			return CampaignReport{}, fmt.Errorf("environment admission decision is missing")
		}
		if report.SchemaVersion >= CampaignReportSchemaVersion {
			if report.EnvironmentManifest.CollectionStage != EnvironmentCollectionStagePostInstall || report.EnvironmentAdmissionRequest.CollectionStage != EnvironmentCollectionStagePostInstall {
				return CampaignReport{}, fmt.Errorf("completed environment admission must use post_install collection stage")
			}
			if report.EnvironmentPreinstall == nil || report.EnvironmentManifest.PreviousManifestSHA256 != report.EnvironmentPreinstall.Record.ManifestDigest {
				return CampaignReport{}, fmt.Errorf("post-install environment manifest is not bound to prepared pre-install digest")
			}
			if err := VerifyPreInstallEnvironmentPlanBinding(report.EnvironmentPreinstall.Plan, *report.EnvironmentPlan); err != nil {
				return CampaignReport{}, err
			}
			if report.EnvironmentComparison == nil {
				return CampaignReport{}, fmt.Errorf("completed environment admission is missing A-to-B manifest comparison")
			}
		}
		if report.EnvironmentComparison != nil {
			if report.EnvironmentPreinstall == nil {
				return CampaignReport{}, fmt.Errorf("environment comparison exists without prepared pre-install evidence")
			}
			expectedComparison, comparisonErr := BuildEnvironmentManifestComparison(report.EnvironmentPreinstall.Manifest, *report.EnvironmentManifest)
			if comparisonErr != nil {
				return CampaignReport{}, fmt.Errorf("rederive A-to-B environment comparison: %w", comparisonErr)
			}
			if CanonicalJSON(expectedComparison) != CanonicalJSON(*report.EnvironmentComparison) {
				return CampaignReport{}, fmt.Errorf("environment comparison differs from embedded A and B manifests")
			}
		}
		if report.EnvironmentComparisonPersistence != nil {
			comparisonPersistence, persistenceErr := rederiveValidationResult(*report.EnvironmentComparisonPersistence)
			if persistenceErr != nil {
				return CampaignReport{}, fmt.Errorf("rederive A-to-B environment comparison persistence: %w", persistenceErr)
			}
			report.EnvironmentComparisonPersistence = &comparisonPersistence
			if report.EnvironmentComparison == nil {
				return CampaignReport{}, fmt.Errorf("environment comparison persistence exists without comparison facts")
			}
		}
		if report.SchemaVersion >= CampaignReportSchemaVersion && report.FailureStage == "" {
			if report.EnvironmentComparisonPersistence == nil || report.EnvironmentComparisonPersistence.PhaseStatus != PhaseStatusPass {
				return CampaignReport{}, fmt.Errorf("completed environment admission did not persist A-to-B manifest comparison")
			}
		}
		if !strings.EqualFold(planDigest, report.EnvironmentAdmissionRequest.ExpectedPlanDigest) {
			return CampaignReport{}, fmt.Errorf("environment admission expected_plan_digest does not match embedded frozen plan")
		}
		var decision EnvironmentAdmissionDecision
		var admissionErr error
		if hasEnvironmentCollectionProvenance(*report.EnvironmentManifest) {
			decision, admissionErr = AdmitEnvironmentManifest(*report.EnvironmentManifest, *report.EnvironmentAdmissionRequest)
		} else {
			// JSON 归档只允许结构复核，不能重新取得 collector-only 的准入能力。
			// 这里重建并比对当时已持久化的 decision，供 cleanup 合并保留原始事实。
			decision, admissionErr = deriveEnvironmentAdmission(*report.EnvironmentManifest, *report.EnvironmentAdmissionRequest)
			if admissionErr == nil && CanonicalJSON(decision) != CanonicalJSON(*report.EnvironmentAdmission) {
				return CampaignReport{}, fmt.Errorf("persisted environment admission decision differs from structural facts")
			}
		}
		if admissionErr != nil {
			return CampaignReport{}, fmt.Errorf("rederive environment admission: %w", admissionErr)
		}
		report.EnvironmentAdmission = &decision
		if report.FailureStage == "" && !decision.Admitted {
			return CampaignReport{}, fmt.Errorf("completed campaign report has a rejected environment admission")
		}
	} else {
		if report.EnvironmentPlan != nil || report.EnvironmentComparison != nil || report.EnvironmentComparisonPersistence != nil || report.EnvironmentAdmissionRequest != nil || report.EnvironmentAdmission != nil {
			return CampaignReport{}, fmt.Errorf("environment admission exists without an environment manifest")
		}
		if report.SchemaVersion >= CampaignReportSchemaVersion && (report.Lane == "nsis_core" || report.Lane == "core_only") && report.FailureStage == "" && report.RuntimeAttestation.Result.Attempted {
			return CampaignReport{}, fmt.Errorf("completed functional campaign report is missing environment preflight")
		}
	}
	report.Prerequisites, err = rederiveStepExecutions(report.Prerequisites)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive campaign prerequisites: %w", err)
	}
	report.Operations, err = rederiveStepExecutions(report.Operations)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive campaign operations: %w", err)
	}
	for index := range report.Scenarios {
		report.Scenarios[index], err = rederiveScenarioExecution(report.Scenarios[index])
		if err != nil {
			return CampaignReport{}, fmt.Errorf("rederive scenario %q: %w", report.Scenarios[index].ID, err)
		}
	}
	for index := range report.Providers {
		report.Providers[index], err = rederiveProviderExecution(report.Providers[index])
		if err != nil {
			return CampaignReport{}, fmt.Errorf("rederive provider %q: %w", report.Providers[index].Provider, err)
		}
	}
	for index := range report.ToolRows {
		report.ToolRows[index].Result, err = rederiveValidationResult(report.ToolRows[index].Result)
		if err != nil {
			return CampaignReport{}, fmt.Errorf("rederive tool row %q: %w", report.ToolRows[index].Tool, err)
		}
	}
	if _, err := deriveCleanupResult(report.Cleanup); err != nil {
		return CampaignReport{}, fmt.Errorf("rederive cleanup: %w", err)
	}
	report.Functional = deriveFunctionalResult(report)
	report.Sections = buildReportSections(report)
	report.Result = deriveCampaignCompletionResult("campaign completion", report)
	logger.GetLogger().WithEntryName("WindowsValidationReportDerivation").WithFields(map[string]any{
		"campaign_id":  report.CampaignID,
		"lane":         report.Lane,
		"phase_status": report.Result.PhaseStatus,
		"tool_rows":    len(report.ToolRows),
		"providers":    len(report.Providers),
		"scenarios":    len(report.Scenarios),
	}).Info("Windows 验证报告已从执行事实与证据重新派生")
	return report, nil
}

func verifyEmbeddedEnvironmentPreinstall(report CampaignReport) error {
	evidence := *report.EnvironmentPreinstall
	record := evidence.Record
	if record.SchemaVersion != PreparedEnvironmentPreinstallSchemaVersion || record.Kind != PreparedEnvironmentPreinstallKind {
		return fmt.Errorf("record identity is invalid")
	}
	if record.CampaignID != report.CampaignID || record.Lane != report.Lane || record.BuildCommit != report.BuildCommit || record.ProductVersion != report.ProductVersion {
		return fmt.Errorf("record campaign, lane, or build binding differs from report")
	}
	for name, value := range map[string]string{
		"prepared_baseline_sha256": record.PreparedBaselineSHA256, "stable_runtime_input_sha256": record.StableRuntimeInputSHA256,
		"stable_plan_sha256": record.StablePlanSHA256, "plan_file_sha256": record.PlanFileSHA256, "manifest_file_sha256": record.ManifestFileSHA256,
		"manifest_digest": record.ManifestDigest,
	} {
		if !validEnvironmentSHA256(value) {
			return fmt.Errorf("record %s is invalid", name)
		}
	}
	if evidence.Manifest.CampaignID != report.CampaignID || evidence.Manifest.CollectionStage != EnvironmentCollectionStagePreInstall {
		return fmt.Errorf("manifest campaign or stage binding differs")
	}
	if err := validatePreInstallEnvironmentCatalog(evidence.Manifest); err != nil {
		return err
	}
	if err := VerifyEnvironmentManifestPlanBinding(evidence.Manifest, evidence.Plan); err != nil {
		return err
	}
	if CanonicalPreInstallEnvironmentPlanDigest(evidence.Plan) != record.StablePlanSHA256 {
		return fmt.Errorf("record stable plan digest differs from embedded pre-install plan")
	}
	if CanonicalEnvironmentManifestDigest(evidence.Manifest) != record.ManifestDigest {
		return fmt.Errorf("record manifest digest differs from embedded pre-install facts")
	}
	expectedMode := EnvironmentAdmissionPreInstall
	if record.Lane == "core_only" {
		expectedMode = EnvironmentAdmissionDiagnostic
	}
	if record.Request.Mode != expectedMode || record.Request.CollectionStage != EnvironmentCollectionStagePreInstall {
		return fmt.Errorf("record admission request is invalid")
	}
	decision, err := deriveEnvironmentAdmission(evidence.Manifest, record.Request)
	if err != nil {
		return err
	}
	if CanonicalJSON(decision) != CanonicalJSON(record.Decision) || !decision.Admitted {
		return fmt.Errorf("record admission decision differs or is rejected")
	}
	if record.Lane != "core_only" && decision.Result.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("strict record admission decision is not PASS")
	}
	prerequisites := []ValidationResult{record.PackageIntegrity, record.InputSafety}
	children := []ValidationResult{record.PackageIntegrity, record.InputSafety, decision.Result}
	if record.Lane == "core_only" {
		if len(record.InstallerChecks) != 0 || len(report.InstallerChecks) != 0 {
			return fmt.Errorf("core_only record or report contains installer checks")
		}
		installerArtifact, deriveErr := rederiveValidationResult(record.InstallerArtifact)
		if deriveErr != nil || installerArtifact.PhaseStatus != PhaseStatusNotRun || record.InstallerArtifact.PhaseStatus != PhaseStatusNotRun {
			return fmt.Errorf("core_only record installer artifact is not a rederived NOT_RUN")
		}
		if err := validateCoreOnlyInstallerExclusion(report.Installer); err != nil {
			return err
		}
	} else {
		if len(record.InstallerChecks) != 1 || !containsPackageIdentity(report.InstallerChecks, record.InstallerChecks[0]) {
			return fmt.Errorf("record installer identity is not present in campaign installer checks")
		}
		prerequisites = append(prerequisites, record.InstallerArtifact)
		children = []ValidationResult{record.PackageIntegrity, record.InputSafety, record.InstallerArtifact, decision.Result}
	}
	for _, child := range prerequisites {
		derived, err := rederiveValidationResult(child)
		if err != nil || derived.PhaseStatus != PhaseStatusPass || child.PhaseStatus != PhaseStatusPass {
			return fmt.Errorf("record prerequisite is not a rederived PASS")
		}
	}
	overall, err := DeriveAggregateResult("prepared environment pre-install", len(children), children)
	if err != nil {
		return err
	}
	if record.Lane != "core_only" && overall.PhaseStatus != PhaseStatusPass {
		return fmt.Errorf("strict record overall result is not PASS")
	}
	if CanonicalJSON(overall) != CanonicalJSON(record.Result) {
		return fmt.Errorf("record overall result differs from rederived facts")
	}
	return nil
}

func validateCoreOnlyInstallerExclusion(installer InstallerExecution) error {
	derived, err := rederiveInstallerExecution(installer)
	if err != nil {
		return fmt.Errorf("rederive core_only installer exclusion: %w", err)
	}
	if CanonicalJSON(derived) != CanonicalJSON(installer) {
		return fmt.Errorf("core_only installer exclusion differs from rederived facts")
	}
	if derived.Format != "core_only" || derived.ArtifactVerified || derived.InstallerExecuted {
		return fmt.Errorf("core_only campaign claims installer format, artifact, or execution facts")
	}
	// core_only 报告会隐藏 installer section，因此必须在报告边界逐项证明零 installer facts；
	// 未尝试但被 BLOCKED 的 install/start/stop/uninstall 也不能被当作排除执行。
	for _, phase := range []struct {
		name   string
		result ValidationResult
	}{
		{name: "artifact", result: derived.Artifact}, {name: "install", result: derived.Install},
		{name: "start", result: derived.Start}, {name: "stop", result: derived.Stop},
		{name: "uninstall", result: derived.Uninstall}, {name: "lifecycle", result: derived.Lifecycle},
		{name: "result", result: derived.Result},
	} {
		if phase.result.PhaseStatus != PhaseStatusNotRun {
			return fmt.Errorf("core_only installer %s is %s, want NOT_RUN", phase.name, phase.result.PhaseStatus)
		}
	}
	return nil
}

func containsPackageIdentity(values []PackageFileIdentity, expected PackageFileIdentity) bool {
	for _, value := range values {
		if value.Path == expected.Path && value.SizeBytes == expected.SizeBytes && strings.EqualFold(value.SHA256, expected.SHA256) {
			return true
		}
	}
	return false
}

func rederiveValidationResult(result ValidationResult) (ValidationResult, error) {
	return DeriveValidationResult(resultInput(result))
}

func rederiveInstallerExecution(installer InstallerExecution) (InstallerExecution, error) {
	return DeriveInstallerExecution(InstallerExecutionFacts{
		Format:            installer.Format,
		ArtifactVerified:  installer.ArtifactVerified,
		InstallerExecuted: installer.InstallerExecuted,
		Artifact:          resultInput(installer.Artifact),
		Install:           resultInput(installer.Install),
		Start:             resultInput(installer.Start),
		Stop:              resultInput(installer.Stop),
		Uninstall:         resultInput(installer.Uninstall),
	})
}

func rederiveStepExecution(step StepExecution) (StepExecution, error) {
	var err error
	step.Result, err = rederiveValidationResult(step.Result)
	if err != nil {
		return StepExecution{}, err
	}
	step.Prerequisites, err = rederiveStepExecutions(step.Prerequisites)
	if err != nil {
		return StepExecution{}, err
	}
	return step, nil
}

func rederiveStepExecutions(steps []StepExecution) ([]StepExecution, error) {
	derived := append([]StepExecution{}, steps...)
	for index := range derived {
		var err error
		derived[index], err = rederiveStepExecution(derived[index])
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", derived[index].StepID, err)
		}
	}
	return derived, nil
}

func rederiveScenarioExecution(scenario ScenarioExecution) (ScenarioExecution, error) {
	var err error
	scenario.Prerequisites, err = rederiveStepExecutions(scenario.Prerequisites)
	if err != nil {
		return ScenarioExecution{}, fmt.Errorf("prerequisites: %w", err)
	}
	scenario.Steps, err = rederiveStepExecutions(scenario.Steps)
	if err != nil {
		return ScenarioExecution{}, fmt.Errorf("steps: %w", err)
	}
	scenario.Cleanup, err = rederiveStepExecutions(scenario.Cleanup)
	if err != nil {
		return ScenarioExecution{}, fmt.Errorf("cleanup: %w", err)
	}
	// requires 只是编排元数据；这里聚合的是已执行并归档的 prerequisite 事实。
	// 它们必须影响场景结论，否则远端写入前或 cleanup 后的身份漂移会被步骤 PASS 掩盖。
	children := make([]ValidationResult, 0, len(scenario.Prerequisites)+len(scenario.Steps)+len(scenario.Cleanup))
	children = appendPrerequisiteResults(children, scenario.Prerequisites)
	for _, step := range scenario.Steps {
		children = append(children, step.Result)
	}
	for _, step := range scenario.Cleanup {
		if step.Result.PhaseStatus != PhaseStatusNotRun {
			children = append(children, step.Result)
		}
	}
	scenario.Result = aggregateResult(scenario.ID+" scenario", len(children), children)
	return scenario, nil
}

func validateCampaignReportStructure(report CampaignReport) error {
	catalog := report.ValidationCatalog
	if len(catalog.Scenarios) == 0 {
		return fmt.Errorf("frozen scenario catalog is missing")
	}
	if len(catalog.Coverage) != 79 {
		return fmt.Errorf("frozen coverage catalog has %d rows, want exactly 79", len(catalog.Coverage))
	}

	executions := make(map[string]ScenarioExecution, len(report.Scenarios))
	for _, execution := range report.Scenarios {
		id := strings.TrimSpace(execution.ID)
		if id == "" || executions[id].ID != "" {
			return fmt.Errorf("scenario executions contain blank or duplicate id %q", id)
		}
		executions[id] = execution
	}
	if len(executions) != len(catalog.Scenarios) {
		return fmt.Errorf("scenario executions have %d rows, frozen catalog requires %d", len(executions), len(catalog.Scenarios))
	}

	primary := make(map[string]CoverageAssignment, len(catalog.Coverage))
	seenCatalogScenarios := make(map[string]bool, len(catalog.Scenarios))
	for _, expected := range catalog.Scenarios {
		if strings.TrimSpace(expected.ID) == "" || expected.ID != strings.TrimSpace(expected.ID) || seenCatalogScenarios[expected.ID] {
			return fmt.Errorf("frozen scenario catalog contains blank or duplicate id %q", expected.ID)
		}
		seenCatalogScenarios[expected.ID] = true
		if err := validateScenarioCatalogEntry(expected); err != nil {
			return err
		}
		actual, ok := executions[expected.ID]
		if !ok {
			return fmt.Errorf("scenario %q is missing from executions", expected.ID)
		}
		if actual.Title != expected.Title {
			return fmt.Errorf("scenario %q title does not match frozen catalog", expected.ID)
		}
		if err := validateScenarioStepExecutions(expected.ID, "steps", expected.Steps, actual.Steps, primary); err != nil {
			return err
		}
		if err := validateScenarioStepExecutions(expected.ID, "cleanup", expected.Cleanup, actual.Cleanup, nil); err != nil {
			return err
		}
	}

	coverageByTool := make(map[string]CoverageAssignment, len(catalog.Coverage))
	for _, assignment := range catalog.Coverage {
		tool := strings.TrimSpace(assignment.Tool)
		if tool == "" || coverageByTool[tool].Tool != "" {
			return fmt.Errorf("frozen coverage contains blank or duplicate tool %q", tool)
		}
		expected, ok := primary[tool]
		if !ok || expected.ScenarioID != assignment.ScenarioID || expected.StepID != assignment.StepID {
			return fmt.Errorf("frozen coverage mapping for tool %q does not match its primary scenario step", tool)
		}
		coverageByTool[tool] = assignment
	}
	if len(primary) != len(coverageByTool) {
		return fmt.Errorf("frozen scenario primary steps=%d coverage rows=%d", len(primary), len(coverageByTool))
	}
	if err := validateToolRowCoverage(report.ToolRows, catalog.Coverage); err != nil {
		return err
	}
	return nil
}

func validateScenarioCatalogEntry(scenario ScenarioCatalogEntry) error {
	seen := make(map[string]bool, len(scenario.Steps)+len(scenario.Cleanup))
	for index, step := range append(append([]StepCatalogEntry{}, scenario.Steps...), scenario.Cleanup...) {
		if strings.TrimSpace(step.StepID) == "" || step.StepID != strings.TrimSpace(step.StepID) || seen[step.StepID] {
			return fmt.Errorf("scenario %q frozen catalog contains blank or duplicate step id %q", scenario.ID, step.StepID)
		}
		seen[step.StepID] = true
		if strings.TrimSpace(step.Tool) == "" || step.Tool != strings.TrimSpace(step.Tool) {
			return fmt.Errorf("scenario %q step %q has an invalid frozen tool identity", scenario.ID, step.StepID)
		}
		if step.Coverage != CoveragePrimary && step.Coverage != CoverageSupporting {
			return fmt.Errorf("scenario %q step %q has invalid frozen coverage %q", scenario.ID, step.StepID, step.Coverage)
		}
		if index >= len(scenario.Steps) && step.Coverage == CoveragePrimary {
			return fmt.Errorf("scenario %q cleanup step %q cannot own primary coverage", scenario.ID, step.StepID)
		}
	}
	return nil
}

func validateScenarioStepExecutions(scenarioID, group string, expected []StepCatalogEntry, actual []StepExecution, primary map[string]CoverageAssignment) error {
	actualByID := make(map[string]StepExecution, len(actual))
	for _, step := range actual {
		id := strings.TrimSpace(step.StepID)
		if id == "" || actualByID[id].StepID != "" {
			return fmt.Errorf("scenario %q %s contain blank or duplicate step id %q", scenarioID, group, id)
		}
		actualByID[id] = step
	}
	if len(actualByID) != len(expected) {
		return fmt.Errorf("scenario %q %s have %d rows, frozen catalog requires %d", scenarioID, group, len(actualByID), len(expected))
	}
	seenExpected := make(map[string]bool, len(expected))
	for _, contract := range expected {
		if strings.TrimSpace(contract.StepID) == "" || seenExpected[contract.StepID] {
			return fmt.Errorf("scenario %q frozen %s contain blank or duplicate step id %q", scenarioID, group, contract.StepID)
		}
		seenExpected[contract.StepID] = true
		step, ok := actualByID[contract.StepID]
		if !ok || step.Tool != contract.Tool || step.Coverage != contract.Coverage {
			return fmt.Errorf("scenario %q step %q identity does not match frozen %s catalog", scenarioID, contract.StepID, group)
		}
		if contract.Coverage == CoveragePrimary {
			if primary == nil {
				return fmt.Errorf("scenario %q cleanup step %q cannot own primary coverage", scenarioID, contract.StepID)
			}
			if existing := primary[contract.Tool]; existing.Tool != "" {
				return fmt.Errorf("primary tool %q is duplicated by scenario %q step %q", contract.Tool, scenarioID, contract.StepID)
			}
			primary[contract.Tool] = CoverageAssignment{Tool: contract.Tool, ScenarioID: scenarioID, StepID: contract.StepID}
		}
	}
	return nil
}

func validateToolRowCoverage(rows []ToolEvidenceRow, coverage []CoverageAssignment) error {
	if len(rows) != len(coverage) {
		return fmt.Errorf("tool rows=%d frozen coverage=%d", len(rows), len(coverage))
	}
	expected := make(map[string]CoverageAssignment, len(coverage))
	for _, assignment := range coverage {
		if strings.TrimSpace(assignment.Tool) == "" || expected[assignment.Tool].Tool != "" {
			return fmt.Errorf("frozen coverage contains blank or duplicate tool %q", assignment.Tool)
		}
		expected[assignment.Tool] = assignment
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		assignment, ok := expected[row.Tool]
		if !ok || seen[row.Tool] || assignment.ScenarioID != row.ScenarioID || assignment.StepID != row.StepID {
			return fmt.Errorf("tool row %q does not match its frozen scenario/step mapping", row.Tool)
		}
		seen[row.Tool] = true
	}
	return nil
}

func rederiveProviderExecution(provider ProviderExecution) (ProviderExecution, error) {
	var err error
	provider.Runtime, err = rederiveValidationResult(provider.Runtime)
	if err != nil {
		return ProviderExecution{}, fmt.Errorf("runtime: %w", err)
	}
	provider.Debug, err = rederiveValidationResult(provider.Debug)
	if err != nil {
		return ProviderExecution{}, fmt.Errorf("debug: %w", err)
	}
	provider.Prerequisites, err = rederiveStepExecutions(provider.Prerequisites)
	if err != nil {
		return ProviderExecution{}, fmt.Errorf("prerequisites: %w", err)
	}
	children := make([]ValidationResult, 0, 2+len(provider.Prerequisites))
	children = append(children, provider.Runtime, provider.Debug)
	for _, prerequisite := range provider.Prerequisites {
		children = append(children, prerequisite.Result)
	}
	provider.Result = aggregateResult(provider.Provider+" provider", len(children), children)
	if strings.TrimSpace(provider.Reason) == "" && provider.Result.PhaseStatus != PhaseStatusPass {
		provider.Reason = resultReason(provider.Result)
	}
	return provider, nil
}

func deriveFunctionalResult(report CampaignReport) ValidationResult {
	var target ValidationResult
	switch report.Lane {
	case "msi_smoke":
		target = report.RuntimeAttestation.Result
	case "nsis_core", "core_only":
		target = aggregateCampaignResult(report.ToolRows, report.Providers, report.Scenarios, report.RuntimeAttestation.ToolNames, report.RuntimeAttestation.ProviderNames, report.ValidationCatalog.Coverage)
	default:
		return attemptedResult(false, fmt.Sprintf("unsupported Windows validation lane %q", report.Lane), "", "", nil)
	}
	if len(report.Operations) == 0 {
		// 门禁失败时 packaged MCP stop 尚不可执行；已完成目标面则必须有显式 stop 事实。
		if target.PhaseStatus != PhaseStatusPass {
			return target
		}
		return attemptedResult(false, "packaged MCP stop execution fact is missing", "", "", nil)
	}
	if len(report.Operations) != 1 || report.Operations[0].StepID != "packaged-mcp-stop" {
		return attemptedResult(false, "campaign must contain exactly one packaged-mcp-stop execution fact", "", "", nil)
	}
	return aggregateResult("Windows functional execution and packaged MCP stop", 2, []ValidationResult{target, report.Operations[0].Result})
}
