// report.go 把结构化 campaign 结果渲染为便于人工复查的 Markdown 报告。
//
// 职责：
//   - 分开展示 installer、core、provider、75 工具、pipeline 与 cleanup 结果
//   - 保留每一条 BLOCKED/FAIL 和证据相对路径
//
// 边界：
//   - 不手工拼装 Phase Status 或用总分掩盖局部失败
//   - 不写入请求、凭据或完整 MCP 响应
package windowsvalidation

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

var campaignIDPattern = regexp.MustCompile(`^w10x64-[0-9a-f]{7}-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)

func writeMarkdownReport(path string, report CampaignReport) error {
	var out strings.Builder
	target := strings.TrimSpace(report.Target)
	if target == "" {
		target = WindowsValidationTargetLabel
	}
	if target != WindowsValidationTargetLabel {
		return fmt.Errorf("managed report target must be %s", WindowsValidationTargetLabel)
	}
	title := "SuperDev " + WindowsValidationTargetLabel + " 真实验证报告"
	if report.Kind == "superdev.windows-validation.summary" {
		title = "SuperDev " + WindowsValidationTargetLabel + " 最终聚合验收摘要"
	}
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "- Campaign: `%s`\n", markdownCell(report.CampaignID))
	fmt.Fprintf(&out, "- 冻结构建: `%s` / `%s`\n", markdownCell(report.BuildCommit), markdownCell(report.ProductVersion))
	fmt.Fprintf(&out, "- 执行 lane: `%s`\n", markdownCell(report.Lane))
	fmt.Fprintf(&out, "- 目标平台: `%s`\n", markdownCell(target))
	fmt.Fprintf(&out, "- 总状态: **%s**（attempted=%t）\n", report.Result.PhaseStatus, report.Result.Attempted)
	fmt.Fprintf(&out, "- Windows 功能状态: **%s**（attempted=%t）\n", report.Functional.PhaseStatus, report.Functional.Attempted)
	fmt.Fprintf(&out, "- 冻结验收目录: scenario `%d`，tool mapping `%d`\n", len(report.ValidationCatalog.Scenarios), len(report.ValidationCatalog.Coverage))
	fmt.Fprintf(&out, "- 时间: `%s` — `%s`\n\n", report.StartedAtUTC, report.FinishedAtUTC)

	out.WriteString("## 固定验收面\n\n")
	out.WriteString("| 验收面 | 状态 | 证据 | 原因 |\n|---|---|---|---|\n")
	for _, section := range []struct {
		name  string
		value ReportSection
	}{
		{"MSI installer lane", report.Sections.MSIInstaller},
		{"NSIS installer lane", report.Sections.NSISInstaller},
		{"Environment preflight", report.Sections.Environment},
		{"Core", report.Sections.Core},
		{"七语言 provider", report.Sections.Providers},
		{"75 MCP tools", report.Sections.MCPTools},
		{"Remote pipeline", report.Sections.Pipeline},
		{"Cleanup / baseline restore", report.Sections.Cleanup},
	} {
		reason := section.value.Reason
		if strings.TrimSpace(reason) == "" {
			reason = resultReason(section.value.Result)
		}
		fmt.Fprintf(&out, "| %s | %s (attempted=%t) | `%s` | %s |\n", section.name, section.value.Result.PhaseStatus, section.value.Result.Attempted, markdownCell(section.value.EvidencePath), markdownCell(reason))
	}
	out.WriteString("\n")
	if report.EnvironmentPreinstall != nil {
		preinstall := report.EnvironmentPreinstall
		fmt.Fprintf(&out, "## 安装前环境门禁（A）\n\n- Stage: `%s`；mode: `%s`；admitted: `%t`。\n", preinstall.Manifest.CollectionStage, preinstall.Record.Request.Mode, preinstall.Record.Decision.Admitted)
		fmt.Fprintf(&out, "- Result: **%s**（attempted=%t）；manifest digest: `%s`。\n", preinstall.Record.Result.PhaseStatus, preinstall.Record.Result.Attempted, markdownCell(preinstall.Record.ManifestDigest))
		fmt.Fprintf(&out, "- 稳定 runtime input / plan digest: `%s` / `%s`；A 只冻结安装前稳定字段，Host / governance 由 B 完整绑定。\n", markdownCell(preinstall.Record.StableRuntimeInputSHA256), markdownCell(preinstall.Record.StablePlanSHA256))
		fmt.Fprintf(&out, "- 后续 B 必须通过 `previous_manifest_sha256` 绑定此 digest；证据：`prepared-backup/%s`。\n\n", filepath.ToSlash(PreparedEnvironmentPreinstallDirectory))
	}
	if report.EnvironmentManifest != nil {
		out.WriteString("## 安装后环境预检（B）\n\n")
		mode := EnvironmentAdmissionMode("")
		if report.EnvironmentAdmissionRequest != nil {
			mode = report.EnvironmentAdmissionRequest.Mode
		}
		fmt.Fprintf(&out, "- Stage: `%s`；mode: `%s`；admitted: `%t`；catalog: `%s`。\n", report.EnvironmentManifest.CollectionStage, mode, report.EnvironmentAdmission != nil && report.EnvironmentAdmission.Admitted, markdownCell(report.EnvironmentManifest.CatalogVersion))
		fmt.Fprintf(&out, "- Previous A manifest digest: `%s`。\n", markdownCell(report.EnvironmentManifest.PreviousManifestSHA256))
		out.WriteString("- B 完整计划与事实：`environment-plan.json` / `environment-manifest.json` / `environment-manifest.md`。\n")
		if report.EnvironmentComparison != nil {
			if environmentComparisonWasPersisted(report) {
				fmt.Fprintf(&out, "- A→B 可重派生差异：`%s`（%d 条 drift）。\n", EnvironmentManifestComparisonFilename, len(report.EnvironmentComparison.Drifts))
			} else {
				fmt.Fprintf(&out, "- A→B 可重派生差异仅保留在 `campaign-report.json#environment_comparison`（%d 条 drift）；未声明独立 comparison 文件已落盘。\n", len(report.EnvironmentComparison.Drifts))
			}
		}
		out.WriteString("\n")
		out.WriteString("| Prerequisite | Expected | Observed | Resolved | Result | Remediation |\n|---|---|---|---|---|---|\n")
		for _, prerequisite := range report.EnvironmentManifest.Prerequisites {
			fmt.Fprintf(&out, "| `%s` | `%s` | `%s` | `%s` | %s | %s |\n", markdownCell(prerequisite.Key), markdownCell(CanonicalJSON(prerequisite.Expected)), markdownCell(CanonicalJSON(prerequisite.Observed)), markdownCell(CanonicalJSON(prerequisite.Resolved)), prerequisite.Result.PhaseStatus, markdownCell(prerequisite.Remediation))
		}
		out.WriteString("\n")
		if report.EnvironmentComparison != nil {
			out.WriteString("### A→B environment drift\n\n")
			out.WriteString("| Prerequisite | Field | A | B |\n|---|---|---|---|\n")
			for _, drift := range report.EnvironmentComparison.Drifts {
				fmt.Fprintf(&out, "| `%s` | `%s` | `%s` | `%s` |\n", markdownCell(drift.Key), markdownCell(drift.Field), markdownCell(drift.Previous), markdownCell(drift.Current))
			}
			out.WriteString("\n")
		}
	}

	out.WriteString("## 安装器与 packaged runtime 身份\n\n")
	out.WriteString("| 类型 | 文件 | 大小 | SHA-256 |\n|---|---|---:|---|\n")
	installers := append([]PackageFileIdentity{}, report.InstallerChecks...)
	sort.Slice(installers, func(i, j int) bool { return installers[i].Path < installers[j].Path })
	for _, installer := range installers {
		format := "NSIS"
		if strings.HasSuffix(strings.ToLower(installer.Path), ".msi") {
			format = "MSI"
		}
		fmt.Fprintf(&out, "| %s | `%s` | %d | `%s` |\n", format, markdownCell(installer.Path), installer.SizeBytes, installer.SHA256)
	}
	fmt.Fprintf(&out, "\nArtifact verified: **%t**；installer executed: **%t**；installer lifecycle: **%s**.\n\n", report.Installer.ArtifactVerified, report.Installer.InstallerExecuted, report.Installer.Lifecycle.PhaseStatus)
	fmt.Fprintf(&out, "Packaged MCP attestation: **%s**, server `%s` `%s`, protocol `%s`, tools `%d`, providers `%d`.\n\n",
		report.RuntimeAttestation.Result.PhaseStatus, markdownCell(report.RuntimeAttestation.ServerName), markdownCell(report.RuntimeAttestation.ServerVersion),
		markdownCell(report.RuntimeAttestation.ProtocolVersion), len(report.RuntimeAttestation.ToolNames), len(report.RuntimeAttestation.ProviderNames))

	out.WriteString("## 七语言 provider 结果\n\n")
	out.WriteString("| Provider | Overall | Runtime | Debug | 证据 | 原因 |\n|---|---|---|---|---|---|\n")
	for _, provider := range report.Providers {
		fmt.Fprintf(&out, "| `%s` | %s/%t | %s/%t | %s/%t | `%s` | %s |\n", provider.Provider,
			provider.Result.PhaseStatus, provider.Result.Attempted,
			provider.Runtime.PhaseStatus, provider.Runtime.Attempted, provider.Debug.PhaseStatus, provider.Debug.Attempted,
			markdownCell(provider.EvidencePath), markdownCell(provider.Reason))
	}

	out.WriteString("\n## 75 个 MCP 工具证据\n\n")
	out.WriteString("| 工具 | 场景/步骤 | 结论 | Outcome | 证据 | 原因 |\n|---|---|---|---|---|---|\n")
	for _, row := range report.ToolRows {
		fmt.Fprintf(&out, "| `%s` | `%s/%s` | %s/%t | `%s` | `%s` | %s |\n",
			row.Tool, row.ScenarioID, row.StepID, row.Result.PhaseStatus, row.Result.Attempted,
			markdownCell(row.Outcome), markdownCell(strings.Join(evidenceReferences(row.Result), ", ")), markdownCell(resultReason(row.Result)))
	}

	out.WriteString("\n## 场景、远端 Pipeline 与清理\n\n")
	for _, prerequisite := range report.Prerequisites {
		fmt.Fprintf(&out, "- campaign prerequisite `%s`: **%s**（attempted=%t），%s。\n", prerequisite.StepID, prerequisite.Result.PhaseStatus, prerequisite.Result.Attempted, markdownCell(resultReason(prerequisite.Result)))
	}
	for _, scenario := range report.Scenarios {
		fmt.Fprintf(&out, "- `%s`: **%s**（attempted=%t）", scenario.ID, scenario.Result.PhaseStatus, scenario.Result.Attempted)
		if scenario.ID == "remote-pipeline" {
			out.WriteString("（Windows → Linux Agent tunnel；A → B → A → cleanup）")
		}
		fmt.Fprintf(&out, "，步骤 %d，cleanup %d。\n", len(scenario.Steps), len(scenario.Cleanup))
	}
	fmt.Fprintf(&out, "\nCleanup 摘要：`%s`\n", markdownCell(CanonicalJSON(report.Cleanup)))

	if len(report.KnownAnomalies) > 0 {
		out.WriteString("\n## 冻结基线已知异常\n\n")
		for _, anomaly := range report.KnownAnomalies {
			fmt.Fprintf(&out, "- `%s`\n", markdownCell(CanonicalJSON(anomaly)))
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(out.String()), 0o644)
}

