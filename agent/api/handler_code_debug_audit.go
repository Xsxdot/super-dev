// handler_code_debug_audit.go 为本机代码调试 evaluate 动作落安全审计。
//
// 职责：
//   - 对直接和复合工具触发的 evaluate 记录表达式级审计
//   - 只记录表达式 hash、长度、来源和结果类型，不记录表达式明文或结果值
//
// 边界：
//   - 不执行审批门禁
//   - 不保存变量值、token、password、cookie 或 evaluate 结果明文
package api

import (
	"context"
	"errors"
	"maps"

	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/operation"
)

func (a *App) auditCodeDebugEvaluate(ctx context.Context, sessionID, deploymentID string, err error, data map[string]any) {
	if a.operationAudit == nil {
		return
	}
	auditData := map[string]any{"session_id": sessionID}
	maps.Copy(auditData, data)
	action := operation.AuditExecuted
	if err != nil {
		action = operation.AuditFailed
		auditData["error_code"] = codeDebugErrorCode(err)
	}
	a.appendOperationAudit(ctx, operation.AuditEvent{
		Kind:   operation.OperationCodeDebugEvaluate,
		Action: action,
		Plan: operation.Plan{
			Kind:   operation.OperationCodeDebugEvaluate,
			Target: operation.Target{DeploymentID: deploymentID, DebugSessionID: sessionID},
		},
		Summary: "code_debug.evaluate",
		Data:    auditData,
	})
}

func codeDebugErrorCode(err error) string {
	switch {
	case errors.Is(err, codedebug.ErrEvaluateDenied):
		return "debug_evaluate_denied"
	case errors.Is(err, codedebug.ErrSessionNotFound):
		return "debug_session_not_found"
	case errors.Is(err, codedebug.ErrSessionClosed):
		return "debug_session_closed"
	default:
		return "dap_request_failed"
	}
}
