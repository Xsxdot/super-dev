package store_test

import (
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
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "hello", Stream: "stdout"},
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: now.Add(time.Second), Level: "ERROR", Message: "boom", Stream: "stderr"},
	}
	require.NoError(t, s.AppendBatch(entries))

	got, err := s.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "hello", got[0].Message)
	assert.Equal(t, "boom", got[1].Message)
}

func TestFetchPagination(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	entries := make([]model.LogEntry, 5)
	for i := range entries {
		entries[i] = model.LogEntry{
			ServiceID: "svc-1", RunID: "run-1",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Level:     "INFO", Message: "msg", Stream: "stdout",
		}
	}
	require.NoError(t, s.AppendBatch(entries))

	first, err := s.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 3})
	require.NoError(t, err)
	assert.Len(t, first, 3)

	second, err := s.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 3, Before: first[0].ID})
	require.NoError(t, err)
	assert.Len(t, second, 0)
}

func TestFetchByRunID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-1", RunID: "run-A", Timestamp: now, Level: "INFO", Message: "run A"},
		{ServiceID: "svc-1", RunID: "run-B", Timestamp: now, Level: "INFO", Message: "run B"},
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
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: old, Level: "INFO", Message: "old"},
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: recent, Level: "INFO", Message: "new"},
	}))

	require.NoError(t, s.DeleteOlderThan(7))

	got, err := s.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "new", got[0].Message)
}

func TestSearchFindsKeywordAcrossServicesInTimeOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 api done", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(1 * time.Second), Level: "WARN", Message: "TRACE-8F21 worker retry", Stream: "stderr"},
		{ServiceID: "svc-c", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "unrelated", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		ServiceIDs: []string{"svc-a", "svc-b", "svc-c"},
		Query:      "trace-8f21",
		Limit:      10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 2)
	assert.Equal(t, "svc-b", got.Entries[0].ServiceID)
	assert.Equal(t, "svc-a", got.Entries[1].ServiceID)
	assert.Equal(t, 2, got.Total)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, got.ServiceCounts)
}

func TestSearchRestrictsToServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		ServiceIDs: []string{"svc-b"},
		Query:      "trace-8f21",
		Limit:      10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 1)
	assert.Equal(t, "svc-b", got.Entries[0].ServiceID)
	assert.Equal(t, map[string]int{"svc-b": 1}, got.ServiceCounts)
}

func TestFetchContextReturnsProjectServicesAroundTargetTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "api before", Stream: "stdout"},
		{ServiceID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "worker before", Stream: "stdout"},
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{ServiceID: "svc-c", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "billing after", Stream: "stdout"},
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Minute), Level: "INFO", Message: "outside window", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{ServiceIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	targetID := search.Entries[0].ID

	got, err := s.FetchContext(store.ContextParams{
		TargetID:   targetID,
		ServiceIDs: []string{"svc-a", "svc-b", "svc-c"},
		Before:     3 * time.Second,
		After:      3 * time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, targetID, got.TargetID)
	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"api before", "trace-8f21 target"}, messagesOf(got.ItemsByService["svc-a"]))
	assert.Equal(t, []string{"worker before"}, messagesOf(got.ItemsByService["svc-b"]))
	assert.Equal(t, []string{"billing after"}, messagesOf(got.ItemsByService["svc-c"]))
}

func TestFetchContextRejectsTargetOutsideServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "target", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{ServiceIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	_, err = s.FetchContext(store.ContextParams{
		TargetID:   search.Entries[0].ID,
		ServiceIDs: []string{"svc-b"},
		Before:     time.Second,
		After:      time.Second,
	})
	require.ErrorIs(t, err, store.ErrLogEntryNotFound)
}

func messagesOf(entries []model.LogEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Message
	}
	return out
}
