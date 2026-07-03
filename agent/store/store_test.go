package store_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
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
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "hello", Stream: "stdout"},
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: now.Add(time.Second), Level: "ERROR", Message: "boom", Stream: "stderr"},
	}
	require.NoError(t, s.AppendBatch(entries))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, "hello", got[0].Message)
	assert.Equal(t, "boom", got[1].Message)
}

func TestAppendBatchUpsertsByFoldKey(t *testing.T) {
	s := newTestStore(t)
	ts := time.Now().UTC()

	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "r", Timestamp: ts, Level: "INFO", Message: "boom", Stream: "stdout", FoldKey: "k1", RepeatCount: 1},
	}))
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "r", Timestamp: ts.Add(time.Second), Level: "INFO", Message: "boom", Stream: "stdout", FoldKey: "k1", RepeatCount: 5},
	}))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "A"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 5, got[0].RepeatCount)
	assert.Equal(t, "k1", got[0].FoldKey)
	assert.Equal(t, ts, got[0].Timestamp)
	require.NotNil(t, got[0].LastSeenAt)
	assert.Equal(t, ts.Add(time.Second), *got[0].LastSeenAt)
}

// TestAppendBatchDoesNotFoldAcrossRuns 回归用例：同 deployment、相同 fold_key，
// 但 run_id 不同（如 agent 重启后的新会话）时不得折叠，必须各自落新行。
//
// 防回归对象：历史 bug——fold_key 单列唯一 + 进程内自增计数器重启归零，
// 导致重启后新日志撞历史 fold_key 被 UPDATE 进旧行，实时日志卡死。
func TestAppendBatchDoesNotFoldAcrossRuns(t *testing.T) {
	s := newTestStore(t)
	ts := time.Now().UTC()

	// run 1 与 run 2 复用同一 fold_key（模拟计数器重启归零后重发）。
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "run-1", Timestamp: ts, Level: "INFO", Message: "old-content", Stream: "stdout", FoldKey: "f1", RepeatCount: 1},
	}))
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "run-2", Timestamp: ts.Add(time.Second), Level: "INFO", Message: "new-content", Stream: "stdout", FoldKey: "f1", RepeatCount: 1},
	}))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "A"})
	require.NoError(t, err)
	// 两次属于不同 run，复合唯一键 (run_id, fold_key) 不冲突，必须是两行。
	require.Len(t, got, 2)
	// Fetch 按 id ASC 返回；run-2 的新内容应作为独立新行存在，未被折叠进 run-1 旧行。
	assert.Equal(t, "run-2", got[1].RunID)
	assert.Equal(t, "new-content", got[1].Message)
}

// TestNewMigratesLegacySingleColumnFoldIndex 回归用例：旧生产库带单列唯一索引
// idx_fold_key 且已有历史日志时，store.New 必须把唯一约束迁移为 (run_id, fold_key) 复合，
// 清空与新维度不兼容的历史脏数据，并保证后续跨 run 同 fold_key 不再被误折叠。
func TestNewMigratesLegacySingleColumnFoldIndex(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/logs.db"

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE log_entries (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			deployment_id TEXT     NOT NULL,
			run_id        TEXT     NOT NULL,
			timestamp     DATETIME NOT NULL,
			level         TEXT     NOT NULL,
			message       TEXT     NOT NULL,
			stream        TEXT     NOT NULL,
			repeat_count  INTEGER  NOT NULL DEFAULT 1,
			fold_key      TEXT     NOT NULL DEFAULT ''
		);
		CREATE UNIQUE INDEX idx_fold_key ON log_entries(fold_key) WHERE fold_key != '';
		INSERT INTO log_entries (deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key)
			VALUES ('A', 'old-run', '2026-06-15 00:00:00 +0000 UTC', 'INFO', 'stale', 'stdout', 1, 'f1');
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s, err := store.New(path)
	require.NoError(t, err)
	defer s.Close()

	// 迁移完成后的可观测行为：历史脏数据被清空 + 旧单列唯一约束失效，
	// 跨 run 复用同 fold_key 不再被折叠，各自落新行（索引名是实现细节，只断言行为）。
	ts := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "run-1", Timestamp: ts, Level: "INFO", Message: "c1", Stream: "stdout", FoldKey: "f1", RepeatCount: 1},
	}))
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "run-2", Timestamp: ts.Add(time.Second), Level: "INFO", Message: "c2", Stream: "stdout", FoldKey: "f1", RepeatCount: 1},
	}))
	got, err := s.Fetch(store.FetchParams{DeploymentID: "A"})
	require.NoError(t, err)
	// 只有迁移后写入的 run-1/run-2 两行；历史 old-run 的 stale 行已被清空。
	require.Len(t, got, 2)
	for _, e := range got {
		assert.NotEqual(t, "old-run", e.RunID, "legacy dirty data should be wiped on migration")
	}
}

