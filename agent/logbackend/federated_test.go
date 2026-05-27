// Package logbackend_test 验证 FederatedBackend。
package logbackend_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

// stubBackend 是测试用的最简 LogBackend 实现。
type stubBackend struct {
	mu      sync.Mutex
	entries []model.LogEntry
	subCh   chan model.LogEntry
}

func newStubBackend(entries []model.LogEntry) *stubBackend {
	return &stubBackend{entries: entries, subCh: make(chan model.LogEntry, 16)}
}

func (s *stubBackend) Query(_ context.Context, _ logbackend.QueryFilter) ([]model.LogEntry, logbackend.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.LogEntry, len(s.entries))
	copy(out, s.entries)
	return out, logbackend.Cursor{}, nil
}

func (s *stubBackend) Search(_ context.Context, q logbackend.SearchQuery) ([]model.LogEntry, logbackend.Cursor, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.LogEntry
	for _, e := range s.entries {
		if q.Text == "" || containsStr(e.Message, q.Text) {
			out = append(out, e)
		}
	}
	return out, logbackend.Cursor{}, false, nil
}

func (s *stubBackend) Subscribe(_ context.Context, _ string) logbackend.LogStream {
	ch := make(chan model.LogEntry, 16)
	cancel := func() { close(ch) }
	// 转发 subCh 到 ch
	go func() {
		for e := range s.subCh {
			ch <- e
		}
	}()
	return logbackend.LogStream{Ch: ch, Cancel: cancel}
}

func (s *stubBackend) push(e model.LogEntry) { s.subCh <- e }

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

func makeEntry(id int64, msg string, t time.Time) model.LogEntry {
	return model.LogEntry{ID: id, ServiceID: "svc-1", Timestamp: t, Message: msg, Stream: "stdout"}
}

func TestFederatedBackend_QueryMergesAndSorts(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	b1 := newStubBackend([]model.LogEntry{
		makeEntry(1, "a", now),
		makeEntry(3, "c", now.Add(2*time.Millisecond)),
	})
	b2 := newStubBackend([]model.LogEntry{
		makeEntry(2, "b", now.Add(time.Millisecond)),
		makeEntry(4, "d", now.Add(3*time.Millisecond)),
	})
	fed := logbackend.NewFederatedBackend([]logbackend.LogBackend{b1, b2})

	entries, _, err := fed.Query(context.Background(), logbackend.QueryFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 4)
	assert.Equal(t, "a", entries[0].Message)
	assert.Equal(t, "b", entries[1].Message)
	assert.Equal(t, "c", entries[2].Message)
	assert.Equal(t, "d", entries[3].Message)
}

func TestFederatedBackend_QueryRespectsLimit(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	b1 := newStubBackend([]model.LogEntry{
		makeEntry(1, "a", now),
		makeEntry(2, "b", now.Add(time.Millisecond)),
	})
	b2 := newStubBackend([]model.LogEntry{
		makeEntry(3, "c", now.Add(2*time.Millisecond)),
	})
	fed := logbackend.NewFederatedBackend([]logbackend.LogBackend{b1, b2})

	entries, _, err := fed.Query(context.Background(), logbackend.QueryFilter{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "a", entries[0].Message)
	assert.Equal(t, "b", entries[1].Message)
}

func TestFederatedBackend_SearchMergesResults(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	b1 := newStubBackend([]model.LogEntry{makeEntry(1, "error in svc", now)})
	b2 := newStubBackend([]model.LogEntry{makeEntry(2, "another error", now.Add(time.Millisecond))})
	fed := logbackend.NewFederatedBackend([]logbackend.LogBackend{b1, b2})

	entries, _, _, err := fed.Search(context.Background(), logbackend.SearchQuery{Text: "error", Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "error in svc", entries[0].Message)
	assert.Equal(t, "another error", entries[1].Message)
}

func TestFederatedBackend_SubscribeFanIn(t *testing.T) {
	b1 := newStubBackend(nil)
	b2 := newStubBackend(nil)
	fed := logbackend.NewFederatedBackend([]logbackend.LogBackend{b1, b2})

	stream := fed.Subscribe(context.Background(), "svc-1")
	defer stream.Cancel()

	now := time.Now()
	b1.push(makeEntry(1, "from-b1", now))
	b2.push(makeEntry(2, "from-b2", now.Add(time.Millisecond)))

	received := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case e := <-stream.Ch:
			received[e.Message] = true
		case <-time.After(time.Second):
			t.Fatalf("timeout, only got %d entries", i)
		}
	}
	assert.True(t, received["from-b1"])
	assert.True(t, received["from-b2"])
}

func TestFederatedBackend_SingleChild(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	b1 := newStubBackend([]model.LogEntry{makeEntry(1, "only", now)})
	fed := logbackend.NewFederatedBackend([]logbackend.LogBackend{b1})

	entries, _, err := fed.Query(context.Background(), logbackend.QueryFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "only", entries[0].Message)
}
