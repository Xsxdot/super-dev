// handler_browser_control_audit.go 为本机浏览器调试的页面控制动作落安全审计。
//
// 职责：
//   - 把会改变页面状态或读取高风险数据的控制动作记入 operation 审计链路
//   - 对 evaluate 只记录表达式 hash、长度和结果类型，绝不记录明文表达式或结果
//
// 边界：
//   - 不做审批门禁，审批只在 open session 阶段进行
//   - 不记录 cookie、token、password、localStorage、输入框明文或 evaluate 结果值
//   - 只读动作（snapshot/screenshot/console/network）不在此审计，避免高频噪音
package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"

	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/operation"
)

// auditBrowserControl 记录一次浏览器控制动作的事实事件。
//
// 参数：
//   - ctx: 请求上下文
//   - action: 控制动作名（click/type/navigate/evaluate 等）
//   - sessionID: 浏览器调试会话 ID
//   - deploymentID: 被调试的本机前端 deployment ID
//   - err: 控制动作执行结果，nil 表示成功
//   - data: 已脱敏的补充字段（如 selector、表达式 hash）；调用方负责保证不含敏感明文
//
// 注意：
//   - kind 固定为 OperationBrowserDebugControl，仅用于审计，不触发审批
//   - 成功与失败都会落审计，便于事后复盘 AI 在浏览器内的全部副作用动作
func (a *App) auditBrowserControl(ctx context.Context, action, sessionID, deploymentID string, err error, data map[string]any) {
	if a.operationAudit == nil {
		return
	}
	auditData := map[string]any{"session_id": sessionID}
	maps.Copy(auditData, data)
	auditAction := operation.AuditExecuted
	if err != nil {
		auditAction = operation.AuditFailed
		auditData["error_code"] = browserControlErrorCode(err)
	}
	a.appendOperationAudit(ctx, operation.AuditEvent{
		Kind:   operation.OperationBrowserDebugControl,
		Action: auditAction,
		Plan: operation.Plan{
			Kind:   operation.OperationBrowserDebugControl,
			Target: operation.Target{DeploymentID: deploymentID},
		},
		Summary: "browser_debug." + action,
		Data:    auditData,
	})
}

// browserControlErrorCode 提取控制错误的稳定错误码，未知错误归为 internal。
func browserControlErrorCode(err error) string {
	var controlErr browsercontrol.ControlError
	if errors.As(err, &controlErr) {
		return controlErr.Code
	}
	return "internal"
}

// hashExpression 返回 evaluate 表达式的 sha256 摘要，用于审计去标识化追踪。
//
// 注意：
//   - 永远不记录表达式明文，只保留 hash，使重复表达式可关联但不可还原
func hashExpression(expression string) string {
	sum := sha256.Sum256([]byte(expression))
	return hex.EncodeToString(sum[:])
}
