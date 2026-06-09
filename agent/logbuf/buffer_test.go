package logbuf_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
)

type fakeLogWriter struct {
	entries []model.LogEntry
}

func (f *fakeLogWriter) AppendBatch(_ context.Context, entries []model.LogEntry) error {
	f.entries = append(f.entries, entries...)
	return nil
}

func TestBufferFlushUsesContextAwareLogWriter(t *testing.T) {
	writer := &fakeLogWriter{}
	buf := logbuf.New(writer, 10, "")

	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "persist me", Timestamp: time.Now()})
	buf.Close()

	require.Len(t, writer.entries, 1)
	assert.Equal(t, "persist me", writer.entries[0].Message)
}

func TestBufferSubscribeReceivesEntries(t *testing.T) {
	buf := logbuf.New(nil, 8000, "")
	defer buf.Close()

	ch := buf.Subscribe("sub-1")
	defer buf.Unsubscribe("sub-1")

	entry := model.LogEntry{DeploymentID: "svc-1", RunID: "run-1", Level: "INFO", Message: "hello", Stream: "stdout", Timestamp: time.Now()}
	buf.Append(entry)

	select {
	case got := <-ch:
		assert.Equal(t, "hello", got.Message)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for log entry")
	}
}

func TestBufferFoldEmitsIncrement(t *testing.T) {
	buf := logbuf.New(nil, 100, "node-1")
	defer buf.Close()
	buf.SetFoldWindow(5 * time.Second)

	ch := buf.Subscribe("s1")
	defer buf.Unsubscribe("s1")
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	buf.Append(model.LogEntry{DeploymentID: "A", Message: "boom count=1", Timestamp: t0, Stream: "stdout"})
	ev1 := <-ch
	require.NotEmpty(t, ev1.Message)
	require.NotEmpty(t, ev1.FoldKey)
	assert.Equal(t, 1, ev1.RepeatCount)
	foldKey := ev1.FoldKey

	buf.Append(model.LogEntry{DeploymentID: "A", Message: "boom count=2", Timestamp: t0.Add(time.Second), Stream: "stdout"})
	ev2 := <-ch
	assert.Empty(t, ev2.Message)
	assert.Equal(t, foldKey, ev2.FoldKey)
	assert.Equal(t, 2, ev2.RepeatCount)
}

func TestBufferFoldUpdatesRecentCount(t *testing.T) {
	buf := logbuf.New(nil, 100, "node-1")
	defer buf.Close()
	buf.SetFoldWindow(5 * time.Second)
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	buf.Append(model.LogEntry{DeploymentID: "A", Message: "boom count=1", Timestamp: t0, Stream: "stdout"})
	buf.Append(model.LogEntry{DeploymentID: "A", Message: "boom count=2", Timestamp: t0.Add(time.Second), Stream: "stdout"})

	recent := buf.Recent(10)
	require.Len(t, recent, 1)
	assert.Equal(t, 2, recent[0].RepeatCount)
	assert.NotEmpty(t, recent[0].FoldKey)
}

func TestBufferRecentReturnsLastN(t *testing.T) {
	buf := logbuf.New(nil, 5, "")
	defer buf.Close()

	for i := 0; i < 10; i++ {
		buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "run-1", Level: "INFO",
			Message: fmt.Sprintf("msg-%d", i), Stream: "stdout", Timestamp: time.Now()})
	}

	got := buf.Recent(3)
	require.Len(t, got, 3)
	assert.Equal(t, "msg-7", got[0].Message)
}

func TestBufferMaxSize(t *testing.T) {
	buf := logbuf.New(nil, 3, "")
	defer buf.Close()

	for i := 0; i < 5; i++ {
		buf.Append(model.LogEntry{DeploymentID: "svc-1", RunID: "run-1", Level: "INFO",
			Message: fmt.Sprintf("msg-%d", i), Stream: "stdout", Timestamp: time.Now()})
	}

	got := buf.Recent(10)
	assert.Len(t, got, 3)
	assert.Equal(t, "msg-2", got[0].Message)
}

func TestBuffer_AppendFillsSourceID(t *testing.T) {
	buf := logbuf.New(nil, 10, "superdev-ab12")
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "hello"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "superdev-ab12", recent[0].SourceID)
}

func TestBuffer_AppendPreservesExistingSourceID(t *testing.T) {
	// 如果 LogEntry 已有 SourceID（远端日志转发场景），不覆盖
	buf := logbuf.New(nil, 10, "superdev-ab12")
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "remote", SourceID: "superdev-ff00"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "superdev-ff00", recent[0].SourceID, "existing SourceID must not be overwritten")
}

func TestBuffer_EmptyNodeID_SourceIDLeftEmpty(t *testing.T) {
	buf := logbuf.New(nil, 10, "")
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "no node"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "", recent[0].SourceID)
}
