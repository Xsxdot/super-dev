// Package logbackend_test 验证 SQLiteBackend 实现。
package logbackend_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

type testStoreWriter struct {
	s *store.Store
}

func (w testStoreWriter) AppendBatch(_ context.Context, entries []model.LogEntry) error {
	return w.s.AppendBatch(entries)
}

func newTestSQLiteBackend(t *testing.T) (*logbackend.SQLiteBackend, *logbuf.Buffer) {
	t.Helper()
	s, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	buf := logbuf.New(testStoreWriter{s: s}, 100, "")
	t.Cleanup(buf.Close)
	return logbackend.NewSQLiteBackend(s, buf), buf
}

func TestSQLiteBackend_AppendBatchWritesEntries(t *testing.T) {
	b, _ := newTestSQLiteBackend(t)

	err := b.AppendBatch(context.Background(), []model.LogEntry{
		{DeploymentID: "svc-1", Message: "hello", Timestamp: time.Now()},
	})
	require.NoError(t, err)

	entries, _, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "hello", entries[0].Message)
}

func TestSQLiteBackend_QueryEmpty(t *testing.T) {
	b, _ := newTestSQLiteBackend(t)
	entries, next, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1"})
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.True(t, next.Time.IsZero())
}

func TestSQLiteBackend_QueryReturnsEntries(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: now, Message: "hello", Stream: "stdout"})
	buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: now.Add(time.Millisecond), Message: "world", Stream: "stdout"})
	buf.Append(model.LogEntry{DeploymentID: "svc-2", RunID: "r2", Timestamp: now, Message: "other", Stream: "stdout"})

	// flush 写入 SQLite（等待 buffer 刷盘）
	time.Sleep(200 * time.Millisecond)

	entries, _, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "hello", entries[0].Message)
	assert.Equal(t, "world", entries[1].Message)
}

func TestSQLiteBackend_QueryBeforeID(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	for i := 0; i < 5; i++ {
		buf.Append(model.LogEntry{
			DeploymentID: "svc-1",
			RunID:        "r1",
			Timestamp:    now.Add(time.Duration(i) * time.Millisecond),
			Level:        "INFO",
			Message:      fmt.Sprintf("msg-%d", i),
			Stream:       "stdout",
		})
	}
	time.Sleep(200 * time.Millisecond)

	first, _, err := b.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "svc-1", Limit: 3})
	require.NoError(t, err)
	require.Len(t, first, 3)

	second, _, err := b.Query(context.Background(), logbackend.QueryFilter{
		DeploymentID: "svc-1",
		Limit:        3,
		Before:       logbackend.Cursor{ID: strconv.FormatInt(first[0].ID, 10)},
	})
	require.NoError(t, err)
	require.Len(t, second, 2)
	assert.Equal(t, []string{"msg-0", "msg-1"}, []string{second[0].Message, second[1].Message})
}

func TestSQLiteBackend_SearchReturnsMatches(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	now := time.Now().Truncate(time.Millisecond)
	buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: now, Message: "error occurred", Stream: "stderr"})
	buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: now.Add(time.Millisecond), Message: "all good", Stream: "stdout"})

	time.Sleep(200 * time.Millisecond)

	entries, _, hasMore, err := b.Search(context.Background(), logbackend.SearchQuery{
		DeploymentIDs: []string{"svc-1"},
		Text:          "error",
		Limit:         10,
	})
	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, entries, 1)
	assert.Equal(t, "error occurred", entries[0].Message)
}

func TestSQLiteBackend_SubscribeReceivesLiveEntries(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	stream := b.Subscribe(context.Background(), logbackend.SubscribeOptions{DeploymentID: "svc-1"})
	defer stream.Cancel()

	entry := model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: time.Now(), Message: "live", Stream: "stdout"}
	buf.Append(entry)

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "live", got.Message)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for live entry")
	}
}

func TestSQLiteBackend_SubscribeReplayLast(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "old1", Timestamp: time.Now()})
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "old2", Timestamp: time.Now().Add(time.Millisecond)})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := b.Subscribe(ctx, logbackend.SubscribeOptions{DeploymentID: "svc-1", ReplayLast: 2})
	defer stream.Cancel()

	got := []string{}
	for i := 0; i < 2; i++ {
		select {
		case e := <-stream.Ch:
			got = append(got, e.Message)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for replay entry %d", i)
		}
	}
	assert.Equal(t, []string{"old1", "old2"}, got)
}

func TestSQLiteBackend_SubscribeFiltersOtherServices(t *testing.T) {
	b, buf := newTestSQLiteBackend(t)

	stream := b.Subscribe(context.Background(), logbackend.SubscribeOptions{DeploymentID: "svc-1"})
	defer stream.Cancel()

	// 写入 svc-2 的日志，svc-1 的订阅者不应收到
	buf.Append(model.LogEntry{DeploymentID: "svc-2", RunID: "r2", Timestamp: time.Now(), Message: "not mine", Stream: "stdout"})
	// 写入 svc-1 的日志，确认能收到
	buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "r1", Timestamp: time.Now(), Message: "mine", Stream: "stdout"})

	select {
	case got := <-stream.Ch:
		assert.Equal(t, "mine", got.Message)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for entry")
	}
}

func TestSQLiteBackend_CancelStopsStream(t *testing.T) {
	b, _ := newTestSQLiteBackend(t)
	stream := b.Subscribe(context.Background(), logbackend.SubscribeOptions{DeploymentID: "svc-1"})
	stream.Cancel()
	// channel 应被关闭
	select {
	case _, ok := <-stream.Ch:
		assert.False(t, ok, "channel should be closed after Cancel")
	case <-time.After(time.Second):
		t.Fatal("channel not closed after Cancel")
	}
}
