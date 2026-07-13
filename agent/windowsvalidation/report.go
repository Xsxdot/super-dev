// report.go 把结构化 campaign 结果渲染为便于人工复查的 Markdown 报告。
//
// 职责：
//   - 分开展示 installer、core、provider、75 工具、pipeline 与 cleanup 结果
//   - 保留每一条 BLOCKED/FAIL 和证据相对路径
//
// 边界：
//   - 不重新计算 verdict 或用总分掩盖局部失败
//   - 不写入请求、凭据或完整 MCP 响应
package windowsvalidation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

var campaignIDPattern = regexp.MustCompile(`^w10x64-[0-9a-f]{7}-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$`)

func writeMarkdownReport(path string, report CampaignReport) error {
	var out strings.Builder
	title := "SuperDev Windows 10 x64 真实验证报告"
	if report.Kind == "superdev.windows-validation.summary" {
		title = "SuperDev Windows 10 x64 最终聚合验收摘要"
	}
	fmt.Fprintf(&out, "# %s\n\n", title)
	fmt.Fprintf(&out, "- Campaign: `%s`\n", markdownCell(report.CampaignID))
	fmt.Fprintf(&out, "- 冻结构建: `%s` / `%s`\n", markdownCell(report.BuildCommit), markdownCell(report.ProductVersion))
	fmt.Fprintf(&out, "- 执行 lane: `%s`\n", markdownCell(report.Lane))
	fmt.Fprintf(&out, "- 总状态: **%s**\n", report.Status)
	fmt.Fprintf(&out, "- Windows 功能状态: **%s**\n", report.FunctionalStatus)
	fmt.Fprintf(&out, "- 时间: `%s` — `%s`\n\n", report.StartedAtUTC, report.FinishedAtUTC)

	out.WriteString("## 固定验收面\n\n")
	out.WriteString("| 验收面 | 状态 | 证据 | 原因 |\n|---|---|---|---|\n")
	for _, section := range []struct {
		name  string
		value ReportSection
	}{
		{"MSI installer lane", report.Sections.MSIInstaller},
		{"NSIS installer lane", report.Sections.NSISInstaller},
		{"Core", report.Sections.Core},
		{"七语言 provider", report.Sections.Providers},
		{"75 MCP tools", report.Sections.MCPTools},
		{"Remote pipeline", report.Sections.Pipeline},
		{"Cleanup / baseline restore", report.Sections.Cleanup},
	} {
		fmt.Fprintf(&out, "| %s | %s | `%s` | %s |\n", section.name, section.value.Status, markdownCell(section.value.EvidencePath), markdownCell(section.value.Reason))
	}
	out.WriteString("\n")

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
	fmt.Fprintf(&out, "\nPackaged MCP attestation: **%s**, server `%s` `%s`, protocol `%s`, tools `%d`, providers `%d`.\n\n",
		report.RuntimeAttestation.Verdict, markdownCell(report.RuntimeAttestation.ServerName), markdownCell(report.RuntimeAttestation.ServerVersion),
		markdownCell(report.RuntimeAttestation.ProtocolVersion), len(report.RuntimeAttestation.ToolNames), len(report.RuntimeAttestation.ProviderNames))

	out.WriteString("## 七语言 provider 结果\n\n")
	out.WriteString("| Provider | Runtime | Debug | 证据 | 原因 |\n|---|---|---|---|---|\n")
	for _, provider := range report.Providers {
		fmt.Fprintf(&out, "| `%s` | %s | %s | `%s` | %s |\n", provider.Provider, provider.RuntimeVerdict, provider.DebugVerdict, markdownCell(provider.EvidencePath), markdownCell(provider.Reason))
	}

	out.WriteString("\n## 75 个 MCP 工具证据\n\n")
	out.WriteString("| 工具 | 场景/步骤 | 结论 | Outcome | 证据 | 原因 |\n|---|---|---|---|---|---|\n")
	for _, row := range report.ToolRows {
		fmt.Fprintf(&out, "| `%s` | `%s/%s` | %s | `%s` | `%s` | %s |\n",
			row.Tool, row.ScenarioID, row.StepID, row.Verdict, markdownCell(row.Outcome), markdownCell(row.EvidencePath), markdownCell(row.Error))
	}

	out.WriteString("\n## 场景、远端 Pipeline 与清理\n\n")
	for _, scenario := range report.Scenarios {
		fmt.Fprintf(&out, "- `%s`: **%s**", scenario.ID, scenario.Verdict)
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
	notRun := ReportSection{Status: verdictBlocked, Reason: "not executed in this independent lane"}
	sections := ReportSections{
		MSIInstaller: notRun, NSISInstaller: notRun, Core: notRun,
		Providers: notRun, MCPTools: notRun, Pipeline: notRun,
		Cleanup: cleanupSection(report.Cleanup),
	}
	functionalStatus := report.FunctionalStatus
	if functionalStatus == "" {
		functionalStatus = report.Status
	}
	if report.Lane == "msi_smoke" {
		sections.MSIInstaller = ReportSection{Status: installerSectionStatus(report), EvidencePath: "runtime-attestation.json"}
		return sections
	}
	sections.NSISInstaller = ReportSection{Status: installerSectionStatus(report), EvidencePath: "runtime-attestation.json"}
	sections.Core = ReportSection{Status: functionalStatus, EvidencePath: "campaign-report.json", Reason: strings.TrimSpace(strings.Trim(strings.TrimSpace(report.FailureStage+": "+report.FailureReason), ":"))}
	sections.Providers = ReportSection{Status: aggregateProviderStatus(report.Providers), EvidencePath: "evidence/providers"}
	sections.MCPTools = ReportSection{Status: aggregateToolStatus(report.ToolRows), EvidencePath: "campaign-report.json"}
	sections.Pipeline = pipelineSection(report.Scenarios)
	return sections
}

func installerSectionStatus(report CampaignReport) string {
	if report.RuntimeAttestation.Verdict != verdictPass || len(report.InstallerChecks) != 1 {
		return verdictFail
	}
	return verdictPass
}

func aggregateProviderStatus(providers []ProviderExecution) string {
	if len(providers) != 7 {
		return verdictBlocked
	}
	status := verdictPass
	for _, provider := range providers {
		if provider.RuntimeVerdict == verdictFail || provider.DebugVerdict == verdictFail {
			return verdictFail
		}
		if provider.RuntimeVerdict == verdictBlocked || provider.DebugVerdict == verdictBlocked {
			status = verdictBlocked
		}
	}
	return status
}

func aggregateToolStatus(rows []ToolEvidenceRow) string {
	if len(rows) != 75 {
		return verdictBlocked
	}
	status := verdictPass
	for _, row := range rows {
		if row.Verdict == verdictFail {
			return verdictFail
		}
		if row.Verdict == verdictBlocked {
			status = verdictBlocked
		}
	}
	return status
}

func pipelineSection(scenarios []ScenarioExecution) ReportSection {
	for _, scenario := range scenarios {
		if scenario.ID == "remote-pipeline" {
			return ReportSection{Status: scenario.Verdict, EvidencePath: "evidence/remote-pipeline"}
		}
	}
	return ReportSection{Status: verdictBlocked, Reason: "remote-pipeline scenario was not reached"}
}

func cleanupSection(cleanup map[string]any) ReportSection {
	status := strings.ToUpper(fmt.Sprint(cleanup["status"]))
	switch status {
	case verdictPass, verdictFail:
		return ReportSection{Status: status, EvidencePath: "cleanup-report.json", Reason: fmt.Sprint(cleanup["error"])}
	default:
		return ReportSection{Status: verdictBlocked, Reason: fmt.Sprint(cleanup["reason"])}
	}
}

func writeValidationSummary(resultsRoot string, redactor *Redactor, current CampaignReport) error {
	summary := current
	summary.Kind = "superdev.windows-validation.summary"
	summary.Lane = "aggregate"
	if current.Lane == "nsis_core" {
		if prior, found := latestLaneReport(resultsRoot, "msi_smoke", current.BuildCommit); found {
			summary.Sections.MSIInstaller = ReportSection{
				Status: prior.Status, EvidencePath: filepath.ToSlash(filepath.Join(prior.CampaignID, "campaign-report.json")),
				Reason: "independent MSI smoke lane final status",
			}
			summary.InstallerChecks = mergeInstallerChecks(prior.InstallerChecks, summary.InstallerChecks)
		} else {
			summary.Sections.MSIInstaller = ReportSection{Status: verdictBlocked, Reason: "no completed MSI smoke report found for this frozen build"}
		}
	}
	summary.Status = aggregateSummaryStatus(summary.Sections)
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

func aggregateSummaryStatus(sections ReportSections) string {
	status := verdictPass
	for _, section := range []ReportSection{
		sections.MSIInstaller, sections.NSISInstaller, sections.Core, sections.Providers,
		sections.MCPTools, sections.Pipeline, sections.Cleanup,
	} {
		if section.Status == verdictFail {
			return verdictFail
		}
		if section.Status != verdictPass {
			status = verdictBlocked
		}
	}
	return status
}

// FinalizeCampaignCleanup 把 Windows cleanup 证据合并回 lane 报告并重建最终聚合摘要。
//
// 参数：
//   - resultsRoot: campaign 结果根目录
//   - campaignID: Prepare 阶段冻结的 campaign 身份
//   - cleanupPath: 该 campaign 直属的 cleanup-report.json
//
// 返回：
//   - 合并 cleanup 后的 campaign 报告
//   - 身份、读取、脱敏或报告重建错误
//
// 该入口只接受固定 campaign 直属的 cleanup-report.json，避免清理脚本用任意路径改写其他结果。
func FinalizeCampaignCleanup(resultsRoot, campaignID, cleanupPath string) (report CampaignReport, finalizeErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationCleanupFinalize")
	stage := "validate_campaign_identity"
	defer func() {
		if finalizeErr != nil {
			log.WithErr(finalizeErr).WithFields(map[string]any{"campaign_id": campaignID, "stage": stage}).Error("Windows cleanup 最终证据合并失败")
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
	var cleanup map[string]any
	if err := readJSONFile(actualCleanup, &cleanup); err != nil {
		return CampaignReport{}, fmt.Errorf("read cleanup report: %w", err)
	}
	if cleanup["kind"] != "superdev.windows-validation.cleanup-report" || fmt.Sprint(cleanup["campaign_id"]) != campaignID {
		return CampaignReport{}, fmt.Errorf("cleanup report identity does not match campaign")
	}
	cleanupStatus := strings.ToUpper(fmt.Sprint(cleanup["status"]))
	if cleanupStatus != verdictPass && cleanupStatus != verdictFail {
		return CampaignReport{}, fmt.Errorf("cleanup report status must be PASS or FAIL")
	}
	stage = "read_campaign_report"
	if err := readJSONFile(filepath.Join(campaignDir, "campaign-report.json"), &report); err != nil {
		return CampaignReport{}, fmt.Errorf("read campaign report: %w", err)
	}
	if report.CampaignID != campaignID {
		return CampaignReport{}, fmt.Errorf("campaign report identity mismatch")
	}
	report.Cleanup = cleanup
	report.Sections = buildReportSections(report)
	if cleanupStatus == verdictFail {
		report.Status = verdictFail
	} else {
		report.Status = report.FunctionalStatus
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
	log.WithFields(map[string]any{"campaign_id": campaignID, "status": report.Status, "cleanup_status": cleanupStatus}).Info("Windows cleanup 最终证据合并完成")
	return report, nil
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
