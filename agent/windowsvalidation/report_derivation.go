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
	if report.SchemaVersion != 2 || report.Kind != "superdev.windows-validation.campaign-report" {
		return CampaignReport{}, fmt.Errorf("campaign report schema identity is invalid")
	}
	if !campaignIDPattern.MatchString(report.CampaignID) {
		return CampaignReport{}, fmt.Errorf("campaign report id %q is invalid", report.CampaignID)
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
	// prerequisite 是独立尝试；固定 cleanup 必须有记录，但 guard 明确未触发的 NOT_RUN 不是失败子项。
	children := make([]ValidationResult, 0, len(scenario.Steps)+len(scenario.Cleanup))
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
	if len(catalog.Coverage) != 75 {
		return fmt.Errorf("frozen coverage catalog has %d rows, want exactly 75", len(catalog.Coverage))
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
