package logbuf_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/logbuf"
	"github.com/superdev/agent/model"
)

func TestBufferSubscribeReceivesEntries(t *testing.T) {
	buf := logbuf.New(nil, 8000)
	defer buf.Close()

	ch := buf.Subscribe("sub-1")
	defer buf.Unsubscribe("sub-1")

	entry := model.LogEntry{ServiceID: "svc-1", RunID: "run-1", Level: "INFO", Message: "hello", Stream: "stdout", Timestamp: time.Now()}
	buf.Append(entry)

	select {
	case got := <-ch:
		assert.Equal(t, "hello", got.Message)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for log entry")
	}
}

func TestBufferRecentReturnsLastN(t *testing.T) {
	buf := logbuf.New(nil, 5)
	defer buf.Close()

	for i := 0; i < 10; i++ {
		buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "run-1", Level: "INFO",
			Message: fmt.Sprintf("msg-%d", i), Stream: "stdout", Timestamp: time.Now()})
	}

	got := buf.Recent(3)
	require.Len(t, got, 3)
	assert.Equal(t, "msg-7", got[0].Message)
}

func TestBufferMaxSize(t *testing.T) {
	buf := logbuf.New(nil, 3)
	defer buf.Close()

	for i := 0; i < 5; i++ {
		buf.Append(model.LogEntry{ServiceID: "svc-1", RunID: "run-1", Level: "INFO",
			Message: fmt.Sprintf("msg-%d", i), Stream: "stdout", Timestamp: time.Now()})
	}

	got := buf.Recent(10)
	assert.Len(t, got, 3)
	assert.Equal(t, "msg-2", got[0].Message)
}