func buildReportSections(report CampaignReport) ReportSections {
	notRun := ReportSection{Result: notRunResult("not executed in this independent lane"), Reason: "not executed in this independent lane"}
	sections := ReportSections{
		MSIInstaller: notRun, NSISInstaller: notRun, Core: notRun,
		Environment: notRun, Providers: notRun, MCPTools: notRun, Pipeline: notRun,
		Cleanup: cleanupSection(report.Cleanup),
	}
	preinstallEvidencePath := filepath.ToSlash(filepath.Join("prepared-backup", PreparedEnvironmentPreinstallDirectory, PreparedEnvironmentPreinstallRecordFilename)) + ", " +
		filepath.ToSlash(filepath.Join("prepared-backup", PreparedEnvironmentPreinstallDirectory, EnvironmentPlanJSONFilename)) + ", " +
		filepath.ToSlash(filepath.Join("prepared-backup", PreparedEnvironmentPreinstallDirectory, EnvironmentManifestJSONFilename))
	if report.EnvironmentPreinstall != nil {
		reason := report.EnvironmentPreinstall.Record.Decision.Reason
		if strings.TrimSpace(reason) == "" {
			reason = resultReason(report.EnvironmentPreinstall.Record.Result)
		}
		sections.Environment = ReportSection{Result: report.EnvironmentPreinstall.Record.Result, EvidencePath: preinstallEvidencePath, Reason: reason}
	}
	if report.EnvironmentManifest != nil {
		reason := resultReason(report.EnvironmentManifest.Result)
		if report.EnvironmentAdmission != nil && strings.TrimSpace(report.EnvironmentAdmission.Reason) != "" {
			reason = report.EnvironmentAdmission.Reason
		}
		result := report.EnvironmentManifest.Result
		evidencePath := EnvironmentPlanJSONFilename + ", " + EnvironmentManifestJSONFilename + ", " + EnvironmentManifestMarkdownFilename
		if report.EnvironmentPreinstall != nil {
			result = aggregateResult("environment pre-install and post-install", 2, []ValidationResult{report.EnvironmentPreinstall.Record.Result, report.EnvironmentManifest.Result})
			evidencePath = preinstallEvidencePath + ", " + evidencePath
		}
		if report.EnvironmentComparison != nil {
			if environmentComparisonWasPersisted(report) {
				evidencePath += ", " + EnvironmentManifestComparisonFilename
			} else {
				evidencePath += ", campaign-report.json#environment_comparison"
			}
		}
		if report.EnvironmentComparisonPersistence != nil {
			result = aggregateResult("environment pre-install, post-install, and comparison persistence", 2, []ValidationResult{result, *report.EnvironmentComparisonPersistence})
			if report.EnvironmentComparisonPersistence.PhaseStatus != PhaseStatusPass {
				reason = resultReason(*report.EnvironmentComparisonPersistence)
			}
		}
		sections.Environment = ReportSection{Result: result, EvidencePath: evidencePath, Reason: reason}
	}
	if report.Lane == "msi_smoke" {
		sections.MSIInstaller = ReportSection{Result: report.Installer.Result, EvidencePath: strings.Join(evidenceReferences(report.Installer.Result), ", ")}
		return sections
	}
	if report.Lane != "nsis_core" && report.Lane != "core_only" {
		return sections
	}
	if report.Lane == "nsis_core" {
		sections.NSISInstaller = ReportSection{Result: report.Installer.Result, EvidencePath: strings.Join(evidenceReferences(report.Installer.Result), ", ")}
	}
	sections.Core = ReportSection{Result: resultOrNotRun(report.Functional, "core execution did not produce facts"), EvidencePath: "campaign-report.json", Reason: strings.TrimSpace(strings.Trim(strings.TrimSpace(report.FailureStage+": "+report.FailureReason), ":"))}
	sections.Providers = ReportSection{Result: aggregateProviderResult(report.Providers, report.RuntimeAttestation.ProviderNames), EvidencePath: "evidence/providers"}
	sections.MCPTools = ReportSection{Result: aggregateToolResult(report.ToolRows, report.RuntimeAttestation.ToolNames, report.ValidationCatalog.Coverage), EvidencePath: "campaign-report.json"}
	sections.Pipeline = pipelineSection(report.Scenarios)
	return sections
}

