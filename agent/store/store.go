// Package store 提供基于 SQLite 的日志持久化存储功能。
//
// 职责：
//   - 批量写入日志条目（AppendBatch）
//   - 按 DeploymentID、RunID 或 ID 游标分页查询日志（Fetch）
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
	"strings"
	"time"

	"github.com/superdev/agent/model"
	_ "modernc.org/sqlite"
)

// ErrLogEntryNotFound 表示目标日志不存在，或不属于允许查询的部署集合。
var ErrLogEntryNotFound = sql.ErrNoRows

// Store 封装 SQLite 数据库连接，提供日志的读写操作。
type Store struct {
	db *sql.DB
}

// FetchParams 定义日志查询的过滤与分页参数。
//
// DeploymentID 和 RunID 可同时指定（AND 关系），也可单独使用。
// Before 为上一页最小 ID，用于实现向前翻页（游标分页）。
type FetchParams struct {
	DeploymentID string
	RunID        string
	Limit        int
	Before       int64
}

// SearchParams 定义跨部署历史日志搜索参数。
//
// DeploymentIDs 为空时直接返回空结果，避免无边界全库搜索。
// Query 会做大小写不敏感的 message 包含匹配。
// CursorTime 和 CursorID 同时指定时，返回游标之后的下一页。
type SearchParams struct {
	DeploymentIDs []string
	Query         string
	Limit         int
	Before        int64
	CursorTime    *time.Time
	CursorID      int64
	From          *time.Time
	To            *time.Time
}

// SearchResult 表示一次日志搜索的结果、分页状态和按部署聚合的命中数。
type SearchResult struct {
	Entries          []model.LogEntry
	Total            int
	DeploymentCounts map[string]int
	HasMore          bool
}

// ContextParams 定义以某条日志为锚点的跨部署上下文查询参数。
type ContextParams struct {
	TargetID      int64
	DeploymentIDs []string
	Before        time.Duration
	After         time.Duration
}

// ContextPageDirection 表示上下文游标分页的方向。
type ContextPageDirection string

const (
	// ContextPageBefore 表示查询游标之前的更早日志。
	ContextPageBefore ContextPageDirection = "before"
	// ContextPageAfter 表示查询游标之后的更新日志。
	ContextPageAfter ContextPageDirection = "after"
)

// ContextPageParams 定义单部署上下文游标分页参数。
type ContextPageParams struct {
	DeploymentID string
	CursorTime   time.Time
	CursorID     int64
	Direction    ContextPageDirection
	Limit        int
}

// ContextResult 表示跨部署上下文查询结果。
type ContextResult struct {
	TargetID          int64
	AnchorTime        time.Time
	ItemsByDeployment map[string][]model.LogEntry
}

