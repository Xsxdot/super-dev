// verdict_test.go 验证 strict 总结论只能由完整 coverage、真实成功证据与清理事实派生。
//
// 职责：
//   - 锁定 FAIL 高于 BLOCKED、只有全部硬门槛满足才 PASS
//   - 拒绝无根因 NOT_RUN、旧 marker、残留资源和策略拒绝判绿
//
// 边界：
//   - 不渲染报告，不执行清理，也不伪造外部运行事实
package runtimevalidation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveVerdictPassesOnlyCompleteStrictRun(t *testing.T) {
	t.Parallel()

	verdict, err := DeriveVerdict(validVerdictInput())
	require.NoError(t, err)
	require.Equal(t, StatusPass, verdict.Status)
	require.True(t, verdict.Complete)
}

func TestDeriveVerdictRefusesFalseGreenInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*VerdictInput)
		wantStatus Status
	}{
		{
			name: "coverage drift",
			mutate: func(input *VerdictInput) {
				input.Coverage.Complete = false
				input.Coverage.MissingPrimary = []string{"new_tool"}
			},
			wantStatus: StatusFail,
		},
		{
			name: "primary policy denial",
			mutate: func(input *VerdictInput) {
				input.PrimaryEvidence[0].Outcome = "operation_denied"
			},
			wantStatus: StatusFail,
		},
		{
			name: "primary empty assertions",
			mutate: func(input *VerdictInput) {
				input.PrimaryEvidence[0].Assertions = nil
			},
			wantStatus: StatusFail,
		},
		{
			name: "old active marker",
			mutate: func(input *VerdictInput) {
				input.ActiveMarker = &ActiveMarker{CampaignID: "old-campaign", ClonePath: "/tmp/old"}
			},
			wantStatus: StatusBlocked,
		},
		{
			name: "cleanup residual",
			mutate: func(input *VerdictInput) {
				input.Cleanup.Residuals = []Residual{{Kind: "process", ID: "pid-42"}}
			},
			wantStatus: StatusFail,
		},
		{
			name: "failed check wins over blocker",
			mutate: func(input *VerdictInput) {
				input.Checks[1] = CheckResult{ID: "native-host", Status: StatusBlocked, Cause: Cause{Code: "host_mismatch", Message: "wrong arch"}}
				input.Checks[0] = CheckResult{ID: "bundle", Status: StatusFail, Cause: Cause{Code: "bundle_drift", Message: "hash mismatch"}}
			},
			wantStatus: StatusFail,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validVerdictInput()
			test.mutate(&input)
			verdict, err := DeriveVerdict(input)
			require.NoError(t, err)
			require.Equal(t, test.wantStatus, verdict.Status)
			require.False(t, verdict.Complete)
			require.NotEmpty(t, verdict.RootCause.Code)
		})
	}
}

func TestDeriveVerdictRejectsNotRunWithoutNamedUpstream(t *testing.T) {
	t.Parallel()

	input := validVerdictInput()
	input.Checks = append(input.Checks, CheckResult{ID: "browser", Status: StatusNotRun})

	_, err := DeriveVerdict(input)
	require.ErrorContains(t, err, "NOT_RUN")
}

func validVerdictInput() VerdictInput {
	return VerdictInput{
		Coverage: CoverageReport{
			Complete: true, LiveToolCount: 1, PrimaryCount: 1,
			Assignments: []CoverageAssignment{{Tool: "list_projects", ScenarioID: "identity", StepID: "primary"}},
		},
		Checks: []CheckResult{
			{ID: "bundle", Status: StatusPass},
			{ID: "native-host", Status: StatusPass},
			{ID: "providers", Status: StatusPass},
		},
		PrimaryEvidence: []ToolEvidence{{
			CampaignID: "campaign-1", ScenarioID: "identity", StepID: "primary", Tool: "list_projects",
			Outcome: ExpectedOutcomeSuccess, Assertions: []AssertionResult{{Path: "structuredContent.projects", Passed: true}},
		}},
		Cleanup: CleanupResult{
			JournalComplete: true, PipelineTerminal: true, RemoteRootAbsent: true,
			BorrowedTopologyStable: true, ActiveMarkerRemoved: true,
		},
		EvidenceComplete: true,
	}
}