func environmentComparisonWasPersisted(report CampaignReport) bool {
	return report.EnvironmentComparisonPersistence != nil && report.EnvironmentComparisonPersistence.PhaseStatus == PhaseStatusPass
}

func aggregateProviderResult(providers []ProviderExecution, expectedNames []string) ValidationResult {
	if len(providers) != 7 {
		return attemptedResult(false, fmt.Sprintf("seven language provider coverage has %d rows, want exactly 7", len(providers)), "", "", nil)
	}
	seen := make(map[string]bool, len(providers))
	children := make([]ValidationResult, 0, len(providers))
	for _, provider := range providers {
		name := strings.TrimSpace(provider.Provider)
		if name == "" || seen[name] {
			return attemptedResult(false, fmt.Sprintf("seven language provider coverage contains blank or duplicate provider %q", name), "", "", nil)
		}
		seen[name] = true
		children = append(children, provider.Result)
	}
	if len(expectedNames) > 0 {
		if len(expectedNames) != 7 {
			return attemptedResult(false, fmt.Sprintf("runtime attestation has %d provider names, want exactly 7", len(expectedNames)), "", "", nil)
		}
		expected := append([]string{}, expectedNames...)
		actual := make([]string, 0, len(seen))
		for name := range seen {
			actual = append(actual, name)
		}
		sort.Strings(expected)
		sort.Strings(actual)
		if strings.Join(expected, "\x00") != strings.Join(actual, "\x00") {
			return attemptedResult(false, "seven language provider rows do not match the attested provider catalog", "", "", nil)
		}
	}
	return aggregateResult("seven language providers", 7, children)
}

