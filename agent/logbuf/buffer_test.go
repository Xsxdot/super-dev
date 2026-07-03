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

type writerFunc func(entries []model.LogEntry) error

func (f writerFunc) AppendBatch(_ context.Context, entries []model.LogEntry) error {
	return f(entries)
}

func TestBufferFlushUsesContextAwareLogWriter(t *testing.T) {
	writer := &fakeLogWriter{}
	buf := logbuf.New(writer, 10, "", nil)

	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "persist me", Timestamp: time.Now()})
	buf.Close()

	require.Len(t, writer.entries, 1)
	assert.Equal(t, "persist me", writer.entries[0].Message)
}

func TestBufferSubscribeReceivesEntries(t *testing.T) {
	buf := logbuf.New(nil, 8000, "", nil)
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
	buf := logbuf.New(nil, 100, "node-1", nil)
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
	buf := logbuf.New(nil, 100, "node-1", nil)
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
	buf := logbuf.New(nil, 5, "", nil)
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
	buf := logbuf.New(nil, 3, "", nil)
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
	buf := logbuf.New(nil, 10, "superdev-ab12", nil)
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "hello"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "superdev-ab12", recent[0].SourceID)
}

func TestBuffer_AppendPreservesExistingSourceID(t *testing.T) {
	// 如果 LogEntry 已有 SourceID（远端日志转发场景），不覆盖
	buf := logbuf.New(nil, 10, "superdev-ab12", nil)
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "remote", SourceID: "superdev-ff00"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "superdev-ff00", recent[0].SourceID, "existing SourceID must not be overwritten")
}

func TestBuffer_EmptyNodeID_SourceIDLeftEmpty(t *testing.T) {
	buf := logbuf.New(nil, 10, "", nil)
	buf.Append(model.LogEntry{DeploymentID: "svc-1", Message: "no node"})
	recent := buf.Recent(1)
	require.Len(t, recent, 1)
	assert.Equal(t, "", recent[0].SourceID)
}

// TestSeqAssignment 验证新段按 deployment 独立单调分配 seq，折叠命中不耗 seq。
func TestSeqAssignment(t *testing.T) {
	b := logbuf.New(nil, 100, "node-1", map[string]uint64{"dep-a": 10})
	defer b.Close()
	b.SetFoldWindow(5 * time.Second)
	now := time.Now()

	b.Append(model.LogEntry{DeploymentID: "dep-a", Timestamp: now, Message: "x1"})
	b.Append(model.LogEntry{DeploymentID: "dep-a", Timestamp: now.Add(time.Millisecond), Message: "x1"}) // 折叠命中
	b.Append(model.LogEntry{DeploymentID: "dep-a", Timestamp: now.Add(10 * time.Second), Message: "x2"}) // 超窗新段
	b.Append(model.LogEntry{DeploymentID: "dep-b", Timestamp: now, Message: "y1"})

	recent := b.Recent(10)
	var seqs []uint64
	for _, e := range recent {
		if e.DeploymentID == "dep-a" {
			seqs = append(seqs, e.Seq)
		}
	}
	assert.Equal(t, []uint64{11, 12}, seqs)
	for _, e := range recent {
		if e.DeploymentID == "dep-b" {
			assert.Equal(t, uint64(1), e.Seq, "无水位的 deployment 从 1 开始")
		}
	}
}

// TestSeqOnFoldedRep 验证折叠代表行（落库 rep）携带段首 seq。
func TestSeqOnFoldedRep(t *testing.T) {
	var flushed []model.LogEntry
	w := writerFunc(func(entries []model.LogEntry) error {
		flushed = append(flushed, entries...)
		return nil
	})
	b := logbuf.New(w, 100, "node-1", nil)
	now := time.Now()
	b.Append(model.LogEntry{DeploymentID: "dep-a", Timestamp: now, Message: "same"})
	b.Append(model.LogEntry{DeploymentID: "dep-a", Timestamp: now.Add(time.Millisecond), Message: "same"})
	b.Close() // Close 触发 flush + closeAll
	for _, e := range flushed {
		assert.Equal(t, uint64(1), e.Seq, "折叠代表行必须带段首 seq，否则 UPSERT 后 seq 丢失")
	}
}