// ContextPageResult 表示单部署上下文游标分页结果。
type ContextPageResult struct {
	Entries []model.LogEntry
	HasMore bool
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
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			deployment_id TEXT     NOT NULL,
			run_id        TEXT     NOT NULL,
			timestamp     DATETIME NOT NULL,
			level         TEXT     NOT NULL,
			message       TEXT     NOT NULL,
			stream        TEXT     NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_deployment_id ON log_entries(deployment_id);
		CREATE INDEX IF NOT EXISTS idx_run_id        ON log_entries(run_id);
		CREATE INDEX IF NOT EXISTS idx_timestamp     ON log_entries(timestamp);
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
		INSERT INTO log_entries (deployment_id, run_id, timestamp, level, message, stream)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		if _, err := stmt.Exec(e.DeploymentID, e.RunID, e.Timestamp.UTC(), e.Level, e.Message, e.Stream); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Fetch 按指定参数查询日志条目，结果按 id ASC 排序。
//
// 参数：
//   - p: 查询参数，DeploymentID/RunID 为空则不过滤该字段；
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

	query := `SELECT id, deployment_id, run_id, timestamp, level, message, stream FROM log_entries WHERE 1=1`
	args := []any{}

	if p.DeploymentID != "" {
		query += " AND deployment_id = ?"
		args = append(args, p.DeploymentID)
	}
	if p.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, p.RunID)
	}
	if p.Before > 0 {
		query += " AND id < ?"
		args = append(args, p.Before)
	}
	// 始终用 DESC 取最接近游标（或最新）的 N 条，返回前翻转为 ASC，保证调用方顺序一致
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT %d", p.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.LogEntry
	for rows.Next() {
		var e model.LogEntry
		// modernc.org/sqlite 将 DATETIME 列以 time.Time 形式返回，直接 Scan 避免格式解析歧义。
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// DESC 查询结果翻转为 ASC 顺序返回
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func appendDeploymentArgs(args []any, deploymentIDs []string) []any {
	for _, id := range deploymentIDs {
		args = append(args, id)
	}
	return args
}

// Search 在指定部署集合内按关键词搜索历史日志。
//
// 参数：
//   - p: DeploymentIDs 限定搜索范围，Query 为大小写不敏感关键词，Limit 控制返回条数
//
// 返回：
//   - Entries: 按 timestamp ASC, id ASC 排序的匹配日志
//   - Total: 未分页前的总命中数
//   - DeploymentCounts: 未分页前按 deployment_id 聚合的命中数
//   - HasMore: 当前游标之后是否还有更多匹配日志
func (s *Store) Search(p SearchParams) (SearchResult, error) {
	result := SearchResult{
		Entries:          []model.LogEntry{},
		DeploymentCounts: map[string]int{},
	}
	queryText := strings.TrimSpace(p.Query)
	if len(p.DeploymentIDs) == 0 || queryText == "" {
		return result, nil
	}
	if p.Limit <= 0 {
		p.Limit = 1000
	}

	baseWhere := fmt.Sprintf("deployment_id IN (%s) AND LOWER(message) LIKE LOWER(?)", placeholders(len(p.DeploymentIDs)))
	baseArgs := appendDeploymentArgs([]any{}, p.DeploymentIDs)
	baseArgs = append(baseArgs, "%"+queryText+"%")
	if p.From != nil {
		baseWhere += " AND timestamp >= ?"
		baseArgs = append(baseArgs, p.From.UTC())
	}
	if p.To != nil {
		baseWhere += " AND timestamp <= ?"
		baseArgs = append(baseArgs, p.To.UTC())
	}

	countQuery := "SELECT deployment_id, COUNT(*) FROM log_entries WHERE " + baseWhere + " GROUP BY deployment_id"
	countRows, err := s.db.Query(countQuery, baseArgs...)
	if err != nil {
		return result, err
	}
	defer countRows.Close()
	for countRows.Next() {
		var deploymentID string
		var count int
		if err := countRows.Scan(&deploymentID, &count); err != nil {
			return result, err
		}
		result.DeploymentCounts[deploymentID] = count
		result.Total += count
	}
	if err := countRows.Err(); err != nil {
		return result, err
	}

	entryWhere := baseWhere
	entryArgs := append([]any{}, baseArgs...)
	if p.Before > 0 {
		entryWhere += " AND id < ?"
		entryArgs = append(entryArgs, p.Before)
	}
	if p.CursorTime != nil && !p.CursorTime.IsZero() {
		cursorTime := p.CursorTime.UTC()
		entryWhere += " AND (timestamp > ? OR (timestamp = ? AND id > ?))"
		entryArgs = append(entryArgs, cursorTime, cursorTime, p.CursorID)
	}

	entryQuery := fmt.Sprintf(
		"SELECT id, deployment_id, run_id, timestamp, level, message, stream FROM log_entries WHERE %s ORDER BY timestamp ASC, id ASC LIMIT %d",
		entryWhere,
		p.Limit+1,
	)
	rows, err := s.db.Query(entryQuery, entryArgs...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.LogEntry
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if len(result.Entries) > p.Limit {
		result.HasMore = true
		result.Entries = result.Entries[:p.Limit]
	}
	return result, nil
}

// FetchContext 以目标日志时间为锚点，拉取指定部署集合在时间窗口内的日志。
//
// 参数：
//   - p: TargetID 为锚点日志 ID，DeploymentIDs 限定项目部署集合，Before/After 控制时间窗口
//
// 返回：
//   - 按 deployment_id 分组的日志上下文
//   - 目标日志不存在或不属于 DeploymentIDs 时返回 ErrLogEntryNotFound
func (s *Store) FetchContext(p ContextParams) (ContextResult, error) {
	result := ContextResult{
		TargetID:          p.TargetID,
		ItemsByDeployment: map[string][]model.LogEntry{},
	}
	if p.TargetID <= 0 || len(p.DeploymentIDs) == 0 {
		return result, ErrLogEntryNotFound
	}
	if p.Before <= 0 {
		p.Before = 30 * time.Second
	}
	if p.After <= 0 {
		p.After = 30 * time.Second
	}

	targetQuery := fmt.Sprintf(
		"SELECT timestamp FROM log_entries WHERE id = ? AND deployment_id IN (%s)",
		placeholders(len(p.DeploymentIDs)),
	)
	args := []any{p.TargetID}
	args = appendDeploymentArgs(args, p.DeploymentIDs)
	if err := s.db.QueryRow(targetQuery, args...).Scan(&result.AnchorTime); err != nil {
		if err == sql.ErrNoRows {
			return result, ErrLogEntryNotFound
		}
		return result, err
	}

	for _, deploymentID := range p.DeploymentIDs {
		result.ItemsByDeployment[deploymentID] = []model.LogEntry{}
	}

	from := result.AnchorTime.Add(-p.Before)
	to := result.AnchorTime.Add(p.After)
	contextQuery := fmt.Sprintf(`
		SELECT id, deployment_id, run_id, timestamp, level, message, stream
		FROM log_entries
		WHERE deployment_id IN (%s) AND timestamp >= ? AND timestamp <= ?
		ORDER BY timestamp ASC, id ASC
	`, placeholders(len(p.DeploymentIDs)))
	contextArgs := appendDeploymentArgs([]any{}, p.DeploymentIDs)
	contextArgs = append(contextArgs, from, to)
	rows, err := s.db.Query(contextQuery, contextArgs...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.LogEntry
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return result, err
		}
		result.ItemsByDeployment[e.DeploymentID] = append(result.ItemsByDeployment[e.DeploymentID], e)
	}
	return result, rows.Err()
}

// FetchContextPage 按部署和时间游标继续读取上下文日志。
//
// 参数：
//   - p: DeploymentID 限定单个部署，CursorTime/CursorID 定义当前位置，Direction 控制向前或向后翻页
//
// 返回：
//   - Entries: 按 timestamp ASC, id ASC 排序的日志页
//   - HasMore: 当前方向是否还有更多历史数据
//   - 查询或扫描失败时返回错误
func (s *Store) FetchContextPage(p ContextPageParams) (ContextPageResult, error) {
	result := ContextPageResult{Entries: []model.LogEntry{}}
	if p.DeploymentID == "" || p.CursorTime.IsZero() {
		return result, nil
	}
	if p.Limit <= 0 {
		p.Limit = 200
	}

	order := "ASC"
	comparator := "(timestamp > ? OR (timestamp = ? AND id > ?))"
	if p.Direction == ContextPageBefore {
		order = "DESC"
		comparator = "(timestamp < ? OR (timestamp = ? AND id < ?))"
	} else if p.Direction != ContextPageAfter {
		return result, fmt.Errorf("invalid context page direction: %s", p.Direction)
	}

	query := fmt.Sprintf(`
		SELECT id, deployment_id, run_id, timestamp, level, message, stream
		FROM log_entries
		WHERE deployment_id = ? AND %s
		ORDER BY timestamp %s, id %s
		LIMIT ?
	`, comparator, order, order)
	rows, err := s.db.Query(
		query,
		p.DeploymentID,
		p.CursorTime.UTC(),
		p.CursorTime.UTC(),
		p.CursorID,
		p.Limit+1,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		var e model.LogEntry
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.RunID, &e.Timestamp, &e.Level, &e.Message, &e.Stream); err != nil {
			return result, err
		}
		result.Entries = append(result.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	if len(result.Entries) > p.Limit {
		result.HasMore = true
		result.Entries = result.Entries[:p.Limit]
	}
	if p.Direction == ContextPageBefore {
		for i, j := 0, len(result.Entries)-1; i < j; i, j = i+1, j-1 {
			result.Entries[i], result.Entries[j] = result.Entries[j], result.Entries[i]
		}
	}
	return result, nil
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
