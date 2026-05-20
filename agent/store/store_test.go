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
