// seq_migration_test.go 验证日志 seq 迁移的启动热路径保护。
//
// 职责：
//   - 证明已完成 seq 回填的日志库不会重复触发全表窗口回填
//   - 证明新列或残留 NULL seq 仍会触发必要回填
//
// 边界：
//   - 不覆盖日志写入/查询行为，相关行为由 store_test.go 验证
//   - 不依赖真实磁盘上的生产 logs.db
package store

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogEntriesNeedSeqBackfillSkipsFullyBackfilledStore(t *testing.T) {
	db := newSeqMigrationTestDB(t)

	needs, err := logEntriesNeedSeqBackfill(db, false)

	require.NoError(t, err)
	assert.False(t, needs)
}

func TestLogEntriesNeedSeqBackfillRunsForNewColumnOrNullRows(t *testing.T) {
	db := newSeqMigrationTestDB(t)

	needs, err := logEntriesNeedSeqBackfill(db, true)
	require.NoError(t, err)
	assert.True(t, needs)

	_, err = db.Exec(`INSERT INTO log_entries (deployment_id, seq) VALUES ('dep-a', NULL)`)
	require.NoError(t, err)

	needs, err = logEntriesNeedSeqBackfill(db, false)
	require.NoError(t, err)
	assert.True(t, needs)
}

func newSeqMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	_, err = db.Exec(`CREATE TABLE log_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		deployment_id TEXT NOT NULL,
		seq INTEGER
	);
	INSERT INTO log_entries (deployment_id, seq) VALUES ('dep-a', 1), ('dep-a', 2)`)
	require.NoError(t, err)
	return db
}