func aggregateToolResult(rows []ToolEvidenceRow, expectedNames []string, expectedCoverage ...[]CoverageAssignment) ValidationResult {
	if len(rows) != 75 {
		return attemptedResult(false, fmt.Sprintf("75 MCP tool coverage has %d rows, want exactly 75", len(rows)), "", "", nil)
	}
	seen := make(map[string]bool, len(rows))
	children := make([]ValidationResult, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Tool)
		if name == "" || seen[name] {
			return attemptedResult(false, fmt.Sprintf("75 MCP tool coverage contains blank or duplicate tool %q", name), "", "", nil)
		}
		seen[name] = true
		children = append(children, row.Result)
	}
	if len(expectedNames) > 0 {
		if len(expectedNames) != 75 {
			return attemptedResult(false, fmt.Sprintf("runtime attestation has %d tool names, want exactly 75", len(expectedNames)), "", "", nil)
		}
		expected := append([]string{}, expectedNames...)
		actual := make([]string, 0, len(seen))
		for name := range seen {
			actual = append(actual, name)
		}
		sort.Strings(expected)
		sort.Strings(actual)
		if strings.Join(expected, "\x00") != strings.Join(actual, "\x00") {
			return attemptedResult(false, "75 MCP tool rows do not match the attested frozen tool catalog", "", "", nil)
		}
	}
	if len(expectedCoverage) > 0 {
		if err := validateToolRowCoverage(rows, expectedCoverage[0]); err != nil {
			return attemptedResult(false, err.Error(), "", "", nil)
		}
	}
	return aggregateResult("75 MCP tool coverage", 75, children)
}

func pipelineSection(scenarios []ScenarioExecution) ReportSection {
	for _, scenario := range scenarios {
		if scenario.ID == "remote-pipeline" {
			return ReportSection{Result: scenario.Result, EvidencePath: "evidence/remote-pipeline"}
		}
	}
	return ReportSection{Result: notRunResult("remote-pipeline scenario was not reached"), Reason: "remote-pipeline scenario was not reached"}
}

