// result.go 定义 Windows 真机验证唯一的执行事实、证据义务与状态派生合同。
//
// 职责：
//   - 从目标动作的原始执行事实与 required evidence 派生 Phase Status
//   - 聚合 step、scenario、provider、installer 与 report section 的子结果
//   - 保持 Artifact Verification 与 Installer Lifecycle 两组事实互不替代
//
// 边界：
//   - 不执行外部调用、不写证据文件，也不渲染报告
//   - 不允许调用方从 Phase Status 反推或补造执行事实
//   - 不提供通用工作流、恢复协议或状态机平台
package windowsvalidation

import (
	"fmt"
	"strings"
)

// PhaseStatus 是 Windows 验证动作由事实和证据唯一派生的阶段状态。
//
// 取值：
//   - NOT_RUN: 目标动作未尝试，且没有具名 prerequisite 阻断
//   - BLOCKED: 具名 prerequisite 在目标动作前阻断，目标动作未尝试
//   - PASS: 目标动作已尝试、行为成功且 required evidence 完整
//   - FAIL: 目标动作已尝试后失败，或尝试后的 required evidence 不完整
type PhaseStatus string

const (
	// PhaseStatusNotRun 表示目标动作未尝试且没有具名 prerequisite 阻断。
	PhaseStatusNotRun PhaseStatus = "NOT_RUN"
	// PhaseStatusBlocked 表示具名 prerequisite 在目标动作前阻断了执行。
	PhaseStatusBlocked PhaseStatus = "BLOCKED"
	// PhaseStatusPass 表示目标动作及其 required evidence 均满足合同。
	PhaseStatusPass PhaseStatus = "PASS"
	// PhaseStatusFail 表示已尝试动作或其 required evidence 未满足合同。
	PhaseStatusFail PhaseStatus = "FAIL"
)

// ExecutionFacts 是一个目标动作产生的原始事实，不包含任何人工填写的最终状态。
type ExecutionFacts struct {
	Attempted     bool   `json:"attempted"`
	Succeeded     bool   `json:"succeeded"`
	BlockedBy     string `json:"blocked_by,omitempty"`
	Failure       string `json:"failure,omitempty"`
	NotRunReason  string `json:"not_run_reason,omitempty"`
	StartedAtUTC  string `json:"started_at_utc,omitempty"`
	FinishedAtUTC string `json:"finished_at_utc,omitempty"`
}

// EvidenceRecord 描述一个执行动作必须或可选保留的脱敏证据义务。
type EvidenceRecord struct {
	Name       string `json:"name"`
	Required   bool   `json:"required"`
	Present    bool   `json:"present"`
	Ref        string `json:"ref,omitempty"`
	WriteError string `json:"write_error,omitempty"`
}

// ResultInput 把一个目标动作的 Execution Facts 与 Evidence Obligation 交给派生模块。
type ResultInput struct {
	Facts    ExecutionFacts
	Evidence []EvidenceRecord
}

// ValidationResult 是所有 Windows 验证单元共用的事实与派生状态合同。
//
// PhaseStatus 只能由 DeriveValidationResult 或 DeriveAggregateResult 产生；
// Attempted 等字段保留原始事实，禁止从 PhaseStatus 反向补写。
type ValidationResult struct {
	PhaseStatus   PhaseStatus      `json:"phase_status"`
	Attempted     bool             `json:"attempted"`
	Succeeded     bool             `json:"succeeded"`
	BlockedBy     string           `json:"blocked_by,omitempty"`
	Failure       string           `json:"failure,omitempty"`
	NotRunReason  string           `json:"not_run_reason,omitempty"`
	StartedAtUTC  string           `json:"started_at_utc,omitempty"`
	FinishedAtUTC string           `json:"finished_at_utc,omitempty"`
	Evidence      []EvidenceRecord `json:"evidence"`
}

// InstallerExecutionFacts 提交一个安装器 lane 的文件身份与四段生命周期原始事实。
type InstallerExecutionFacts struct {
	Format            string `json:"format"`
	ArtifactVerified  bool   `json:"artifact_verified"`
	InstallerExecuted bool   `json:"installer_executed"`
	Artifact          ResultInput
	Install           ResultInput
	Start             ResultInput
	Stop              ResultInput
	Uninstall         ResultInput
}

