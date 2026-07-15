// cleanup_test.go 验证 campaign-owned 资源严格按 acquisition 逆序释放并保留 residual。
//
// 职责：锁定统一 cleanup stack、错误上下文和 marker 删除门槛。
// 边界：不删除 borrowed topology，不实现跨版本 journal replay。
package runtimevalidation

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCleanupStackReleasesInReverseOrder(t *testing.T) {
	t.Parallel()

	journal, err := OpenCleanupJournal(filepath.Join(t.TempDir(), "journal.jsonl"), "campaign-1", time.Now)
	require.NoError(t, err)
	defer journal.Close()
	stack := NewCleanupStack(journal)
	order := []string{}
	require.NoError(t, stack.Track(&fakeCleanupAction{kind: "service", id: "first", release: func() error { order = append(order, "first"); return nil }}))
	require.NoError(t, stack.Track(&fakeCleanupAction{kind: "session", id: "second", release: func() error { order = append(order, "second"); return nil }}))

	result := stack.Cleanup(context.Background())
	require.Equal(t, []string{"second", "first"}, order)
	require.True(t, result.JournalComplete)
	require.Empty(t, result.Residuals)
}

func TestCleanupStackKeepsFailedReleaseAsResidual(t *testing.T) {
	t.Parallel()

	journal, err := OpenCleanupJournal(filepath.Join(t.TempDir(), "journal.jsonl"), "campaign-1", time.Now)
	require.NoError(t, err)
	defer journal.Close()
	stack := NewCleanupStack(journal)
	require.NoError(t, stack.Track(&fakeCleanupAction{kind: "pipeline", id: "run-1", release: func() error { return errors.New("still running") }}))

	result := stack.Cleanup(context.Background())
	require.False(t, result.JournalComplete)
	require.Equal(t, []Residual{{Kind: "pipeline", ID: "run-1", Detail: "still running"}}, result.Residuals)
}

type fakeCleanupAction struct {
	kind    string
	id      string
	release func() error
}

func (a *fakeCleanupAction) Kind() string { return a.kind }
func (a *fakeCleanupAction) ID() string   { return a.id }
func (a *fakeCleanupAction) Release(context.Context) error {
	return a.release()
}