func TestNewMigratesExistingLogEntriesBeforeCreatingFoldIndex(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/logs.db"

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`
		CREATE TABLE log_entries (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			deployment_id TEXT     NOT NULL,
			run_id        TEXT     NOT NULL,
			timestamp     DATETIME NOT NULL,
			level         TEXT     NOT NULL,
			message       TEXT     NOT NULL,
			stream        TEXT     NOT NULL
		);
	`)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s, err := store.New(path)
	require.NoError(t, err)
	defer s.Close()

	ts := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "A", RunID: "r", Timestamp: ts, Level: "INFO", Message: "boom", Stream: "stdout", FoldKey: "legacy-k", RepeatCount: 2},
		{DeploymentID: "A", RunID: "r", Timestamp: ts.Add(time.Second), Level: "INFO", Message: "boom", Stream: "stdout", FoldKey: "legacy-k", RepeatCount: 3},
	}))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "A"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, 3, got[0].RepeatCount)
	assert.Equal(t, "legacy-k", got[0].FoldKey)
}

// TestSeqMigrationBackfill 验证旧库升级后按 (deployment_id, id 序) 回填 seq。
func TestSeqMigrationBackfill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.db")

	db, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE log_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		deployment_id TEXT NOT NULL, run_id TEXT NOT NULL,
		timestamp DATETIME NOT NULL, level TEXT NOT NULL,
		message TEXT NOT NULL, stream TEXT NOT NULL,
		repeat_count INTEGER NOT NULL DEFAULT 1, fold_key TEXT NOT NULL DEFAULT '');
		CREATE UNIQUE INDEX idx_run_fold_key ON log_entries(run_id, fold_key) WHERE fold_key != ''`)
	require.NoError(t, err)

	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		_, err = db.Exec(`INSERT INTO log_entries (deployment_id, run_id, timestamp, level, message, stream, fold_key)
			VALUES ('dep-a','', ?, 'INFO', ?, 'stdout', ?)`, now, fmt.Sprintf("a%d", i), fmt.Sprintf("fa%d", i))
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO log_entries (deployment_id, run_id, timestamp, level, message, stream, fold_key)
		VALUES ('dep-b','', ?, 'INFO', 'b0', 'stdout', 'fb0')`, now)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s, err := store.New(path)
	require.NoError(t, err)
	defer s.Close()

	wm, err := s.SeqWatermarks()
	require.NoError(t, err)
	assert.Equal(t, uint64(3), wm["dep-a"], "dep-a 三条按 id 序回填 1..3")
	assert.Equal(t, uint64(1), wm["dep-b"])
}

// TestAppendBatchSeqAndLastSeen 验证插入写 seq/last_seen_at，折叠 UPSERT 不回写 timestamp。
func TestAppendBatchSeqAndLastSeen(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	head := model.LogEntry{DeploymentID: "dep-1", RunID: "", Timestamp: t0,
		Level: "INFO", Message: "boom", Stream: "stdout", FoldKey: "fk1", RepeatCount: 1, Seq: 7}
	require.NoError(t, s.AppendBatch([]model.LogEntry{head}))

	// 同 (run_id, fold_key) 的折叠代表行：计数与 last_seen_at 更新，timestamp 与 seq 不动。
	rep := head
	rep.Timestamp = t0.Add(3 * time.Second)
	rep.RepeatCount = 5
	require.NoError(t, s.AppendBatch([]model.LogEntry{rep}))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "dep-1"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint64(7), got[0].Seq)
	assert.Equal(t, 5, got[0].RepeatCount)
	assert.True(t, got[0].Timestamp.Equal(t0), "折叠 UPSERT 不得回写 timestamp")
	require.NotNil(t, got[0].LastSeenAt)
	assert.True(t, got[0].LastSeenAt.Equal(t0.Add(3*time.Second)))
}

