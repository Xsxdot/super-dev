// mutation_caller.go 为真实 MCP 写调用提供逐次 cleanup journal 边界。
//
// 职责：
//   - 在 delegate 调用前 fsync intent，并为调用生成 campaign 内唯一身份
//   - 在真实应用成功后立即报告 committed，再 fsync acquired
//   - 把 released 延迟到 CleanupStack 已确认全部 owning roots 清理之后
//
// 边界：
//   - 不记录 arguments、approval token、credential 或原始响应
//   - 不决定业务断言，也不把 policy denial 或应用错误记为 acquired
package runtimevalidation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// MutationJournalToolCaller 包装一个真实 ToolCaller，为每次 mutation 写三阶段 journal。
type MutationJournalToolCaller struct {
	delegate    ToolCaller
	cleanup     *CleanupStack
	onCommitted func(tool string, arguments map[string]any, response ToolCallResult)
	mu          sync.Mutex
	sequence    int64
}

// NewMutationJournalToolCaller 创建 mutation journal 包装器。
//
// 参数：
//   - delegate: 已包含 exact-match 自动审批 actor 的真实 MCP caller
//   - cleanup: 当前 campaign 的统一 cleanup stack/journal
//   - onCommitted: 可选的外部副作用即时回调，先于 acquired fsync 执行
//
// 返回：输入完整时返回可用 caller，否则返回配置错误。
func NewMutationJournalToolCaller(delegate ToolCaller, cleanup *CleanupStack, onCommitted func(string, map[string]any, ToolCallResult)) (*MutationJournalToolCaller, error) {
	if delegate == nil || cleanup == nil || cleanup.journal == nil {
		return nil, fmt.Errorf("mutation journal delegate and cleanup stack are required")
	}
	return &MutationJournalToolCaller{delegate: delegate, cleanup: cleanup, onCommitted: onCommitted}, nil
}

// CallTool 对写工具执行 intent→真实调用→acquired；只读工具原样透传。
func (c *MutationJournalToolCaller) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	if !mutationTool(name) {
		return c.delegate.CallTool(ctx, name, arguments)
	}
	id := c.nextID(name)
	if err := c.cleanup.IntentMutation(id, map[string]any{"tool": name, "owner": "campaign-owning-roots"}); err != nil {
		return ToolCallResult{}, err
	}
	result, err := c.delegate.CallTool(ctx, name, arguments)
	committed := mutationWasApplied(err)
	if err != nil && !committed {
		return result, err
	}
	ok := applicationOK(result)
	if result.IsError || (ok != nil && !*ok) {
		return result, err
	}
	// callback 必须在 acquired fsync 前发生；即使磁盘随后失败，远程 pipeline 等副作用也不能从 cleanup guard 消失。
	if c.onCommitted != nil {
		c.onCommitted(name, cloneMap(arguments), result)
	}
	if acquireErr := c.cleanup.AcquireMutation(id); acquireErr != nil {
		return result, errors.Join(err, acquireErr)
	}
	return result, err
}

type mutationAppliedError struct {
	cause error
}

func (e *mutationAppliedError) Error() string { return e.cause.Error() }
func (e *mutationAppliedError) Unwrap() error { return e.cause }

func markMutationApplied(err error) error {
	if err == nil {
		return nil
	}
	return &mutationAppliedError{cause: err}
}

func mutationWasApplied(err error) bool {
	var applied *mutationAppliedError
	return errors.As(err, &applied)
}

func (c *MutationJournalToolCaller) nextID(tool string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sequence++
	return fmt.Sprintf("%06d-%s", c.sequence, strings.ReplaceAll(tool, "/", "_"))
}