func cleanupSection(cleanup CleanupRecord) ReportSection {
	result, err := deriveCleanupResult(cleanup)
	if err != nil {
		result = attemptedResult(false, "invalid cleanup execution facts: "+err.Error(), "", "", nil)
	}
	section := ReportSection{Result: result, Reason: resultReason(result)}
	if result.Attempted {
		section.EvidencePath = "cleanup-report.json"
	}
	return section
}

func writeValidationSummary(resultsRoot string, redactor *Redactor, current CampaignReport) error {
	derived, err := rederiveCampaignReport(current)
	if err != nil {
		return fmt.Errorf("derive current campaign before summary: %w", err)
	}
	current = derived
	summary := current
	summary.Kind = "superdev.windows-validation.summary"
	summary.Lane = "aggregate"
	if current.Lane == "nsis_core" {
		if prior, found := latestLaneReport(resultsRoot, "msi_smoke", current.BuildCommit); found {
			summary.Sections.MSIInstaller = ReportSection{
				Result: prior.Sections.MSIInstaller.Result, EvidencePath: filepath.ToSlash(filepath.Join(prior.CampaignID, "campaign-report.json")),
				Reason: "independent MSI smoke lane final status",
			}
			summary.InstallerChecks = mergeInstallerChecks(prior.InstallerChecks, summary.InstallerChecks)
		} else {
			summary.Sections.MSIInstaller = ReportSection{Result: notRunResult("no completed MSI smoke report found for this frozen build"), Reason: "no completed MSI smoke report found for this frozen build"}
		}
	}
	summary.Result = aggregateSummaryResult(summary.Sections, current.Result)
	redacted := redactor.Redact(RawMessageMap(summary))
	if err := writeJSON(filepath.Join(resultsRoot, "validation-summary.json"), redacted); err != nil {
		return fmt.Errorf("write validation summary JSON: %w", err)
	}
	raw, err := json.Marshal(redacted)
	if err != nil {
		return fmt.Errorf("encode validation summary: %w", err)
	}
	var safeSummary CampaignReport
	if err := json.Unmarshal(raw, &safeSummary); err != nil {
		return fmt.Errorf("decode validation summary: %w", err)
	}
	return writeMarkdownReport(filepath.Join(resultsRoot, "validation-summary.md"), safeSummary)
}

func latestLaneReport(resultsRoot, lane, buildCommit string) (CampaignReport, bool) {
	entries, err := os.ReadDir(resultsRoot)
	if err != nil {
		return CampaignReport{}, false
	}
	var latest CampaignReport
	found := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var candidate CampaignReport
		if err := readJSONFile(filepath.Join(resultsRoot, entry.Name(), "campaign-report.json"), &candidate); err != nil {
			continue
		}
		candidate, err = rederiveCampaignReport(candidate)
		if err != nil {
			continue
		}
		if candidate.Lane != lane || candidate.BuildCommit != buildCommit {
			continue
		}
		if !found || candidate.FinishedAtUTC > latest.FinishedAtUTC {
			latest = candidate
			found = true
		}
	}
	return latest, found
}

func mergeInstallerChecks(left, right []PackageFileIdentity) []PackageFileIdentity {
	byPath := map[string]PackageFileIdentity{}
	for _, identity := range append(append([]PackageFileIdentity{}, left...), right...) {
		byPath[identity.Path] = identity
	}
	merged := make([]PackageFileIdentity, 0, len(byPath))
	for _, identity := range byPath {
		merged = append(merged, identity)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Path < merged[j].Path })
	return merged
}

func aggregateSummaryResult(sections ReportSections, currentCampaign ValidationResult) ValidationResult {
	children := make([]ValidationResult, 0, 8)
	for _, section := range []ReportSection{
		sections.MSIInstaller, sections.NSISInstaller, sections.Environment, sections.Core, sections.Providers,
		sections.MCPTools, sections.Pipeline, sections.Cleanup,
	} {
		children = append(children, section.Result)
	}
	// current campaign result 已包含 campaign/scenario/step prerequisite 的独立事实；
	// summary 再加入独立 MSI section，不能把嵌套前置失败降级成 BLOCKED。
	children = append(children, currentCampaign)
	return aggregateResult("final validation summary", len(children), children)
}