func TestFetchPagination(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	// 插入5条，id 递增（1..5），消息为 "msg-0".."msg-4"
	entries := make([]model.LogEntry, 5)
	for i := range entries {
		entries[i] = model.LogEntry{
			DeploymentID: "svc-1", RunID: "run-1",
			Timestamp: now.Add(time.Duration(i) * time.Second),
			Level:     "INFO", Message: fmt.Sprintf("msg-%d", i), Stream: "stdout",
		}
	}
	require.NoError(t, s.AppendBatch(entries))

	// 不带 Before：返回最新 3 条（id 3,4,5），结果按 ASC 排列
	first, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3})
	require.NoError(t, err)
	assert.Len(t, first, 3)
	assert.Equal(t, "msg-2", first[0].Message) // id=3
	assert.Equal(t, "msg-4", first[2].Message) // id=5

	// Before=first[0].ID（即 id=3）：返回 id<3 的最新 2 条（id 1,2）
	second, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3, Before: first[0].ID})
	require.NoError(t, err)
	assert.Len(t, second, 2)
	assert.Equal(t, "msg-0", second[0].Message) // id=1
	assert.Equal(t, "msg-1", second[1].Message) // id=2

	// Before=second[0].ID（即 id=1）：没有更早的记录
	third, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 3, Before: second[0].ID})
	require.NoError(t, err)
	assert.Len(t, third, 0)
}

// TestFetchBeforeTime 验证 BeforeTime 游标只返回严格早于该时间的日志。
func TestFetchBeforeTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)
	entries := make([]model.LogEntry, 0, 3)
	for i := 0; i < 3; i++ {
		entries = append(entries, model.LogEntry{
			DeploymentID: "dep-1",
			RunID:        "run-1",
			Timestamp:    base.Add(time.Duration(i) * time.Minute),
			Level:        "INFO",
			Message:      fmt.Sprintf("m%d", i),
			Stream:       "stdout",
		})
	}
	require.NoError(t, s.AppendBatch(entries))

	cut := base.Add(1 * time.Minute) // 只应返回 m0。
	got, err := s.Fetch(store.FetchParams{DeploymentID: "dep-1", BeforeTime: &cut})
	require.NoError(t, err)
	if len(got) != 1 || got[0].Message != "m0" {
		t.Fatalf("want [m0], got %+v", got)
	}
}

func TestFetchByRunID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-1", RunID: "run-A", Timestamp: now, Level: "INFO", Message: "run A"},
		{DeploymentID: "svc-1", RunID: "run-B", Timestamp: now, Level: "INFO", Message: "run B"},
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
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: old, Level: "INFO", Message: "old"},
		{DeploymentID: "svc-1", RunID: "run-1", Timestamp: recent, Level: "INFO", Message: "new"},
	}))

	require.NoError(t, s.DeleteOlderThan(7))

	got, err := s.Fetch(store.FetchParams{DeploymentID: "svc-1", Limit: 10})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, "new", got[0].Message)
}

func TestDeleteToMaxBytesRemovesOldest(t *testing.T) {
	s := newTestStore(t)
	base := time.Now().UTC()
	var batch []model.LogEntry
	for i := 0; i < 200; i++ {
		batch = append(batch, model.LogEntry{
			DeploymentID: "A",
			RunID:        "r",
			Timestamp:    base.Add(time.Duration(i) * time.Second),
			Level:        "INFO",
			Message:      "line",
			Stream:       "stdout",
			RepeatCount:  1,
		})
	}
	require.NoError(t, s.AppendBatch(batch))
	before, err := s.SizeBytes()
	require.NoError(t, err)

	deleted, err := s.DeleteToMaxBytes(before - 1)
	require.NoError(t, err)
	require.Positive(t, deleted)

	got, err := s.Fetch(store.FetchParams{DeploymentID: "A", Limit: 1000})
	require.NoError(t, err)
	require.NotEmpty(t, got)
	require.Less(t, len(got), 200)
	assert.True(t, got[0].Timestamp.After(base), "oldest remaining should be newer than base")
}

