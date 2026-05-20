// Package store 提供基于 SQLite 的日志持久化存储功能。
//
// 职责：
//   - 批量写入日志条目（AppendBatch）
//   - 按服务ID、RunID 或 ID 游标分页查询日志（Fetch）
//   - 清理过期日志（DeleteOlderThan）
//
// 边界：
//   - 不负责日志解析或格式化，仅存取原始 model.LogEntry
//   - 使用 modernc.org/sqlite（纯 Go，无 CGO）
//   - 写并发通过 SetMaxOpenConns(1) 串行化，避免 SQLITE_BUSY
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/superdev/agent/model"
	_ "modernc.org/sqlite"
)

// Store 封装 SQLite 数据库连接，提供日志的读写操作。
type Store struct {
	db *sql.DB
}

// FetchParams 定义日志查询的过滤与分页参数。
//
// ServiceID 和 RunID 可同时指定（AND 关系），也可单独使用。
// Before 为上一页最小 ID，用于实现向前翻页（游标分页）。
type FetchParams struct {
	ServiceID string
	RunID     string
	Limit     int
	Before    int64
}

// New 打开（或创建）指定路径的 SQLite 数据库，并执行 schema 迁移。
//
// 参数：
//   - path: SQLite 文件路径，传入 ":memory:" 可创建内存数据库（适合测试）
//
// 返回：
//   - 初始化完成的 Store 实例
//   - 打开或迁移失败时返回错误
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// 限制最大连接数为 1，将写操作串行化，防止 SQLite 并发写冲突。
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close 关闭底层数据库连接，释放资源。
func (s *Store) Close() error { return s.db.Close() }

// migrate 创建日志表和索引（如果不存在）。
//
// 注意：多条 DDL 语句放在一个 Exec 中执行，SQLite 支持此方式。
func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS log_entries (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			service_id TEXT     NOT NULL,
			run_id     TEXT     NOT NULL,
			timestamp  DATETIME NOT NULL,
			level      TEXT     NOT NULL,
			message    TEXT     NOT NULL,
			stream     TEXT     NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_service_id ON log_entries(service_id);
		CREATE INDEX IF NOT EXISTS idx_run_id     ON log_entries(run_id);
		CREATE INDEX IF NOT EXISTS idx_timestamp  ON log_entries(timestamp);
	`)
	return err
}

// AppendBatch 在单个事务中批量插入日志条目。
//
// 参数：
//   - entries: 待插入的日志条目列表，为空时直接返回 nil
//
// 返回：
//   - 任意一条插入失败时回滚事务并返回错误
func (s *Store) AppendBatch(entries []model.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO log_entries (service_id, run_id, timestamp, level, message, stream)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.Exec(e.ServiceID, e.RunID, e.Timestamp.UTC(), e.Level, e.Message, e.Stream); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Fetch 按指定参数查询日志条目，结果按 id ASC 排序。
//
// 参数：
//   - p: 查询参数，ServiceID/RunID 为空则不过滤该字段；
//     Before > 0 时仅返回 id < Before 的记录（用于向前翻页）；
//     Limit <= 0 时默认取 1000 条。
//
// 返回：
//   - 匹配的日志条目列表
//   - 查询或扫描失败时返回错误
func (s *Store) Fetch(p FetchParams) ([]model.LogEntry, error) {
	if p.Limit <= 0 {
		p.Limit = 1000
	}

	query := `SELECT id, service_id, run_id, timestamp, level, message, stream FROM log_entries WHERE 1=1`
	args := []any{}

	if p.ServiceID != "" {
		query += " AND service_id = ?"
		args = append(args, p.ServiceID)
	}
	if p.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, p.RunID)
	}
	if p.Before > 0 {
		query += " AND id < ?"
		args = append(args, p.Before)
	}
	query += fmt.Sprintf(" ORDER BY id ASC LIMIT %d", p.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.LogEntry
	for rows.Next() {
		var e model.LogEntry
		// modernc.org/sqlite 将 DATETIME 列以 time.Time 形式返回，直接 Scan 避免格式解析歧义。
		if err := rows.Scan(&e.ID, &e.ServiceID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// DeleteOlderThan 删除超过指定天数的日志条目。
//
// 参数：
//   - days: 保留最近 N 天的日志，早于此时间点的记录将被删除
//
// 返回：
//   - 删除操作失败时返回错误
func (s *Store) DeleteOlderThan(days int) error {
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	_, err := s.db.Exec("DELETE FROM log_entries WHERE timestamp < ?", cutoff)
	return err
}