// InstallerExecution 保存安装包文件身份、四段生命周期及其独立聚合结果。
type InstallerExecution struct {
	Format            string           `json:"format"`
	ArtifactVerified  bool             `json:"artifact_verified"`
	InstallerExecuted bool             `json:"installer_executed"`
	Artifact          ValidationResult `json:"artifact"`
	Install           ValidationResult `json:"install"`
	Start             ValidationResult `json:"start"`
	Stop              ValidationResult `json:"stop"`
	Uninstall         ValidationResult `json:"uninstall"`
	Lifecycle         ValidationResult `json:"lifecycle"`
	Result            ValidationResult `json:"result"`
}

// DeriveValidationResult 从原始执行事实与证据义务派生唯一的 Phase Status。
//
// 参数：
//   - input: 一个目标动作的 Execution Facts 与 Evidence Obligation
//
// 返回：
//   - 保留全部输入事实和证据记录的统一 ValidationResult
//   - 事实自相矛盾或 required evidence 记录无效时的合同错误
//
// 注意：未尝试动作不会因缺少证据变成 FAIL；required evidence 义务只在尝试后生效。
func DeriveValidationResult(input ResultInput) (ValidationResult, error) {
	facts := input.Facts
	if !facts.Attempted && facts.Succeeded {
		return ValidationResult{}, fmt.Errorf("unattempted action cannot be successful")
	}
	if facts.Attempted && strings.TrimSpace(facts.BlockedBy) != "" {
		return ValidationResult{}, fmt.Errorf("attempted action cannot be blocked by prerequisite %q", facts.BlockedBy)
	}
	if !facts.Attempted && (strings.TrimSpace(facts.StartedAtUTC) != "" || strings.TrimSpace(facts.FinishedAtUTC) != "") {
		return ValidationResult{}, fmt.Errorf("unattempted action cannot have execution timestamps")
	}
	if facts.Succeeded && strings.TrimSpace(facts.Failure) != "" {
		return ValidationResult{}, fmt.Errorf("successful action cannot also contain failure %q", facts.Failure)
	}
	if strings.TrimSpace(facts.BlockedBy) != "" && strings.TrimSpace(facts.NotRunReason) != "" {
		return ValidationResult{}, fmt.Errorf("blocked action cannot also be an unclassified NOT_RUN")
	}
	if facts.Attempted && strings.TrimSpace(facts.NotRunReason) != "" {
		return ValidationResult{}, fmt.Errorf("attempted action cannot contain not_run_reason")
	}
	if !facts.Attempted && strings.TrimSpace(facts.BlockedBy) == "" && strings.TrimSpace(facts.Failure) != "" {
		return ValidationResult{}, fmt.Errorf("unattempted unblocked action cannot contain a failure")
	}

	evidence := append([]EvidenceRecord{}, input.Evidence...)
	if evidence == nil {
		evidence = []EvidenceRecord{}
	}
	for index, record := range evidence {
		if strings.TrimSpace(record.Name) == "" {
			return ValidationResult{}, fmt.Errorf("evidence %d has no name", index)
		}
		if record.Present && strings.TrimSpace(record.Ref) == "" {
			return ValidationResult{}, fmt.Errorf("evidence %d (%s) is present without a reference", index, record.Name)
		}
		if record.Present && strings.TrimSpace(record.WriteError) != "" {
			return ValidationResult{}, fmt.Errorf("evidence %d (%s) cannot be present after a write error", index, record.Name)
		}
	}
	result := ValidationResult{
		Attempted: facts.Attempted, Succeeded: facts.Succeeded,
		BlockedBy: facts.BlockedBy, Failure: facts.Failure, NotRunReason: facts.NotRunReason,
		StartedAtUTC: facts.StartedAtUTC, FinishedAtUTC: facts.FinishedAtUTC, Evidence: evidence,
	}
	if !facts.Attempted {
		if strings.TrimSpace(facts.BlockedBy) != "" {
			result.PhaseStatus = PhaseStatusBlocked
		} else {
			result.PhaseStatus = PhaseStatusNotRun
		}
		return result, nil
	}
	if !facts.Succeeded {
		result.PhaseStatus = PhaseStatusFail
		if strings.TrimSpace(result.Failure) == "" {
			result.Failure = "attempted action did not satisfy its behavior contract"
		}
		return result, nil
	}
	for _, record := range evidence {
		if !record.Required {
			continue
		}
		if !record.Present || strings.TrimSpace(record.WriteError) != "" {
			result.PhaseStatus = PhaseStatusFail
			result.Failure = requiredEvidenceFailure(record)
			return result, nil
		}
	}
	result.PhaseStatus = PhaseStatusPass
	return result, nil
}