func TestSearchFindsKeywordAcrossServicesInTimeOrder(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace-8f21 api done", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(1 * time.Second), Level: "WARN", Message: "TRACE-8F21 worker retry", Stream: "stderr"},
		{DeploymentID: "svc-c", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "unrelated", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b", "svc-c"},
		Query:         "trace-8f21",
		Limit:         10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 2)
	assert.Equal(t, "svc-b", got.Entries[0].DeploymentID)
	assert.Equal(t, "svc-a", got.Entries[1].DeploymentID)
	assert.Equal(t, 2, got.Total)
	assert.Equal(t, map[string]int{"svc-a": 1, "svc-b": 1}, got.DeploymentCounts)
}

func TestSearchRestrictsToServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 api", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "trace-8f21 worker", Stream: "stdout"},
	}))

	got, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-b"},
		Query:         "trace-8f21",
		Limit:         10,
	})
	require.NoError(t, err)

	require.Len(t, got.Entries, 1)
	assert.Equal(t, "svc-b", got.Entries[0].DeploymentID)
	assert.Equal(t, map[string]int{"svc-b": 1}, got.DeploymentCounts)
}

func TestSearchPagesAfterCursorWithoutChangingCounts(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 12, 31, 0, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "trace page api 1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "trace page api 2", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "trace page worker 1", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(4 * time.Second), Level: "INFO", Message: "trace page worker 2", Stream: "stdout"},
	}))

	first, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b"},
		Query:         "trace page",
		Limit:         2,
	})
	require.NoError(t, err)
	require.Len(t, first.Entries, 2)
	assert.True(t, first.HasMore)
	assert.Equal(t, 4, first.Total)
	assert.Equal(t, map[string]int{"svc-a": 2, "svc-b": 2}, first.DeploymentCounts)

	cursor := first.Entries[len(first.Entries)-1]
	second, err := s.Search(store.SearchParams{
		DeploymentIDs: []string{"svc-a", "svc-b"},
		Query:         "trace page",
		Limit:         2,
		CursorTime:    &cursor.Timestamp,
		CursorID:      cursor.ID,
	})
	require.NoError(t, err)

	require.Len(t, second.Entries, 2)
	assert.False(t, second.HasMore)
	assert.Equal(t, []string{"trace page worker 1", "trace page worker 2"}, messagesOf(second.Entries))
	assert.Equal(t, 4, second.Total)
	assert.Equal(t, map[string]int{"svc-a": 2, "svc-b": 2}, second.DeploymentCounts)
}

func TestFetchContextReturnsProjectServicesAroundTargetTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "api before", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "worker before", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "trace-8f21 target", Stream: "stderr"},
		{DeploymentID: "svc-c", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "billing after", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Minute), Level: "INFO", Message: "outside window", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	targetID := search.Entries[0].ID

	got, err := s.FetchContext(store.ContextParams{
		TargetID:      targetID,
		DeploymentIDs: []string{"svc-a", "svc-b", "svc-c"},
		Before:        3 * time.Second,
		After:         3 * time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, targetID, got.TargetID)
	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"api before", "trace-8f21 target"}, messagesOf(got.ItemsByDeployment["svc-a"]))
	assert.Equal(t, []string{"worker before"}, messagesOf(got.ItemsByDeployment["svc-b"]))
	assert.Equal(t, []string{"billing after"}, messagesOf(got.ItemsByDeployment["svc-c"]))
}

func TestFetchContextAtReturnsProjectServicesAroundAnchorTime(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-time.Second), Level: "INFO", Message: "api before", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "worker after", Stream: "stdout"},
		{DeploymentID: "svc-c", RunID: "run-1", Timestamp: base.Add(2 * time.Minute), Level: "INFO", Message: "outside window", Stream: "stdout"},
	}))

	got, err := s.FetchContextAt(store.ContextAtParams{
		AnchorTime:    base,
		DeploymentIDs: []string{"svc-a", "svc-b", "svc-c"},
		Before:        2 * time.Second,
		After:         2 * time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"api before"}, messagesOf(got.ItemsByDeployment["svc-a"]))
	assert.Equal(t, []string{"worker after"}, messagesOf(got.ItemsByDeployment["svc-b"]))
	assert.Empty(t, got.ItemsByDeployment["svc-c"])
}

