// Package debugsession 验证本机诊断会话持久化。
//
// 职责：
//   - 验证会话创建、事件追加、关闭和重载
//   - 验证损坏文件和超大事件的边界行为
//
// 边界：
//   - 不启动 HTTP agent
//   - 不访问 MCP 工具层
package debugsession

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStoreCreateAppendCloseAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug-sessions.json")
	store := NewFileStore(path)
	ctx := context.Background()

	session, event, err := store.Create(ctx, CreateRequest{
		ProjectID:   "p1",
		ProjectName: "demo",
		Title:       "API startup failure",
		Question:    "Why does api-dev fail?",
	})

	require.NoError(t, err)
	require.NotEmpty(t, session.ID)
	require.NotEmpty(t, event.ID)
	assert.Equal(t, StatusOpen, session.Status)
	assert.Equal(t, EventStatusChange, event.Type)

	note, err := store.AppendEvent(ctx, session.ID, AppendEventRequest{
		Type:    EventObservation,
		Actor:   ActorAssistant,
		Summary: "api-dev emitted retry exhausted",
		Data: map[string]any{
			"evidence_ids": []any{float64(42)},
		},
	})

	require.NoError(t, err)
	assert.NotEmpty(t, note.ID)

	closed, closeEvent, err := store.Close(ctx, session.ID, "collected enough evidence")
	require.NoError(t, err)
	require.NotNil(t, closed.ClosedAt)
	assert.Equal(t, StatusClosed, closed.Status)
	assert.Equal(t, EventStatusChange, closeEvent.Type)

	reloaded := NewFileStore(path)
	got, events, err := reloaded.Get(ctx, session.ID, 10)
	require.NoError(t, err)
	assert.Equal(t, StatusClosed, got.Status)
	assert.Len(t, events, 3)
}

func TestFileStoreRejectsNormalEventAfterClose(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "debug-sessions.json"))
	ctx := context.Background()
	session, _, err := store.Create(ctx, CreateRequest{
		ProjectID:   "p1",
		ProjectName: "demo",
		Title:       "closed session",
		Question:    "closed session question",
	})
	require.NoError(t, err)
	_, _, err = store.Close(ctx, session.ID, "close it")
	require.NoError(t, err)

	_, err = store.AppendEvent(ctx, session.ID, AppendEventRequest{
		Type:    EventNote,
		Actor:   ActorAssistant,
		Summary: "late note",
	})

	require.ErrorIs(t, err, ErrSessionClosed)
}

func TestFileStoreCorruptedFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug-sessions.json")
	require.NoError(t, os.WriteFile(path, []byte("{broken-json"), 0o600))

	store := NewFileStore(path)
	_, err := store.List(context.Background(), ListFilter{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "load debug sessions")
}

func TestFileStoreRejectsOversizedEventData(t *testing.T) {
	store := NewFileStore(filepath.Join(t.TempDir(), "debug-sessions.json"))
	ctx := context.Background()
	session, _, err := store.Create(ctx, CreateRequest{
		ProjectID:   "p1",
		ProjectName: "demo",
		Title:       "oversized",
		Question:    "oversized event",
	})
	require.NoError(t, err)

	_, err = store.AppendEvent(ctx, session.ID, AppendEventRequest{
		Type:    EventObservation,
		Actor:   ActorAssistant,
		Summary: "too large",
		Data:    map[string]any{"logs": strings.Repeat("x", maxEventDataBytes+1)},
	})

	require.ErrorIs(t, err, ErrEventTooLarge)
}