// DeriveAggregateResult 把同一验收面的子结果按统一优先级聚合为一个 ValidationResult。
//
// 参数：
//   - name: 用于错误与未执行原因的稳定验收面名称
//   - expected: 该验收面必须存在的子结果数量
//   - children: 已产生的子 ValidationResult
//
// 返回：
//   - FAIL 优先于 BLOCKED，BLOCKED 优先于 NOT_RUN，且仅全部 PASS 才 PASS 的聚合结果
//   - expected 非法、子结果超量或包含未知 Phase Status 时的合同错误
//
// 注意：混合 PASS/NOT_RUN 不会被提升为成功；缺失 required child 且已有执行时按覆盖义务失败。
func DeriveAggregateResult(name string, expected int, children []ValidationResult) (ValidationResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ValidationResult{}, fmt.Errorf("aggregate name is required")
	}
	if expected < 0 {
		return ValidationResult{}, fmt.Errorf("aggregate %s expected count cannot be negative", name)
	}
	if len(children) > expected {
		return ValidationResult{}, fmt.Errorf("aggregate %s has %d children, expected at most %d", name, len(children), expected)
	}
	if len(children) == 0 {
		return deriveKnown(ResultInput{Facts: ExecutionFacts{NotRunReason: name + " has no execution records"}}), nil
	}

	var firstBlocked ValidationResult
	var firstNotRun ValidationResult
	for _, child := range children {
		switch child.PhaseStatus {
		case PhaseStatusFail:
			return deriveKnown(ResultInput{Facts: ExecutionFacts{Attempted: true, Failure: name + " contains a failed required child"}}), nil
		case PhaseStatusBlocked:
			if firstBlocked.PhaseStatus == "" {
				firstBlocked = child
			}
		case PhaseStatusNotRun:
			if firstNotRun.PhaseStatus == "" {
				firstNotRun = child
			}
		case PhaseStatusPass:
		default:
			return ValidationResult{}, fmt.Errorf("aggregate %s contains unknown phase status %q", name, child.PhaseStatus)
		}
	}
	if len(children) < expected {
		return deriveKnown(ResultInput{Facts: ExecutionFacts{Attempted: true, Failure: fmt.Sprintf("%s has %d of %d required children", name, len(children), expected)}}), nil
	}
	if firstBlocked.PhaseStatus != "" {
		blockedBy := firstBlocked.BlockedBy
		if strings.TrimSpace(blockedBy) == "" {
			blockedBy = name + " prerequisite"
		}
		return deriveKnown(ResultInput{Facts: ExecutionFacts{BlockedBy: blockedBy, Failure: resultReason(firstBlocked)}}), nil
	}
	if firstNotRun.PhaseStatus != "" {
		reason := firstNotRun.NotRunReason
		if strings.TrimSpace(reason) == "" {
			reason = name + " contains an unreached required child"
		}
		return deriveKnown(ResultInput{Facts: ExecutionFacts{NotRunReason: reason}}), nil
	}
	return deriveKnown(ResultInput{Facts: ExecutionFacts{Attempted: true, Succeeded: true}}), nil
}