func TestFetchContextUsesTargetDeploymentOutsideContextScope(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-anchor", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "target anchor", Stream: "stderr"},
		{DeploymentID: "svc-peer", RunID: "run-1", Timestamp: base.Add(500 * time.Millisecond), Level: "INFO", Message: "peer context", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-anchor"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	got, err := s.FetchContext(store.ContextParams{
		TargetID:           search.Entries[0].ID,
		TargetDeploymentID: "svc-anchor",
		DeploymentIDs:      []string{"svc-peer"},
		Before:             time.Second,
		After:              time.Second,
	})
	require.NoError(t, err)

	assert.Equal(t, search.Entries[0].ID, got.TargetID)
	assert.Equal(t, base, got.AnchorTime)
	assert.Equal(t, []string{"peer context"}, messagesOf(got.ItemsByDeployment["svc-peer"]))
	_, hasAnchorContext := got.ItemsByDeployment["svc-anchor"]
	assert.False(t, hasAnchorContext)
}

func TestFetchContextLimitsWindowEntriesPerDeployment(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "older", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-time.Second), Level: "INFO", Message: "near before", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "target", Stream: "stderr"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "near after", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	got, err := s.FetchContext(store.ContextParams{
		TargetID:           search.Entries[0].ID,
		TargetDeploymentID: "svc-a",
		DeploymentIDs:      []string{"svc-a"},
		Before:             3 * time.Second,
		After:              3 * time.Second,
		LimitPerDeployment: 2,
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"near before", "target"}, messagesOf(got.ItemsByDeployment["svc-a"]))
}

func TestFetchContextRejectsTargetOutsideServiceSet(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: now, Level: "INFO", Message: "target", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)

	_, err = s.FetchContext(store.ContextParams{
		TargetID:      search.Entries[0].ID,
		DeploymentIDs: []string{"svc-b"},
		Before:        time.Second,
		After:         time.Second,
	})
	require.ErrorIs(t, err, store.ErrLogEntryNotFound)
}

func TestFetchContextPagePagesBeforeAndAfter(t *testing.T) {
	s := newTestStore(t)
	base := time.Date(2026, 5, 20, 22, 41, 32, 0, time.UTC)
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-3 * time.Second), Level: "INFO", Message: "a-3", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-2 * time.Second), Level: "INFO", Message: "a-2", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(-1 * time.Second), Level: "INFO", Message: "a-1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base, Level: "ERROR", Message: "target", Stream: "stderr"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(time.Second), Level: "INFO", Message: "a+1", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "a+2", Stream: "stdout"},
		{DeploymentID: "svc-a", RunID: "run-1", Timestamp: base.Add(3 * time.Second), Level: "INFO", Message: "a+3", Stream: "stdout"},
		{DeploymentID: "svc-b", RunID: "run-1", Timestamp: base.Add(-500 * time.Millisecond), Level: "INFO", Message: "b-near", Stream: "stdout"},
	}))
	search, err := s.Search(store.SearchParams{DeploymentIDs: []string{"svc-a"}, Query: "target", Limit: 1})
	require.NoError(t, err)
	target := search.Entries[0]

	before, err := s.FetchContextPage(store.ContextPageParams{
		DeploymentID: "svc-a",
		CursorTime:   target.Timestamp,
		CursorID:     target.ID,
		Direction:    store.ContextPageBefore,
		Limit:        2,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a-2", "a-1"}, messagesOf(before.Entries))
	assert.True(t, before.HasMore)

	after, err := s.FetchContextPage(store.ContextPageParams{
		DeploymentID: "svc-a",
		CursorTime:   target.Timestamp,
		CursorID:     target.ID,
		Direction:    store.ContextPageAfter,
		Limit:        2,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"a+1", "a+2"}, messagesOf(after.Entries))
	assert.True(t, after.HasMore)
}

func messagesOf(entries []model.LogEntry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.Message
	}
	return out
}