// FinalizeCampaignCleanup 把 Windows cleanup 证据合并回 lane 报告并重建最终聚合摘要。
//
// 参数：
//   - resultsRoot: campaign 结果根目录
//   - campaignID: Prepare 阶段冻结的 campaign 身份
//   - cleanupPath: 该 campaign 直属的 cleanup-report.json
//   - preparedBackup: Prepare-Validation.ps1 产生的精确基线目录
//
// 返回：
//   - 合并 cleanup 后的 campaign 报告
//   - 身份、读取、脱敏或报告重建错误
//
// 该入口只接受固定 campaign 直属的 cleanup-report.json，避免清理脚本用任意路径改写其他结果。
func FinalizeCampaignCleanup(resultsRoot, campaignID, cleanupPath, preparedBackup string) (report CampaignReport, finalizeErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationCleanupFinalize")
	stage := "validate_campaign_identity"
	lane := ""
	defer func() {
		if finalizeErr != nil {
			log.WithErr(finalizeErr).WithFields(map[string]any{"campaign_id": campaignID, "lane": lane, "stage": stage}).Error("Windows cleanup 最终证据合并失败")
		}
	}()
	log.WithFields(map[string]any{"results_root": resultsRoot, "campaign_id": campaignID}).Info("开始合并 Windows cleanup 最终证据")
	if !campaignIDPattern.MatchString(campaignID) {
		return CampaignReport{}, fmt.Errorf("invalid campaign id %q", campaignID)
	}
	stage = "resolve_paths"
	root, err := filepath.Abs(resultsRoot)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("resolve results root: %w", err)
	}
	campaignDir := filepath.Join(root, campaignID)
	wantCleanup := filepath.Join(campaignDir, "cleanup-report.json")
	actualCleanup, err := filepath.Abs(cleanupPath)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("resolve cleanup report path: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(actualCleanup), filepath.Clean(wantCleanup)) {
		return CampaignReport{}, fmt.Errorf("cleanup report must be the selected campaign direct child")
	}
	stage = "read_cleanup_report"
	var cleanup CleanupRecord
	if err := readJSONFile(actualCleanup, &cleanup); err != nil {
		return CampaignReport{}, fmt.Errorf("read cleanup report: %w", err)
	}
	if cleanup.Kind != "superdev.windows-validation.cleanup-report" || cleanup.CampaignID != campaignID {
		return CampaignReport{}, fmt.Errorf("cleanup report identity does not match campaign")
	}
	if cleanup.SchemaVersion != 2 {
		return CampaignReport{}, fmt.Errorf("cleanup report schema_version must be 2")
	}
	cleanupResult, err := deriveCleanupResult(cleanup)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("derive cleanup result: %w", err)
	}
	if !cleanupResult.Attempted || (cleanupResult.PhaseStatus != PhaseStatusPass && cleanupResult.PhaseStatus != PhaseStatusFail) {
		return CampaignReport{}, fmt.Errorf("cleanup report must contain completed attempted execution facts")
	}
	stage = "read_campaign_report"
	if err := readJSONFile(filepath.Join(campaignDir, "campaign-report.json"), &report); err != nil {
		return CampaignReport{}, fmt.Errorf("read campaign report: %w", err)
	}
	if report.CampaignID != campaignID {
		return CampaignReport{}, fmt.Errorf("campaign report identity mismatch")
	}
	lane = report.Lane
	stage = "rederive_persisted_campaign"
	report, err = rederiveCampaignReport(report)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive persisted campaign report: %w", err)
	}
	stage = "verify_prepared_backup_identity"
	backupDirectory, preparedManifest, err := loadPreparedBackupIdentity(preparedBackup, campaignID, report.Lane)
	if err != nil {
		return CampaignReport{}, err
	}
	stage = "verify_prepared_baseline"
	verificationStarted := time.Now().UTC()
	baselineErr := verifyPreparedBaselineIntegrity(backupDirectory, preparedManifest, cleanup)
	verificationFinished := time.Now().UTC()
	if baselineErr != nil && cleanupResult.PhaseStatus == PhaseStatusPass {
		return CampaignReport{}, baselineErr
	}
	verificationFailure := ""
	if baselineErr != nil {
		verificationFailure = baselineErr.Error()
		log.WithErr(baselineErr).WithFields(map[string]any{"campaign_id": campaignID, "lane": report.Lane, "stage": stage}).Error("prepared baseline 完整性失败已并入 cleanup FAIL 报告")
	}
	verificationResult := attemptedResult(baselineErr == nil, verificationFailure, verificationStarted.Format(time.RFC3339Nano), verificationFinished.Format(time.RFC3339Nano), []EvidenceRecord{{
		Name: "prepared_baseline_verification", Required: true, Present: true, Ref: "campaign-report.json#prerequisites/prepared-baseline-verification",
	}})
	var verificationInline map[string]any
	if baselineErr != nil {
		verificationInline = map[string]any{"error": verificationFailure, "prepared_baseline_sha256": cleanup.PreparedBaselineSHA256}
	}
	report.Prerequisites = upsertStepExecution(report.Prerequisites, StepExecution{
		StepID: "prepared-baseline-verification", Coverage: CoverageSupporting, Result: verificationResult,
		InlineEvidence: verificationInline,
	})
	stage = "merge_installer_lifecycle"
	lifecycleDirectory := filepath.Join(backupDirectory, "installer-lifecycle")
	factCount, factInspectErr := inspectInstallerLifecycleFactFiles(lifecycleDirectory)
	if factInspectErr != nil {
		return CampaignReport{}, fmt.Errorf("inspect installer lifecycle facts during cleanup finalize: %w", factInspectErr)
	}
	if factCount > 0 {
		// baseline 完整性已作为独立 FAIL 事实时仍需把 cleanup 报告固化；只有摘要
		// 完整的 prepared 输入才有资格继续证明 lifecycle 平台/clean-baseline 合同。
		if baselineErr == nil {
			if err := validateCleanInstallerBaseline(filepath.Join(backupDirectory, "baseline.json")); err != nil {
				return CampaignReport{}, fmt.Errorf("verify installer lifecycle prepared baseline: %w", err)
			}
		}
		factBinding, readErr := readInstallerLifecycleFactBinding(backupDirectory)
		if readErr != nil {
			return CampaignReport{}, fmt.Errorf("read installer lifecycle during cleanup finalize: %w", readErr)
		}
		binding, bindingErr := installerLifecycleBindingFromReport(report, preparedManifest, factBinding.InstallDirectory)
		if bindingErr != nil {
			return CampaignReport{}, fmt.Errorf("bind installer lifecycle during cleanup finalize: %w", bindingErr)
		}
		verified, verifyErr := loadVerifiedInstallerLifecycleFacts(backupDirectory, binding, resultInput(report.Installer.Artifact))
		if verifyErr != nil {
			return CampaignReport{}, fmt.Errorf("verify installer lifecycle during cleanup finalize: %w", verifyErr)
		}
		report.Installer = verified.Execution
		log.WithFields(map[string]any{
			"campaign_id": campaignID,
			"lane":        report.Lane,
			"actions":     len(verified.Evidence),
			"status":      report.Installer.Lifecycle.PhaseStatus,
		}).Info("prepared installer lifecycle facts 已并入 cleanup 最终报告")
	} else if report.Lane != "core_only" {
		// 四动作事实不存在时必须回落到明确 NOT_RUN；持久化报告里的旧 PASS 不是动作事实源。
		report.Installer, err = installerExecutionWithoutRecordedLifecycle(
			report.Lane,
			resultInput(report.Installer.Artifact),
			"prepared installer lifecycle action facts were not found during cleanup finalization",
		)
		if err != nil {
			return CampaignReport{}, fmt.Errorf("derive missing installer lifecycle during cleanup finalize: %w", err)
		}
		log.WithFields(map[string]any{"campaign_id": campaignID, "lane": report.Lane}).Error("prepared installer lifecycle 缺失，installer 保持 NOT_RUN")
	}
	report.Cleanup = cleanup
	report, err = rederiveCampaignReport(report)
	if err != nil {
		return CampaignReport{}, fmt.Errorf("rederive finalized campaign report: %w", err)
	}
	redactor := NewRedactor()
	stage = "write_campaign_report"
	if err := writeCampaignReports(campaignDir, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "write_validation_summary"
	if err := writeValidationSummary(root, redactor, report); err != nil {
		return CampaignReport{}, err
	}
	stage = "complete"
	log.WithFields(map[string]any{"campaign_id": campaignID, "lane": report.Lane, "phase_status": report.Result.PhaseStatus, "cleanup_status": report.Sections.Cleanup.Result.PhaseStatus}).Info("Windows cleanup 最终证据合并完成")
	return report, nil
}

