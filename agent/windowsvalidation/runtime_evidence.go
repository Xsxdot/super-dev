// runtime_evidence.go 记录 packaged MCP 进程生命周期的原始执行事实。
//
// 职责：
//   - 显式关闭当前 campaign 独占的 packaged MCP
//   - 保存 stop 的开始/结束时间、错误与 required evidence
//
// 边界：
//   - packaged MCP start/stop 不是 installer install/uninstall，不能补造 Installer Lifecycle
//   - 不启动或卸载桌面产品，也不推断人工 UAC 动作
package windowsvalidation

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/xsxdot/gokit/logger"
)

func recordPackagedMCPStop(client *MCPProcess, resultsDir string, redactor *Redactor, campaignID, lane string) StepExecution {
	log := logger.GetLogger().WithEntryName("WindowsValidationRuntime")
	started := time.Now().UTC()
	baseFields := map[string]any{"campaign_id": campaignID, "lane": lane, "operation": "packaged_mcp_stop"}
	log.WithFields(baseFields).Info("开始关闭 packaged MCP")
	closeErr := client.Close()
	finished := time.Now().UTC()
	relative := "mcp-stop.json"
	failure := ""
	if closeErr != nil {
		failure = closeErr.Error()
	}
	facts := ExecutionFacts{
		Attempted: true, Succeeded: closeErr == nil, Failure: failure,
		StartedAtUTC: started.Format(time.RFC3339Nano), FinishedAtUTC: finished.Format(time.RFC3339Nano),
	}
	record := map[string]any{
		"schema_version": 2, "kind": "superdev.windows-validation.mcp-stop",
		"campaign_id": campaignID, "lane": lane, "execution_facts": facts,
	}
	if closeErr != nil {
		record["error"] = failure
	}
	evidence := EvidenceRecord{Name: "packaged_mcp_stop", Required: true, Ref: relative}
	safeRecord := redactor.Redact(record)
	inline := map[string]any(nil)
	var writeErr error
	if redactor.containsKnownSecret(safeRecord) {
		writeErr = fmt.Errorf("redaction invariant failed before writing packaged MCP stop evidence")
	} else {
		writeErr = writeJSON(filepath.Join(resultsDir, relative), safeRecord)
	}
	if writeErr != nil {
		evidence.WriteError = writeErr.Error()
		if !redactor.containsKnownSecret(safeRecord) {
			inline = RawMessageMap(safeRecord)
		}
		log.WithErr(writeErr).WithFields(baseFields).Error("packaged MCP stop 证据写入失败")
	} else {
		evidence.Present = true
	}
	result := deriveKnown(ResultInput{Facts: facts, Evidence: []EvidenceRecord{evidence}})
	fields := map[string]any{
		"campaign_id": campaignID, "lane": lane, "operation": "packaged_mcp_stop",
		"phase_status": result.PhaseStatus, "evidence_ref": relative,
	}
	if result.PhaseStatus == PhaseStatusPass {
		log.WithFields(fields).Info("packaged MCP 已关闭")
	} else {
		log.WithFields(fields).WithField("failure", resultReason(result)).Error("packaged MCP 关闭失败")
	}
	return StepExecution{StepID: "packaged-mcp-stop", Tool: "process_close", Coverage: CoverageSupporting, Result: result, InlineEvidence: inline}
}