// DeriveInstallerExecution 独立派生安装包文件身份、安装器生命周期和 lane 总结果。
//
// 参数：
//   - facts: 显式的 artifact_verified、installer_executed 与五个动作输入
//
// 返回：
//   - 文件身份与 install/start/stop/uninstall 均可独立复查的 InstallerExecution
//   - 布尔事实与动作事实矛盾，或任一子结果无效时的合同错误
//
// 注意：Artifact Verification 即使 PASS，也不能把未执行的 Installer Lifecycle 提升为 PASS。
func DeriveInstallerExecution(facts InstallerExecutionFacts) (InstallerExecution, error) {
	if strings.TrimSpace(facts.Format) == "" {
		return InstallerExecution{}, fmt.Errorf("installer format is required")
	}
	artifact, err := DeriveValidationResult(facts.Artifact)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("derive installer artifact: %w", err)
	}
	install, err := DeriveValidationResult(facts.Install)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("derive installer install phase: %w", err)
	}
	start, err := DeriveValidationResult(facts.Start)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("derive installer start phase: %w", err)
	}
	stop, err := DeriveValidationResult(facts.Stop)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("derive installer stop phase: %w", err)
	}
	uninstall, err := DeriveValidationResult(facts.Uninstall)
	if err != nil {
		return InstallerExecution{}, fmt.Errorf("derive installer uninstall phase: %w", err)
	}
	if facts.ArtifactVerified != (artifact.PhaseStatus == PhaseStatusPass) {
		return InstallerExecution{}, fmt.Errorf("artifact_verified=%v contradicts artifact phase_status=%s", facts.ArtifactVerified, artifact.PhaseStatus)
	}
	lifecycleChildren := []ValidationResult{install, start, stop, uninstall}
	if !facts.InstallerExecuted {
		for _, child := range lifecycleChildren {
			if child.Attempted {
				return InstallerExecution{}, fmt.Errorf("installer_executed=false contradicts attempted lifecycle action")
			}
		}
	} else if !install.Attempted {
		return InstallerExecution{}, fmt.Errorf("installer_executed=true requires an attempted install action")
	}
	lifecycle, err := DeriveAggregateResult(facts.Format+" installer lifecycle", len(lifecycleChildren), lifecycleChildren)
	if err != nil {
		return InstallerExecution{}, err
	}
	result, err := DeriveAggregateResult(facts.Format+" installer lane", 2, []ValidationResult{artifact, lifecycle})
	if err != nil {
		return InstallerExecution{}, err
	}
	return InstallerExecution{
		Format: facts.Format, ArtifactVerified: facts.ArtifactVerified, InstallerExecuted: facts.InstallerExecuted,
		Artifact: artifact, Install: install, Start: start, Stop: stop, Uninstall: uninstall,
		Lifecycle: lifecycle, Result: result,
	}, nil
}

func requiredEvidenceFailure(record EvidenceRecord) string {
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = "unnamed evidence"
	}
	if strings.TrimSpace(record.WriteError) != "" {
		return fmt.Sprintf("required evidence %s write failed: %s", name, record.WriteError)
	}
	return fmt.Sprintf("required evidence %s is missing", name)
}

func deriveKnown(input ResultInput) ValidationResult {
	result, err := DeriveValidationResult(input)
	if err != nil {
		panic(err)
	}
	return result
}

func resultReason(result ValidationResult) string {
	for _, value := range []string{result.Failure, result.NotRunReason, result.BlockedBy} {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return string(result.PhaseStatus)
}

func notRunResult(reason string) ValidationResult {
	return deriveKnown(ResultInput{Facts: ExecutionFacts{NotRunReason: reason}})
}

func blockedResult(prerequisite, reason string) ValidationResult {
	return deriveKnown(ResultInput{Facts: ExecutionFacts{BlockedBy: prerequisite, Failure: reason}})
}

func attemptedResult(succeeded bool, failure, startedAtUTC, finishedAtUTC string, evidence []EvidenceRecord) ValidationResult {
	return deriveKnown(ResultInput{Facts: ExecutionFacts{
		Attempted: true, Succeeded: succeeded, Failure: failure,
		StartedAtUTC: startedAtUTC, FinishedAtUTC: finishedAtUTC,
	}, Evidence: evidence})
}

func aggregateResult(name string, expected int, children []ValidationResult) ValidationResult {
	result, err := DeriveAggregateResult(name, expected, children)
	if err != nil {
		panic(err)
	}
	return result
}

func withEvidence(result ValidationResult, evidence ...EvidenceRecord) ValidationResult {
	input := resultInput(result)
	input.Evidence = append(input.Evidence, evidence...)
	return deriveKnown(input)
}

func resultInput(result ValidationResult) ResultInput {
	return ResultInput{Facts: ExecutionFacts{
		Attempted: result.Attempted, Succeeded: result.Succeeded,
		BlockedBy: result.BlockedBy, Failure: result.Failure, NotRunReason: result.NotRunReason,
		StartedAtUTC: result.StartedAtUTC, FinishedAtUTC: result.FinishedAtUTC,
	}, Evidence: append([]EvidenceRecord{}, result.Evidence...)}
}