func verifyPreparedBaseline(preparedBackup, campaignID, lane string, cleanup CleanupRecord) error {
	backupDirectory, manifest, err := loadPreparedBackupIdentity(preparedBackup, campaignID, lane)
	if err != nil {
		return err
	}
	return verifyPreparedBaselineIntegrity(backupDirectory, manifest, cleanup)
}

func loadPreparedBackupIdentity(preparedBackup, campaignID, lane string) (string, preparedBackupManifest, error) {
	backupDirectory, err := filepath.Abs(preparedBackup)
	if err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("resolve prepared backup: %w", err)
	}
	var manifest preparedBackupManifest
	if err := readJSONFile(filepath.Join(backupDirectory, "backup-manifest.json"), &manifest); err != nil {
		return "", preparedBackupManifest{}, fmt.Errorf("read prepared backup manifest: %w", err)
	}
	if err := validatePreparedBackupIdentity(manifest, campaignID); err != nil {
		return "", preparedBackupManifest{}, err
	}
	if manifest.Lane != lane {
		return "", preparedBackupManifest{}, fmt.Errorf("prepared backup lane %q does not match campaign lane %q", manifest.Lane, lane)
	}
	return backupDirectory, manifest, nil
}

func verifyPreparedBaselineIntegrity(backupDirectory string, manifest preparedBackupManifest, cleanup CleanupRecord) error {
	if err := validatePreparedBaselineManifest(manifest); err != nil {
		return err
	}
	baselinePath := filepath.Join(backupDirectory, "baseline.json")
	baselineBytes, err := os.ReadFile(baselinePath)
	if err != nil {
		return fmt.Errorf("read prepared baseline: %w", err)
	}
	actualDigest := fmt.Sprintf("%x", sha256.Sum256(baselineBytes))
	if actualDigest != manifest.BaselineSHA256 {
		return fmt.Errorf("prepared manifest and baseline.json SHA-256 are not identical")
	}
	cleanupSucceeded := cleanup.ExecutionFacts != nil && cleanup.ExecutionFacts.Succeeded
	if cleanupSucceeded && cleanup.PreparedBaselineSHA256 != actualDigest {
		return fmt.Errorf("cleanup report is not bound to the prepared baseline SHA-256")
	}
	if cleanup.PreparedBaselineSHA256 != "" && cleanup.PreparedBaselineSHA256 != actualDigest {
		return fmt.Errorf("cleanup report references a different prepared baseline SHA-256")
	}
	var baselineIdentity struct {
		SchemaVersion int    `json:"schema_version"`
		Kind          string `json:"kind"`
	}
	if err := json.Unmarshal(baselineBytes, &baselineIdentity); err != nil {
		return fmt.Errorf("decode prepared baseline: %w", err)
	}
	if baselineIdentity.SchemaVersion != 1 || baselineIdentity.Kind != "superdev.windows-validation.machine-baseline" {
		return fmt.Errorf("prepared baseline identity is invalid")
	}
	preparedCategoryDigests, err := preparedBaselineCategoryDigests(baselineBytes)
	if err != nil {
		return err
	}
	for _, category := range cleanupBaselineCategories {
		if manifest.BaselineCategorySHA256[category] != preparedCategoryDigests[category] {
			return fmt.Errorf("prepared manifest category %q is not derived from baseline.json", category)
		}
	}
	if cleanupSucceeded && cleanup.BaselineComparison == nil {
		return fmt.Errorf("cleanup report has no final baseline comparison")
	}
	if cleanup.BaselineComparison == nil {
		return nil
	}
	for _, check := range cleanup.BaselineComparison.Checks {
		if manifest.BaselineCategorySHA256[check.Category] != check.ExpectedSHA256 {
			return fmt.Errorf("cleanup category %q is not bound to the prepared baseline", check.Category)
		}
	}
	return nil
}

