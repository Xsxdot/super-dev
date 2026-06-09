// 本文件提供 MCP 写操作的“调用即阻塞等待审批”通用流程。
//
// 职责：
//   - 首次直接执行写操作；遇 approval_required 时在等待窗口内轮询审批
//   - 批准后自动带 token 重试，使审批对调用方（AI）单轮无感
//   - 超时/驳回/过期返回带原因的 AgentError
//
// 边界：
//   - 不决定是否需要审批（由 agent 端 plan/policy 决定）
//   - 不直接读写审批文件，全部经 agent HTTP API
package mcp

import (
	"context"
	"strings"
	"time"
)

// callWithApproval 执行写操作并在需要时阻塞等待审批后自动重试。
//
// 参数：
//   - wait: 最长等待时长（<=0 表示不等待，遇审批直接返回原错误）
//   - do: 执行实际写操作，入参为 approvalToken（首次为空）
//
// 返回：
//   - nil 表示已执行成功（可能经历过一次审批等待）
//   - 超时/驳回/过期/其他错误返回对应 error
func (s *Server) callWithApproval(ctx context.Context, wait time.Duration, do func(ctx context.Context, approvalToken string) error) error {
	err := do(ctx, "")
	if err == nil {
		return nil
	}
	agentErr, ok := approvalRequiredAgentError(err)
	if !ok {
		return err
	}
	if wait <= 0 {
		return err
	}
	token, werr := s.waitForApprovalToken(ctx, agentErr.Approval.ID, wait)
	if werr != nil {
		return werr
	}
	if strings.TrimSpace(token) == "" {
		return err // 等待超时，返回原始 approval_required 让 AI 得知未执行
	}
	return do(ctx, token)
}
