// Package logbackend_test 验证 LogBackend 接口契约。
package logbackend_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

// TestQueryFilterZeroValue 确认 QueryFilter 零值不引发 panic。
func TestQueryFilterZeroValue(t *testing.T) {
	f := logbackend.QueryFilter{}
	assert.Equal(t, "", f.ServiceID)
	assert.Equal(t, 0, f.Limit)
	assert.True(t, f.Before.IsZero())
}

// TestSearchQueryZeroValue 确认 SearchQuery 零值不引发 panic。
func TestSearchQueryZeroValue(t *testing.T) {
	q := logbackend.SearchQuery{}
	assert.Equal(t, "", q.Text)
	assert.Nil(t, q.ServiceIDs)
}

// TestCursorZeroValue 确认 Cursor 零值表示"无游标"。
func TestCursorZeroValue(t *testing.T) {
	c := logbackend.Cursor{}
	assert.True(t, c.Time.IsZero())
	assert.Equal(t, int64(0), c.ID)
}

// TestLogStreamSendAndReceive 确认 LogStream channel 可正常发送/接收。
func TestLogStreamSendAndReceive(t *testing.T) {
	ch := make(chan model.LogEntry, 1)
	stream := logbackend.LogStream{Ch: ch, Cancel: func() {}}
	entry := model.LogEntry{ID: 1, Message: "hello", Timestamp: time.Now()}
	ch <- entry
	got := <-stream.Ch
	assert.Equal(t, entry.ID, got.ID)
}

// TestBackendIsInterface 确认 LogBackend 是接口（通过 nil 赋值验证）。
func TestBackendIsInterface(t *testing.T) {
	var _ logbackend.LogBackend = (logbackend.LogBackend)(nil)
	// 如果 LogBackend 不是接口，编译时直接报错
	_ = context.Background()
}
