// mutation_caller_test.go 验证 MCP mutation 只在 owning roots 清理后才进入 released。
//
// 职责：锁定 intent/acquired 顺序、committed 回调时机和失败时的 fail-closed residual。
// 边界：使用内存 fake ToolCaller，不访问 Agent、MCP 或真实文件系统资源。
package runtimevalidation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMutationJournalToolCallerReleasesOnlyAfterOwningRoots(t *testing.T) {
	journalPath := filepath.Join(t.TempDir(), "journal.jsonl")
	journal, err := OpenCleanupJournal(journalPath, "campaign-1", time.Now)
	require.NoError(t, err)
	stack := NewCleanupStack(journal)
	committed := false
	caller, err := NewMutationJournalToolCaller(mutationCallerDelegate{}, stack, func(tool string, _ map[string]any, _ ToolCallResult) {
		committed = tool == "start_service"
	})
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "start_service", map[string]any{"approval_token": "must-not-enter-journal"})
	require.NoError(t, err)
	require.True(t, committed)
	require.False(t, journal.Snapshot().Complete)

	stack.SetTerminalFacts(true, true, true, false)
	result := stack.Cleanup(context.Background())
	require.True(t, result.JournalComplete)
	require.Empty(t, result.Residuals)
	require.NoError(t, journal.Close())
	raw, err := os.ReadFile(journalPath)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(raw), "must-not-enter-journal"))
}

func TestMutationJournalToolCallerLeavesFailedCallIntentUnreleased(t *testing.T) {
	journal, err := OpenCleanupJournal(filepath.Join(t.TempDir(), "journal.jsonl"), "campaign-1", time.Now)
	require.NoError(t, err)
	stack := NewCleanupStack(journal)
	caller, err := NewMutationJournalToolCaller(mutationCallerDelegate{isError: true}, stack, nil)
	require.NoError(t, err)

	_, err = caller.CallTool(context.Background(), "start_service", nil)
	require.NoError(t, err)
	stack.SetTerminalFacts(true, true, true, false)
	result := stack.Cleanup(context.Background())
	require.False(t, result.JournalComplete)
	require.NotEmpty(t, journal.Snapshot().Unreleased)
	require.NoError(t, journal.Close())
}

type mutationCallerDelegate struct {
	isError bool
}

func (d mutationCallerDelegate) CallTool(context.Context, string, map[string]any) (ToolCallResult, error) {
	return ToolCallResult{IsError: d.isError, StructuredContent: map[string]any{"ok": !d.isError}}, nil
}
