package store_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAppendAndFetch(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)

	entries := []model.LogEntry{
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "hello", Stream: "stdout"},
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: now.Add(time.Second), Level: "ERROR", Message: "boom", Stream: "stderr"},
	}
	require.NoError(t, s.AppendBatch(entries))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "hello", got[0].Message)
	assert.Equal(t, "boom", got[1].Message)
}

func TestFetchPagination(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	// 插入5条，id 递增（1..5），消息为 "msg-0".."msg-4"
	entries := make([]model.LogEntry, 5)
	for i := range entries {
		entries[i] = model.LogEntry{
			DeploymentID: "svc-1", RunID: "run-1",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Level:     "INFO", Message: fmt.Sprintf("msg-%d", i), Stream: "stdout",
		}
	}
	require.NoError(t, s.AppendBatch(entries))

	// 不带 Before：返回最新 3 条（id 3,4,5），结果按 ASC 排列
	first, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3})
	require.NoError(t, err)
	assert.Len(t, first, 3)
	assert.Equal(t, "msg-2", first[0].Message) // id=3
	assert.Equal(t, "msg-4", first[2].Message) // id=5

	// Before=first[0].ID（即 id=3）：返回 id<3 的最新 2 条（id 1,2）
	second, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3, Before: first[0].ID})
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, "msg-0", second[0].Message) // id=1
	assert.Equal(t, "msg-1", second[1].Message) // id=2

	// Before=second[0].ID（即 id=1）：没有更早的记录
	third, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3, Before: second[0].ID})
	require.NoError(t, err)
	assert.Len(t, third, 0)
}

func TestFetchByRunID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-1", RunID: "run-A", Timestamp: now, Level: "INFO", Message: "run A"},
		{DeploymentID: "svc-1", RunID: "run-B", Timestamp: now, Level: "INFO", Message: "run B"},
	}))

	got, err := s.Fetch(store.FetchParams{RunID: "run-A", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "run A", got[0].Message)
}

func TestDeleteOldEntries(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	recent := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: old, Level: "INFO", Message: "old"},
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: recent, Level: "INFO", Message: "new"},
	}))

	require.NoError(t, s.DeleteOlderThan(7))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "new", got[0].Message)
}

func TestSearchFindsKeywordAcrossServicesInTimeOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 api done", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(1 * time.Second), Level: "WARN", Message: "TRACE-8F21 worker retry", Stream: "stderr"},
		{DeploymentID: "svc-c", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "unrelated", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b", "svc-c"},
		Query:      "trace-8f21",
		Limit:      10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 2)
	assert.Equal(t, "svc-b", got.Entries[0].DeploymentID)
	assert.Equal(t, "svc-a", got.Entries[1].DeploymentID)
	assert.Equal(t, 2, got.Total)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, got.DeploymentCounts)
}

func TestSearchRestrictsToServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-b"},
		Query:      "trace-8f21",
		Limit:      10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 1)
	assert.Equal(t, "svc-b", got.Entries[0].DeploymentID)
	assert.Equal(t, map[string]int{"svc-b": 1}, got.DeploymentCounts)
}

func TestSearchPagesAfterCursorWithoutChangingCounts(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace page api 1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace page api 2", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "trace page worker 1", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(4 * time.Second), Level: "INFO", Message: "trace page worker 2", Stream: "stdout"},
	}))

	first, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b"},
		Query:      "trace page",
		Limit:      2,
	})
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	assert.True(t, first.HasMore)
	assert.Equal(t, 4, first.Total)
	assert.Equal(t, map[string]int{"svc-a": 2, "svc-b": 2}, first.DeploymentCounts)

	cursor := first.Entries[len(first.Entries)-1]
	second, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b"},
		Query:      "trace page",
		Limit:      2,
		CursorTime: &cursor.Timestamp,
		CursorID:   cursor.ID,
	})
	require.NoError(t, err)

	require.Len(t, second.Entries, 2)
	assert.False(t, second.HasMore)
	assert.Equal(t, []string{"trace page worker 1", "trace page worker 2"}, messagesOf(second.Entries))
	assert.Equal(t, 4, second.Total)
	assert.Equal(t, map[string]int{"svc-a": 2, "svc-b": 2}, second.DeploymentCounts)
}

func TestFetchContextReturnsProjectServicesAroundTargetTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "api before", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "worker before", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{DeploymentID: "svc-c", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "billing after", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Minute), Level: "INFO", Message: "outside window", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	targetID := search.Entries[0].ID

	got, err := s.FetchContext(store.ContextParams{
		TargetID:   targetID,
		DeploymentIDs: []string{"svc-a", "svc-b", "svc-c"},
		Before:     3 * time.Second,
		After:      3 * time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, targetID, got.TargetID)
	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"api before", "trace-8f21 target"}, messagesOf(got.ItemsByDeployment["svc-a"]))
	assert.Equal(t, []string{"worker before"}, messagesOf(got.ItemsByDeployment["svc-b"]))
	assert.Equal(t, []string{"billing after"}, messagesOf(got.ItemsByDeployment["svc-c"]))
}

func TestFetchContextRejectsTargetOutsideServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "target", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	_, err = s.FetchContext(store.ContextParams{
		TargetID:   search.Entries[0].ID,
		DeploymentIDs: []string{"svc-b"},
		Before:     time.Second,
		After:      time.Second,
	})
	require.ErrorIs(t, err, store.ErrLogEntryNotFound)
}

func TestFetchContextPagePagesBeforeAndAfter(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-3 * time.Second), Level: "INFO", Message: "a-3", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "a-2", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-1 * time.Second), Level: "INFO", Message: "a-1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "target", Stream: "stderr"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "a+1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "a+2", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "a+3", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "b-near", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	target := search.Entries[0]

	before, err := s.FetchContextPage(store.ContextPageParams{
		DeploymentID:  "svc-a",
		CursorTime: target.Timestamp,
		CursorID:   target.ID,
		Direction:  store.ContextPageBefore,
		Limit:      2,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a-2", "a-1"}, messagesOf(before.Entries))
	assert.True(t, before.HasMore)

	after, err := s.FetchContextPage(store.ContextPageParams{
		DeploymentID:  "svc-a",
		CursorTime: target.Timestamp,
		CursorID:   target.ID,
		Direction:  store.ContextPageAfter,
		Limit:      2,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a+1", "a+2"}, messagesOf(after.Entries))
	assert.True(t, after.HasMore)
}

func messagesOf(entries []model.LogEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Message
	}
	return out
}
