// report_test.go 验证 authoritative summary.json 是最后一次原子写入。
//
// 职责：覆盖 evidence manifest、半写 summary、secret 残留与旧结果隔离。
// 边界：不从 summary.md 或旧 campaign 目录推断 PASS。
package runtimevalidation

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportWritesEvidenceAndSummaryJSONLast(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "results", "campaign with spaces")
	summary := validReportSummary()
	written, err := WriteReport(root, ReportInput{
		Summary:  summary,
		Evidence: map[string]any{"coverage.json": summary.Coverage, "providers.json": summary.Languages},
	})
	require.NoError(t, err)
	require.True(t, written.Complete)
	require.FileExists(t, filepath.Join(root, "evidence-manifest.json"))
	require.FileExists(t, filepath.Join(root, "summary.md"))
	require.FileExists(t, filepath.Join(root, "summary.json"))

	loaded, err := LoadAuthoritativeSummary(filepath.Join(root, "summary.json"))
	require.NoError(t, err)
	require.Equal(t, StatusPass, loaded.Verdict.Status)
	require.NotEmpty(t, loaded.EvidenceManifestSHA256)
}

func TestReportRefusesSecretAndLeavesNoAuthoritativeSummary(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "campaign")
	_, err := WriteReport(root, ReportInput{
		Summary: validReportSummary(), Evidence: map[string]any{"unsafe.json": map[string]any{"value": "human-secret"}},
		ForbiddenSecrets: []string{"human-secret"},
	})
	require.ErrorContains(t, err, "secret")
	require.NoFileExists(t, filepath.Join(root, "summary.json"))
}

func TestAuthoritativeSummaryRejectsHalfWriteAndOldCampaignIsIsolated(t *testing.T) {
	t.Parallel()

	results := t.TempDir()
	oldRoot := filepath.Join(results, "old")
	require.NoError(t, os.MkdirAll(oldRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(oldRoot, "summary.json"), []byte(`{"schema_version":1,"kind":"superdev.runtime-validation.summary","complete":true,"verdict":{"status":"PASS","complete":true}}`), 0o600))
	newRoot := filepath.Join(results, "new")
	require.NoError(t, os.MkdirAll(newRoot, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(newRoot, "summary.json"), []byte(`{"schema_version":1,"kind":"superdev.runtime-validation.summary","complete":false}`), 0o600))

	_, err := LoadAuthoritativeSummary(filepath.Join(newRoot, "summary.json"))
	require.ErrorContains(t, err, "complete")
	_, err = WriteReport(oldRoot, ReportInput{Summary: validReportSummary()})
	require.ErrorContains(t, err, "already exists")
}

func validReportSummary() Summary {
	applicationOK := true
	return Summary{
		CampaignID: "campaign-1", Target: Target{OS: "darwin", Architecture: "arm64"},
		Host:            HostIdentity{OS: "darwin", Architecture: "arm64", Native: true, DetectionSource: "sysctl"},
		Bundle:          BundleReceipt{Target: Target{OS: "darwin", Architecture: "arm64"}, ManifestSHA256: "abc", FileCount: 5},
		LiveTools:       []string{"list_projects"},
		Coverage:        CoverageReport{Complete: true, LiveToolCount: 1, PrimaryCount: 1, Assignments: []CoverageAssignment{{Tool: "list_projects", ScenarioID: "identity", StepID: "list"}}},
		PrimaryEvidence: []ToolEvidence{{CampaignID: "campaign-1", ScenarioID: "identity", StepID: "list", Tool: "list_projects", Outcome: ExpectedOutcomeSuccess, ApplicationOK: &applicationOK, Assertions: []AssertionResult{{Path: "structuredContent.data.count", Passed: true}}}},
		Languages:       []LanguageResult{}, Cleanup: CleanupResult{JournalComplete: true, PipelineTerminal: true, RemoteRootAbsent: true, BorrowedTopologyStable: true, ActiveMarkerRemoved: true},
		Verdict: Verdict{Status: StatusPass, Complete: true},
	}
}
