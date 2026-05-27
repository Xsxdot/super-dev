// Package logbackend_test 验证 SQLiteBackend 实现。
package logbackend_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/logbuf"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

func newTestSQLiteBackend(t *testing.T) (logbackend.LogBackend, *logbuf.Buffer) {
	t.Helper()
	s, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	buf := logbuf.New(s, 100, "")
	t.Cleanup(buf.Close)
	return logbackend.NewSQLiteBackend(s, buf), buf
}

func TestSQLiteBackend_QueryEmpty(t *testing.T) {
	b, _ := newTestSQLiteBackend(t)
	entries, next, err := b.Query(context.Background(), logbackend.QueryFilter{ServiceID: "svc-1"})
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.True(t, next.Time.IsZero())
}

func TestSQLiteBackend_QueryReturnsEntries(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: now, Message: "hello", Stream: "stdout"})
	buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: now.Add(time.Millisecond), Message: "world", Stream: "stdout"})
	buf.Append(model.LogEntry{ServiceID: "svc-2", RunID: "r2", Timestamp: now, Message: "other", Stream: "stdout"})

	// flush 写入 SQLite（等待 buffer 刷盘）
	time.Sleep(200 * time.Millisecond)

	entries, _, err := b.Query(context.Background(), logbackend.QueryFilter{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "hello", entries[0].Message)
	assert.Equal(t, "world", entries[1].Message)
}

func TestSQLiteBackend_SearchReturnsMatches(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: now, Message: "error occurred", Stream: "stderr"})
	buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: now.Add(time.Millisecond), Message: "all good", Stream: "stdout"})

	time.Sleep(200 * time.Millisecond)

	entries, _, hasMore, err := b.Search(context.Background(), logbackend.SearchQuery{
		ServiceIDs: []string{"svc-1"},
		Text:       "error",
		Limit:      10,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, entries, 1)
	assert.Equal(t, "error occurred", entries[0].Message)
}

func TestSQLiteBackend_SubscribeReceivesLiveEntries(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	stream := b.Subscribe(context.Background(), "svc-1")
	defer stream.Cancel()

	entry := model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: time.Now(), Message: "live", Stream: "stdout"}
	buf.Append(entry)

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "live", got.Message)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for live entry")
	}
}

func TestSQLiteBackend_SubscribeFiltersOtherServices(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	stream := b.Subscribe(context.Background(), "svc-1")
	defer stream.Cancel()

	// 写入 svc-2 的日志，svc-1 的订阅者不应收到
	buf.Append(model.LogEntry{ServiceID: "svc-2", RunID: "r2", Timestamp: time.Now(), Message: "not mine", Stream: "stdout"})
	// 写入 svc-1 的日志，确认能收到
	buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "r1", Timestamp: time.Now(), Message: "mine", Stream: "stdout"})

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "mine", got.Message)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for entry")
	}
}

func TestSQLiteBackend_CancelStopsStream(t *testing.T) {
	b, _ := newTestSQLiteBackend(t)
	stream := b.Subscribe(context.Background(), "svc-1")
	stream.Cancel()
	// channel 应被关闭
	select {
	case _, ok := <-stream.Ch:
		assert.False(t, ok, "channel should be closed after Cancel")
	case <-time.After(time.Second):
		t.Fatal("channel not closed after Cancel")
	}
}