func upsertStepExecution(steps []StepExecution, replacement StepExecution) []StepExecution {
	for index := range steps {
		if steps[index].StepID == replacement.StepID {
			steps[index] = replacement
			return steps
		}
	}
	return append(steps, replacement)
}

func preparedBaselineCategoryDigests(baselineBytes []byte) (map[string]string, error) {
	var baseline map[string]json.RawMessage
	if err := json.Unmarshal(baselineBytes, &baseline); err != nil {
		return nil, fmt.Errorf("decode prepared baseline categories: %w", err)
	}
	digests := make(map[string]string, len(cleanupBaselineCategories))
	for _, category := range cleanupBaselineCategories {
		raw, ok := baseline[category]
		if !ok {
			return nil, fmt.Errorf("prepared baseline is missing category %q", category)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return nil, fmt.Errorf("compact prepared baseline category %q: %w", category, err)
		}
		digests[category] = fmt.Sprintf("%x", sha256.Sum256(compact.Bytes()))
	}
	return digests, nil
}

func resultOrNotRun(result ValidationResult, reason string) ValidationResult {
	if result.PhaseStatus == "" {
		return notRunResult(reason)
	}
	return result
}

func deriveCampaignCompletionResult(name string, report CampaignReport) ValidationResult {
	children := make([]ValidationResult, 0, 4+len(report.Prerequisites))
	children = append(children,
		resultOrNotRun(report.Installer.Result, "installer execution did not produce facts"),
		resultOrNotRun(report.RuntimeAttestation.Result, "runtime attestation did not produce facts"),
		resultOrNotRun(report.Functional, "functional execution did not produce facts"),
	)
	children = appendPrerequisiteResults(children, report.Prerequisites)
	if report.EnvironmentManifest != nil {
		children = append(children, report.EnvironmentManifest.Result)
	}
	for _, scenario := range report.Scenarios {
		children = appendPrerequisiteResults(children, scenario.Prerequisites)
		for _, step := range scenario.Steps {
			children = appendPrerequisiteResults(children, step.Prerequisites)
		}
		for _, step := range scenario.Cleanup {
			children = appendPrerequisiteResults(children, step.Prerequisites)
		}
	}
	children = append(children, report.Sections.Cleanup.Result)
	return aggregateResult(name, len(children), children)
}

func appendPrerequisiteResults(children []ValidationResult, prerequisites []StepExecution) []ValidationResult {
	for _, prerequisite := range prerequisites {
		children = append(children, prerequisite.Result)
		children = appendPrerequisiteResults(children, prerequisite.Prerequisites)
	}
	return children
}

func evidenceReferences(result ValidationResult) []string {
	seen := map[string]bool{}
	references := make([]string, 0, len(result.Evidence))
	for _, record := range result.Evidence {
		ref := strings.TrimSpace(record.Ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		references = append(references, ref)
	}
	sort.Strings(references)
	return references
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
