// cleanup.go 定义 Windows cleanup 原始执行事实与基线比较合同。
//
// 职责：
//   - 用类型化 DTO 承载 Cleanup-Validation.ps1 的原始事实
//   - 从 cleanup execution facts 与报告证据派生统一 Validation Result
//   - 表达机器基线逐类是否匹配，而不在 PowerShell 中生成 Phase Status
//
// 边界：
//   - 不执行恢复、卸载或文件删除
//   - 不从基线恢复反推安装器卸载动作已经发生
//   - 不接受 cleanup 调用方填写最终 PASS/FAIL
package windowsvalidation

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	cleanupBaselineCategories = []string{
		"superdev_processes", "listening_port_57017", "install_paths",
		"uninstall_entries", "connector_files", "user_state",
	}
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// CleanupBaselineCheck 是一个机器基线分类的原始摘要比较事实。
type CleanupBaselineCheck struct {
	Category       string `json:"category"`
	Matched        bool   `json:"matched"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256"`
}

// CleanupBaselineComparison 保存 cleanup 后所有机器基线分类的原始匹配事实。
type CleanupBaselineComparison struct {
	Matched bool                   `json:"matched"`
	Checks  []CleanupBaselineCheck `json:"checks"`
}

// CleanupRecord 是 campaign 内嵌或独立 cleanup-report.json 共用的 schema v2 DTO。
//
// ExecutionFacts 为 nil 表示旧调用方没有提交 cleanup 事实；这种记录只能派生 NOT_RUN。
type CleanupRecord struct {
	SchemaVersion                    int                        `json:"schema_version,omitempty"`
	Kind                             string                     `json:"kind,omitempty"`
	CampaignID                       string                     `json:"campaign_id,omitempty"`
	PreparedBaselineSHA256           string                     `json:"prepared_baseline_sha256,omitempty"`
	ExecutionFacts                   *ExecutionFacts            `json:"execution_facts,omitempty"`
	Reason                           string                     `json:"reason,omitempty"`
	Error                            string                     `json:"error,omitempty"`
	CampaignWorkspace                string                     `json:"campaign_workspace,omitempty"`
	CampaignWorkspaceRemoved         bool                       `json:"campaign_workspace_removed"`
	CampaignResultsRemoved           bool                       `json:"campaign_results_removed"`
	UserStateRestored                bool                       `json:"user_state_restored"`
	RestoredStateFileCount           int                        `json:"restored_state_file_count"`
	BaselineComparison               *CleanupBaselineComparison `json:"baseline_comparison,omitempty"`
	ValidationStateQuarantineRemoved bool                       `json:"validation_state_quarantine_removed"`
	RecoveryQuarantine               string                     `json:"recovery_quarantine,omitempty"`
	FinishedAtUTC                    string                     `json:"finished_at_utc,omitempty"`
}

func pendingCleanupRecord(reason, campaignWorkspace string) CleanupRecord {
	facts := ExecutionFacts{NotRunReason: reason}
	return CleanupRecord{ExecutionFacts: &facts, Reason: reason, CampaignWorkspace: campaignWorkspace}
}

func deriveCleanupResult(cleanup CleanupRecord) (ValidationResult, error) {
	if cleanup.ExecutionFacts == nil {
		reason := strings.TrimSpace(cleanup.Reason)
		if reason == "" {
			reason = "cleanup was not executed"
		}
		return DeriveValidationResult(ResultInput{Facts: ExecutionFacts{NotRunReason: reason}})
	}
	facts := *cleanup.ExecutionFacts
	if facts.Attempted && (strings.TrimSpace(facts.StartedAtUTC) == "" || strings.TrimSpace(facts.FinishedAtUTC) == "") {
		return ValidationResult{}, fmt.Errorf("attempted cleanup facts require start and finish timestamps")
	}
	if facts.Succeeded {
		if !cleanup.CampaignWorkspaceRemoved || !cleanup.UserStateRestored || !cleanup.ValidationStateQuarantineRemoved {
			return ValidationResult{}, fmt.Errorf("successful cleanup facts require workspace removal, user-state restore, and quarantine removal")
		}
		if !sha256Pattern.MatchString(cleanup.PreparedBaselineSHA256) {
			return ValidationResult{}, fmt.Errorf("successful cleanup facts require the prepared baseline SHA-256")
		}
		if cleanup.BaselineComparison == nil || !cleanup.BaselineComparison.Matched || len(cleanup.BaselineComparison.Checks) != len(cleanupBaselineCategories) {
			return ValidationResult{}, fmt.Errorf("successful cleanup facts require a matched final baseline comparison")
		}
		expectedCategories := make(map[string]bool, len(cleanupBaselineCategories))
		for _, category := range cleanupBaselineCategories {
			expectedCategories[category] = true
		}
		seen := make(map[string]bool, len(cleanup.BaselineComparison.Checks))
		for _, check := range cleanup.BaselineComparison.Checks {
			if !expectedCategories[check.Category] || seen[check.Category] {
				return ValidationResult{}, fmt.Errorf("successful cleanup has unknown or duplicate baseline category %q", check.Category)
			}
			seen[check.Category] = true
			if !check.Matched {
				return ValidationResult{}, fmt.Errorf("successful cleanup contradicts unmatched baseline category %q", check.Category)
			}
			if !sha256Pattern.MatchString(check.ExpectedSHA256) || !sha256Pattern.MatchString(check.ActualSHA256) || check.ExpectedSHA256 != check.ActualSHA256 {
				return ValidationResult{}, fmt.Errorf("successful cleanup category %q requires equal lowercase SHA-256 facts", check.Category)
			}
		}
	}
	evidence := []EvidenceRecord{}
	if facts.Attempted {
		evidence = append(evidence, EvidenceRecord{Name: "cleanup_report", Required: true, Present: true, Ref: "cleanup-report.json"})
	}
	return DeriveValidationResult(ResultInput{Facts: facts, Evidence: evidence})
}

func validatePreparedBackupManifest(manifest preparedBackupManifest, campaignID string) error {
	if err := validatePreparedBackupIdentity(manifest, campaignID); err != nil {
		return err
	}
	return validatePreparedBaselineManifest(manifest)
}

func validatePreparedBackupIdentity(manifest preparedBackupManifest, campaignID string) error {
	if manifest.SchemaVersion != 1 || manifest.Kind != "superdev.windows-validation.prepared-backup" || manifest.Status != "ready" || manifest.CampaignID != campaignID {
		return fmt.Errorf("prepared backup identity or readiness does not match campaign")
	}
	if strings.TrimSpace(manifest.CreatedAtUTC) == "" {
		return fmt.Errorf("prepared backup creation time is missing")
	}
	switch manifest.Lane {
	case "msi_smoke", "nsis_core", "core_only":
	default:
		return fmt.Errorf("prepared backup lane %q is invalid", manifest.Lane)
	}
	return nil
}

func validatePreparedBaselineManifest(manifest preparedBackupManifest) error {
	if !sha256Pattern.MatchString(manifest.BaselineSHA256) {
		return fmt.Errorf("prepared backup baseline SHA-256 is invalid")
	}
	if len(manifest.BaselineCategorySHA256) != len(cleanupBaselineCategories) {
		return fmt.Errorf("prepared backup has %d baseline category hashes, want %d", len(manifest.BaselineCategorySHA256), len(cleanupBaselineCategories))
	}
	for _, category := range cleanupBaselineCategories {
		if !sha256Pattern.MatchString(manifest.BaselineCategorySHA256[category]) {
			return fmt.Errorf("prepared backup category %q SHA-256 is invalid", category)
		}
	}
	return nil
}
