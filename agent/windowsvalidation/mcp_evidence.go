// mcp_evidence.go 统一保存 Windows 验证中 supporting MCP 调用的事实证据。
//
// 职责：
//   - 为前置检查和 runtime attestation 记录脱敏 request、normalized response、错误与时间
//   - 把证据写入结果目录，并显式返回 required/present/ref/write_error 义务
//
// 边界：
//   - 不判断业务动作是否成功，也不直接生成 Phase Status
//   - primary 75 工具的精选证据仍由 scenario executor 按冻结合同生成
package windowsvalidation

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/xsxdot/gokit/logger"
)

type mcpEvidenceAttempt struct {
	Operation          string         `json:"operation"`
	Tool               string         `json:"tool,omitempty"`
	Request            map[string]any `json:"request"`
	NormalizedResponse any            `json:"normalized_response,omitempty"`
	TransportError     string         `json:"transport_error,omitempty"`
	ProductError       string         `json:"product_error,omitempty"`
	AssertionError     string         `json:"assertion_error,omitempty"`
	StartedAtUTC       string         `json:"started_at_utc"`
	FinishedAtUTC      string         `json:"finished_at_utc"`
}

func observeToolCall(ctx context.Context, client mcpToolCaller, tool string, arguments map[string]any) (ToolCallResult, mcpEvidenceAttempt, error) {
	started := time.Now().UTC()
	result, err := client.CallTool(ctx, tool, arguments)
	finished := time.Now().UTC()
	attempt := mcpEvidenceAttempt{
		Operation: "tools/call", Tool: tool,
		Request:      map[string]any{"tool": tool, "arguments": cloneJSONMap(arguments)},
		StartedAtUTC: started.Format(time.RFC3339Nano), FinishedAtUTC: finished.Format(time.RFC3339Nano),
	}
	if err != nil {
		attempt.TransportError = err.Error()
		return result, attempt, err
	}
	attempt.NormalizedResponse = RawMessageMap(result)
	if result.IsError {
		attempt.ProductError = toolErrorCode(result)
	}
	return result, attempt, nil
}

func persistMCPAttemptEvidence(resultsDir, relativePath, evidenceName, kind string, attempts []mcpEvidenceAttempt, fields map[string]any, redactor *Redactor) (EvidenceRecord, map[string]any) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEvidence")
	reference := filepath.ToSlash(relativePath)
	record := EvidenceRecord{Name: evidenceName, Required: true, Ref: reference}
	payload := map[string]any{
		"schema_version": 2,
		"kind":           kind,
		"attempts":       attempts,
	}
	for key, value := range fields {
		payload[key] = value
	}
	logFields := map[string]any{"kind": kind, "evidence_ref": reference, "attempt_count": len(attempts)}
	for _, key := range []string{"campaign_id", "lane", "step_id", "provider", "stage", "tool"} {
		if value, ok := fields[key]; ok {
			logFields[key] = value
		}
	}
	log.WithFields(logFields).Info("开始写入 Windows MCP 调用证据")
	redacted := redactor.Redact(payload)
	if redactor.containsKnownSecret(redacted) {
		record.WriteError = "redaction invariant failed before writing MCP call evidence"
		log.WithFields(logFields).Error("Windows MCP 调用证据脱敏检查失败")
		return record, nil
	}
	safePayload, _ := redacted.(map[string]any)
	if err := writeJSON(filepath.Join(resultsDir, filepath.FromSlash(relativePath)), redacted); err != nil {
		record.WriteError = err.Error()
		log.WithErr(err).WithFields(logFields).Error("Windows MCP 调用证据写入失败")
		return record, safePayload
	}
	record.Present = true
	log.WithFields(logFields).Info("Windows MCP 调用证据写入完成")
	return record, safePayload
}

func assertionAttempt(operation string, request map[string]any, response any, started, finished time.Time, err error) mcpEvidenceAttempt {
	attempt := mcpEvidenceAttempt{
		Operation: operation, Request: request, NormalizedResponse: response,
		StartedAtUTC: started.Format(time.RFC3339Nano), FinishedAtUTC: finished.Format(time.RFC3339Nano),
	}
	if err != nil {
		attempt.TransportError = err.Error()
	}
	return attempt
}

func (e *ScenarioExecutor) recordLocalPrerequisiteFailure(scope, stepID, operation string, started time.Time, cause error) StepExecution {
	finished := time.Now().UTC()
	attempt := assertionAttempt(operation, map[string]any{"scope": scope, "step_id": stepID}, map[string]any{"checked": true}, started, finished, nil)
	attempt.AssertionError = cause.Error()
	relative := filepath.ToSlash(filepath.Join("evidence", "prerequisites", scope+"-"+stepID+".json"))
	evidence, safePayload := persistMCPAttemptEvidence(e.resultsDir, relative, stepID, "superdev.windows-validation.prerequisite-evidence", []mcpEvidenceAttempt{attempt}, map[string]any{
		"campaign_id": e.campaignID, "lane": e.lane, "step_id": stepID, "stage": operation,
		"execution_facts": map[string]any{
			"attempted": true, "succeeded": false, "failure": cause.Error(),
			"started_at_utc": started.Format(time.RFC3339Nano), "finished_at_utc": finished.Format(time.RFC3339Nano),
		},
	}, e.redactor)
	result := attemptedResult(false, cause.Error(), started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), []EvidenceRecord{evidence})
	inline := map[string]any(nil)
	if !evidence.Present {
		inline = safePayload
	}
	logger.GetLogger().WithEntryName("WindowsValidationPrerequisite").WithErr(cause).WithFields(e.logFields(map[string]any{
		"scope": scope, "step": stepID, "operation": operation, "phase_status": result.PhaseStatus, "evidence_ref": relative,
	})).Error("Windows 本地前置检查失败")
	return StepExecution{StepID: stepID, Coverage: CoverageSupporting, Result: result, InlineEvidence: inline}
}

func supportingFailure(action, detail string) string {
	if detail == "" {
		return action
	}
	return fmt.Sprintf("%s: %s", action, detail)
}
