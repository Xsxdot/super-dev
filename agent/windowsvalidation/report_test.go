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
	"path/filepath"
	"testing"
)

func TestFinalizeCampaignCleanupRebuildsCompleteAggregateSummary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	redactor := NewRedactor()
	msiID := "w10x64-e3cc94f-20260713T010101Z-a1b2c3"
	msi := CampaignReport{
		SchemaVersion: 1, Kind: "superdev.windows-validation.campaign-report", CampaignID: msiID,
		Status: verdictPass, FunctionalStatus: verdictPass, BuildCommit: "e3cc94f", Lane: "msi_smoke",
		RuntimeAttestation: RuntimeAttestation{Verdict: verdictPass},
		InstallerChecks:    []PackageFileIdentity{{Path: "frozen.msi"}},
		Cleanup:            map[string]any{"status": verdictPass}, FinishedAtUTC: "2026-07-13T01:01:01Z",
	}
	msi.Sections = buildReportSections(msi)
	if err := writeCampaignReports(filepath.Join(root, msiID), redactor, msi); err != nil {
		t.Fatal(err)
	}

	nsisID := "w10x64-e3cc94f-20260713T020202Z-d4e5f6"
	providers := make([]ProviderExecution, 7)
	for index := range providers {
		providers[index] = ProviderExecution{Provider: "provider", RuntimeVerdict: verdictPass, DebugVerdict: verdictPass}
	}
	rows := make([]ToolEvidenceRow, 75)
	for index := range rows {
		rows[index] = ToolEvidenceRow{Tool: "tool", Verdict: verdictPass}
	}
	nsis := CampaignReport{
		SchemaVersion: 1, Kind: "superdev.windows-validation.campaign-report", CampaignID: nsisID,
		Status: verdictBlocked, FunctionalStatus: verdictPass, BuildCommit: "e3cc94f", Lane: "nsis_core",
		RuntimeAttestation: RuntimeAttestation{Verdict: verdictPass},
		InstallerChecks:    []PackageFileIdentity{{Path: "frozen.exe"}}, Providers: providers, ToolRows: rows,
		Scenarios: []ScenarioExecution{{ID: "remote-pipeline", Verdict: verdictPass}},
		Cleanup:   map[string]any{"status": "PENDING"}, FinishedAtUTC: "2026-07-13T02:02:02Z",
	}
	nsis.Sections = buildReportSections(nsis)
	nsisDir := filepath.Join(root, nsisID)
	if err := writeCampaignReports(nsisDir, redactor, nsis); err != nil {
		t.Fatal(err)
	}
	cleanupPath := filepath.Join(nsisDir, "cleanup-report.json")
	if err := writeJSON(cleanupPath, map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.cleanup-report",
		"campaign_id": nsisID, "status": verdictPass,
	}); err != nil {
		t.Fatal(err)
	}
	final, err := FinalizeCampaignCleanup(root, nsisID, cleanupPath)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != verdictPass {
		t.Fatalf("final campaign status=%s, want PASS", final.Status)
	}
	var summary CampaignReport
	if err := readJSONFile(filepath.Join(root, "validation-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != verdictPass || summary.Sections.MSIInstaller.Status != verdictPass || summary.Sections.Cleanup.Status != verdictPass {
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
		SchemaVersion: 1, Kind: "superdev.windows-validation.campaign-report", CampaignID: campaignID,
		Status: verdictPass, FunctionalStatus: verdictPass, BuildCommit: "e3cc94f", Lane: "msi_smoke",
		RuntimeAttestation: RuntimeAttestation{Verdict: verdictPass},
		InstallerChecks:    []PackageFileIdentity{{Path: "frozen.msi"}},
		Cleanup:            map[string]any{"status": verdictPass}, FinishedAtUTC: "2026-07-13T03:03:03Z",
	}
	report.Sections = buildReportSections(report)
	if err := writeCampaignReports(directory, NewRedactor(), report); err != nil {
		t.Fatal(err)
	}
	cleanupPath := filepath.Join(directory, "cleanup-report.json")
	if err := writeJSON(cleanupPath, map[string]any{
		"schema_version": 1, "kind": "superdev.windows-validation.cleanup-report",
		"campaign_id": campaignID, "status": verdictFail, "error": "baseline drift",
	}); err != nil {
		t.Fatal(err)
	}
	final, err := FinalizeCampaignCleanup(root, campaignID, cleanupPath)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != verdictFail || final.Sections.Cleanup.Status != verdictFail {
		t.Fatalf("cleanup failure retained stale PASS: status=%s cleanup=%s", final.Status, final.Sections.Cleanup.Status)
	}
	var summary CampaignReport
	if err := readJSONFile(filepath.Join(root, "validation-summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Status != verdictFail {
		t.Fatalf("summary status=%s, want FAIL", summary.Status)
	}
}
