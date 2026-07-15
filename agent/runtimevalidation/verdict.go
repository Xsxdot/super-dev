// verdict.go 从严格硬门槛、primary evidence 与 cleanup 事实派生唯一最终结论。
//
// 职责：
//   - 保证 FAIL 优先于 BLOCKED，且只有全部条件满足才 PASS
//   - 拒绝漏/重 primary、策略拒绝、空断言、旧 marker 和 residual 判绿
//   - 为 CLI 退出码和 authoritative summary 提供同一判定入口
//
// 边界：
//   - 不执行场景、清理资源或写报告
//   - 不从文本日志猜测事实，也不接受调用方直接填写最终 PASS
package runtimevalidation

import (
	"fmt"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// DeriveVerdict 从已完成的 strict facts 派生唯一总 verdict。
//
// 参数：
//   - input: coverage、硬门槛、primary evidence、active marker 与 cleanup 事实
//
// 返回：
//   - FAIL 优先于 BLOCKED、仅全绿才 complete 的 verdict
//   - 输入状态自相矛盾或 NOT_RUN 没有具名上游时的合同错误
//
// 注意：旧 active marker 形成 BLOCKED；本次 cleanup residual 是已执行后的 FAIL。
func DeriveVerdict(input VerdictInput) (Verdict, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationVerdict")
	log.WithFields(map[string]any{
		"check_count": len(input.Checks), "live_tool_count": input.Coverage.LiveToolCount,
		"primary_count": input.Coverage.PrimaryCount, "evidence_count": len(input.PrimaryEvidence),
	}).Info("开始聚合 runtime validation strict verdict")

	checks, err := validateChecks(input.Checks)
	if err != nil {
		log.WithErr(err).Error("runtime validation 硬门槛事实无效")
		return Verdict{}, err
	}
	failures := make([]Cause, 0)
	blockers := make([]Cause, 0)
	notRuns := make([]Cause, 0)
	counts := VerdictCounts{}
	for _, check := range checks {
		switch check.Status {
		case StatusPass:
			counts.Pass++
		case StatusFail:
			counts.Fail++
			failures = append(failures, normalizedCause(check.Cause, "check_failed", check.ID+" failed", check.ID))
		case StatusBlocked:
			counts.Blocked++
			blockers = append(blockers, normalizedCause(check.Cause, "check_blocked", check.ID+" blocked", check.ID))
		case StatusNotRun:
			counts.NotRun++
			notRuns = append(notRuns, normalizedCause(check.Cause, "not_run", check.ID+" was not run after "+check.Upstream, check.ID))
		}
	}

	if input.ActiveMarker != nil {
		blockers = append(blockers, Cause{
			Code: "active_marker_present", Source: "campaign-state",
			Message: fmt.Sprintf("campaign %s remains active at %s; reset the dedicated validation environment", input.ActiveMarker.CampaignID, input.ActiveMarker.ClonePath),
		})
	}
	if !input.Coverage.Complete {
		failures = append(failures, Cause{
			Code: "tool_coverage_drift", Source: "coverage",
			Message: fmt.Sprintf("live/primary mismatch missing=%v unexpected=%v duplicate=%v", input.Coverage.MissingPrimary, input.Coverage.UnexpectedPrimary, input.Coverage.DuplicatePrimary),
		})
	}
	if err := validatePrimaryEvidence(input.Coverage.Assignments, input.PrimaryEvidence); err != nil {
		failures = append(failures, Cause{Code: "primary_evidence_invalid", Message: err.Error(), Source: "evidence"})
	}
	if !input.EvidenceComplete {
		failures = append(failures, Cause{Code: "evidence_incomplete", Message: "required redacted evidence is incomplete", Source: "evidence"})
	}
	if cause := cleanupFailure(input.Cleanup); cause.Code != "" {
		failures = append(failures, cause)
	}

	verdict := Verdict{Counts: counts}
	switch {
	case len(failures) > 0:
		verdict.Status = StatusFail
		verdict.RootCause = failures[0]
	case len(blockers) > 0:
		verdict.Status = StatusBlocked
		verdict.RootCause = blockers[0]
	case len(notRuns) > 0:
		verdict.Status = StatusNotRun
		verdict.RootCause = notRuns[0]
	default:
		verdict.Status = StatusPass
		verdict.Complete = true
	}
	if verdict.Status == StatusPass {
		log.WithFields(map[string]any{"status": verdict.Status, "pass_count": verdict.Counts.Pass}).Info("runtime validation strict verdict 聚合成功")
		return verdict, nil
	}
	log.WithFields(map[string]any{"status": verdict.Status, "cause_code": verdict.RootCause.Code, "cause_source": verdict.RootCause.Source}).Error("runtime validation strict verdict 未通过")
	return verdict, nil
}

func validateChecks(checks []CheckResult) (map[string]CheckResult, error) {
	result := make(map[string]CheckResult, len(checks))
	for _, check := range checks {
		check.ID = strings.TrimSpace(check.ID)
		if check.ID == "" {
			return nil, fmt.Errorf("check id is required")
		}
		if _, ok := result[check.ID]; ok {
			return nil, fmt.Errorf("check %s is duplicated", check.ID)
		}
		switch check.Status {
		case StatusPass, StatusFail, StatusBlocked:
		case StatusNotRun:
			if strings.TrimSpace(check.Upstream) == "" {
				return nil, fmt.Errorf("check %s is NOT_RUN without a named upstream", check.ID)
			}
		default:
			return nil, fmt.Errorf("check %s has unknown status %q", check.ID, check.Status)
		}
		result[check.ID] = check
	}
	for _, check := range result {
		if check.Status != StatusNotRun {
			continue
		}
		upstream, ok := result[check.Upstream]
		if !ok || (upstream.Status != StatusFail && upstream.Status != StatusBlocked) {
			return nil, fmt.Errorf("check %s is NOT_RUN but upstream %s is not FAIL/BLOCKED", check.ID, check.Upstream)
		}
	}
	return result, nil
}

func validatePrimaryEvidence(assignments []CoverageAssignment, evidence []ToolEvidence) error {
	wanted := make(map[string]CoverageAssignment, len(assignments))
	for _, assignment := range assignments {
		wanted[assignment.Tool] = assignment
	}
	counts := map[string]int{}
	for _, item := range evidence {
		assignment, ok := wanted[item.Tool]
		if !ok {
			return fmt.Errorf("tool %s has primary evidence without manifest assignment", item.Tool)
		}
		counts[item.Tool]++
		if item.ScenarioID != assignment.ScenarioID || item.StepID != assignment.StepID {
			return fmt.Errorf("tool %s evidence is not bound to manifest primary %s/%s", item.Tool, assignment.ScenarioID, assignment.StepID)
		}
		if strings.TrimSpace(item.CampaignID) == "" {
			return fmt.Errorf("tool %s evidence has no campaign correlation", item.Tool)
		}
		if item.Outcome != ExpectedOutcomeSuccess || item.IsError || (item.ApplicationOK != nil && !*item.ApplicationOK) {
			return fmt.Errorf("tool %s primary outcome is not exact success", item.Tool)
		}
		if len(item.Assertions) == 0 {
			return fmt.Errorf("tool %s primary evidence has no assertions", item.Tool)
		}
		for _, assertion := range item.Assertions {
			if !assertion.Passed {
				return fmt.Errorf("tool %s assertion %s failed: %s", item.Tool, assertion.Path, assertion.Failure)
			}
		}
	}
	for tool := range wanted {
		if counts[tool] != 1 {
			return fmt.Errorf("tool %s has %d primary evidence records, want exactly 1", tool, counts[tool])
		}
	}
	return nil
}

func cleanupFailure(cleanup CleanupResult) Cause {
	switch {
	case len(cleanup.Residuals) > 0:
		return Cause{Code: "cleanup_residual", Message: fmt.Sprintf("cleanup left residual resources: %v", cleanup.Residuals), Source: "cleanup"}
	case !cleanup.PipelineTerminal:
		return Cause{Code: "pipeline_not_terminal", Message: "pipeline execution did not reach a confirmed terminal state", Source: "cleanup"}
	case !cleanup.RemoteRootAbsent:
		return Cause{Code: "remote_root_present", Message: "remote campaign root still exists", Source: "cleanup"}
	case !cleanup.JournalComplete:
		return Cause{Code: "cleanup_journal_incomplete", Message: "cleanup journal contains unreleased resources", Source: "cleanup"}
	case !cleanup.BorrowedTopologyStable:
		return Cause{Code: "borrowed_topology_drift", Message: "borrowed topology changed during campaign", Source: "cleanup"}
	case !cleanup.ActiveMarkerRemoved:
		return Cause{Code: "active_marker_not_removed", Message: "active marker remains after cleanup", Source: "cleanup"}
	default:
		return Cause{}
	}
}

func normalizedCause(cause Cause, code, message, source string) Cause {
	if strings.TrimSpace(cause.Code) == "" {
		cause.Code = code
	}
	if strings.TrimSpace(cause.Message) == "" {
		cause.Message = message
	}
	if strings.TrimSpace(cause.Source) == "" {
		cause.Source = source
	}
	return cause
}
