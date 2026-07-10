// Package store 提供基于 SQLite 的日志持久化存储功能。
//
// 职责：
//   - 批量写入日志条目（AppendBatch）
//   - 按 DeploymentID、RunID 或 ID 游标分页查询日志（Fetch）
//   - 清理过期日志（DeleteOlderThan）
//   - 按 SQLite 数据库体积做容量兜底淘汰（DeleteToMaxBytes）
//
// 边界：
//   - 不负责日志解析或格式化，仅存取原始 model.LogEntry
//   - 使用 modernc.org/sqlite（纯 Go，无 CGO）
//   - 写并发通过 SetMaxOpenConns(1) 串行化，避免 SQLITE_BUSY
package store

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	_ "modernc.org/sqlite"
)

const defaultContextLimitPerDeployment = 1000

// ErrLogEntryNotFound 表示目标日志不存在，或不属于允许查询的部署集合。
var ErrLogEntryNotFound = sql.ErrNoRows

// Store 封装 SQLite 数据库连接，提供日志的读写操作。
type Store struct {
	db           *sql.DB
	artifactRoot string
}

// FetchParams 定义日志查询的过滤与分页参数。
//
// DeploymentID 和 RunID 可同时指定（AND 关系），也可单独使用。
type FetchParams struct {
	DeploymentID string
	RunID        string
	Limit        int
	// BeforeSeq 游标分页：只返回 seq < BeforeSeq 的记录；0 表示从最新记录开始。
	// seq 是 per-deployment 单调序，本参数仅在 DeploymentID 非空时有意义。
	BeforeSeq uint64
	// BeforeTime 按时间向前翻页的兜底游标：只返回 timestamp 严格早于该时间的记录。
	// 与 BeforeSeq 可同时指定（AND 关系）；前端裁剪掉无 seq 的实时条目后用它续翻。
	BeforeTime *time.Time
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
	TargetID           int64
	TargetDeploymentID string
	DeploymentIDs      []string
	Before             time.Duration
	After              time.Duration
	LimitPerDeployment int
}

// ContextAtParams 定义以已知锚点时间拉取跨部署上下文的查询参数。
type ContextAtParams struct {
	AnchorTime         time.Time
	DeploymentIDs      []string
	Before             time.Duration
	After              time.Duration
	LimitPerDeployment int
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
	// CursorSeq 是 CursorTime 相同情况下的 per-deployment seq tiebreak。
	// seq 为 0 的旁路日志会在查询内部退回 rowid 兜底。
	CursorSeq uint64
	Direction ContextPageDirection
	Limit     int
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
	artifactRoot := filepath.Join(filepath.Dir(path), "artifacts")
	if path == ":memory:" {
		artifactRoot, err = os.MkdirTemp("", "superdev-artifacts-*")
		if err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	// 限制最大连接数为 1，将写操作串行化，防止 SQLite 并发写冲突。
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db, artifactRoot: artifactRoot}, nil
}

// Close 关闭底层数据库连接，释放资源。
func (s *Store) Close() error { return s.db.Close() }

// migrate 创建日志表和索引（如果不存在）。
//
// 注意：多条 DDL 语句放在一个 Exec 中执行，SQLite 支持此方式。
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return err
	}
	// SQLite 规则：对已存在的库，PRAGMA auto_vacuum 从 none 切到 incremental 不会立即生效，
	// 必须做一次全量 VACUUM 才会真正改写库的 auto_vacuum 模式。历史上老库建于 auto_vacuum
	// 未启用的版本，如果不在此处补一次 VACUUM，incremental_vacuum 永远是空操作、删行只进
	// freelist、文件永不收缩（20GiB logs.db 即由此而来）。这里检测实际模式，仅对未生效的老库
	// 补做一次一次性重整；VACUUM 后 auto_vacuum 落为 incremental，后续启动查到即跳过。
	if err := ensureAutoVacuumEffective(db); err != nil {
		return err
	}
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS log_entries (
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
		CREATE INDEX IF NOT EXISTS idx_deployment_id ON log_entries(deployment_id);
		CREATE INDEX IF NOT EXISTS idx_run_id        ON log_entries(run_id);
		CREATE INDEX IF NOT EXISTS idx_timestamp     ON log_entries(timestamp);

		CREATE TABLE IF NOT EXISTS pipeline_artifacts (
			project_id  TEXT NOT NULL,
			pipeline_id TEXT NOT NULL,
			version     TEXT NOT NULL,
			kind        TEXT NOT NULL,
			location    TEXT NOT NULL,
			meta_json   TEXT NOT NULL,
			created_at  INTEGER NOT NULL,
			PRIMARY KEY(project_id, pipeline_id, version)
		);
		CREATE INDEX IF NOT EXISTS idx_pipeline_artifacts_created
			ON pipeline_artifacts(project_id, pipeline_id, created_at DESC);

		CREATE TABLE IF NOT EXISTS pipeline_runs (
			id               TEXT PRIMARY KEY,
			project_id       TEXT NOT NULL,
			pipeline_id      TEXT NOT NULL,
			env_name         TEXT NOT NULL,
			deployment_id    TEXT NOT NULL,
			artifact_version TEXT NOT NULL,
			status           TEXT NOT NULL,
			started_at       INTEGER NOT NULL,
			finished_at      INTEGER NOT NULL,
			run_json         TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_pipeline_runs_project_pipeline
			ON pipeline_runs(project_id, pipeline_id, started_at DESC);

		CREATE TABLE IF NOT EXISTS pipeline_run_logs (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id    TEXT NOT NULL,
			step_name TEXT NOT NULL,
			host_id   TEXT NOT NULL,
			host_name TEXT NOT NULL DEFAULT '',
			stream    TEXT NOT NULL,
			line      TEXT NOT NULL,
			at        INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_pipeline_run_logs_filter
			ON pipeline_run_logs(run_id, step_name, host_id, id);
	`)
	if err != nil {
		return err
	}
	// 旧库没有 fold_key 列，唯一索引必须等列补齐后再创建。
	if err := ensureLogEntriesFoldColumns(db); err != nil {
		return err
	}
	if err := ensureLogEntriesSeqColumns(db); err != nil {
		return err
	}
	return ensurePipelineRunLogsHostNameColumn(db)
}

func ensureLogEntriesFoldColumns(db *sql.DB) error {
	cols, err := tableColumns(db, "log_entries")
	if err != nil {
		return err
	}
	if !cols["repeat_count"] {
		if _, err := db.Exec(`ALTER TABLE log_entries ADD COLUMN repeat_count INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	if !cols["fold_key"] {
		if _, err := db.Exec(`ALTER TABLE log_entries ADD COLUMN fold_key TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	// fold_key 折叠 upsert 的冲突键必须带 run_id 维度。
	// 旧库用的是单列 idx_fold_key（仅 fold_key 唯一），会导致 agent 重启后新会话
	// 的 fold_key 与历史撞键、新日志被 UPDATE 进旧行（实时日志卡死）。
	// 这里把唯一约束迁移为 (run_id, fold_key) 复合：drop 旧索引、清空历史折叠脏数据、建复合唯一索引。
	if err := migrateFoldKeyUniqueIndex(db); err != nil {
		return err
	}
	return nil
}

// tableColumns 返回指定表已存在的列名集合，供幂等迁移判断是否需要 ALTER TABLE。
func tableColumns(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

// ensureLogEntriesSeqColumns 为旧库补齐 seq/last_seen_at 列并回填 seq。
//
// seq 是 per-deployment 单调逻辑身份（spec §3.1）。回填用 ROW_NUMBER 按 id 序
// 生成，保证与历史写入顺序一致；回填后建 (deployment_id, seq) 唯一索引，
// 任何重复分配都会在写入时立刻暴露而不是静默覆盖。
func ensureLogEntriesSeqColumns(db *sql.DB) error {
	cols, err := tableColumns(db, "log_entries")
	if err != nil {
		return err
	}
	seqColumnAdded := !cols["seq"]
	if seqColumnAdded {
		if _, err := db.Exec(`ALTER TABLE log_entries ADD COLUMN seq INTEGER`); err != nil {
			return err
		}
	}
	if !cols["last_seen_at"] {
		if _, err := db.Exec(`ALTER TABLE log_entries ADD COLUMN last_seen_at DATETIME`); err != nil {
			return err
		}
	}
	needsBackfill, err := logEntriesNeedSeqBackfill(db, seqColumnAdded)
	if err != nil {
		return err
	}
	if !needsBackfill {
		return nil
	}
	// 只回填 seq 为 NULL 的行，迁移幂等；UPDATE...FROM 需 SQLite>=3.33（modernc 满足）。
	if _, err := db.Exec(`
		UPDATE log_entries SET seq = t.rn
		FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY deployment_id ORDER BY id) AS rn FROM log_entries) AS t
		WHERE log_entries.id = t.id AND log_entries.seq IS NULL`); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_dep_seq ON log_entries(deployment_id, seq)`)
	return err
}

// logEntriesNeedSeqBackfill 判断是否需要执行昂贵的 seq 窗口回填。
//
// 参数：
//   - db: 已打开并完成基础表结构迁移的 SQLite 连接
//   - seqColumnAdded: 本轮迁移是否刚添加 seq 列
//
// 返回：
//   - true 表示存在需要回填的 NULL seq
//   - 查询过程中的错误
//
// 注意：
//   - 已全量回填的库必须跳过 ROW_NUMBER 全表窗口计算，否则每次 agent 启动都会
//     在 HTTP listener 创建前重复扫描大日志库。
func logEntriesNeedSeqBackfill(db *sql.DB, seqColumnAdded bool) (bool, error) {
	if seqColumnAdded {
		return true, nil
	}
	var exists int
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM log_entries WHERE seq IS NULL LIMIT 1)`).Scan(&exists); err != nil {
		return false, err
	}
	return exists != 0, nil
}

// migrateFoldKeyUniqueIndex 将 fold_key 唯一约束从单列迁移为 (run_id, fold_key) 复合。
//
// 注意：
//   - 旧的单列 idx_fold_key 必须先 drop，否则历史 fold_key 仍会跨 run 误撞
//   - 历史日志的 fold_key 是旧自增空间的产物，与新 (run_id, fold_key) 语义不兼容，
//     直接清空 log_entries（过往日志可丢弃），避免新旧 fold_key 混在一张表里产生歧义
func migrateFoldKeyUniqueIndex(db *sql.DB) error {
	hasComposite, err := indexExists(db, "idx_run_fold_key")
	if err != nil {
		return err
	}
	if hasComposite {
		return nil
	}
	// 旧单列唯一索引存在则先删除，让冲突键彻底切到复合维度。
	if _, err := db.Exec(`DROP INDEX IF EXISTS idx_fold_key`); err != nil {
		return err
	}
	// 清空历史折叠脏数据：旧 fold_key 属于已废弃的自增空间，保留会与新 run 维度冲突键混淆。
	if _, err := db.Exec(`DELETE FROM log_entries`); err != nil {
		return err
	}
	_, err = db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_run_fold_key ON log_entries(run_id, fold_key) WHERE fold_key != ''`)
	return err
}

// autoVacuumIncremental 是 SQLite PRAGMA auto_vacuum 处于 INCREMENTAL 模式时的取值。
// 取值含义：0=NONE，1=FULL，2=INCREMENTAL。
const autoVacuumIncremental = 2

// ensureAutoVacuumEffective 确保库的 auto_vacuum 真正处于 INCREMENTAL 模式。
//
// 背景：
//   - SQLite 对已存在的库改 auto_vacuum（none→incremental）不会立即生效，
//     必须做一次全量 VACUUM 才会落地。老版本建的库 auto_vacuum 实为 NONE，
//     导致 incremental_vacuum 空转、删行只进 freelist、logs.db 文件永不收缩。
//
// 行为：
//   - 读回实际 auto_vacuum 值；已是 INCREMENTAL 则直接返回（新库/已重整过的库走这里，零开销）
//   - 未生效的老库执行一次 VACUUM：它会改写模式并物理回收所有 freelist 空间，
//     文件立刻缩到真实数据量。这是一次性操作，之后启动查到 INCREMENTAL 即跳过。
//
// 注意：
//   - VACUUM 会重建整库、需要约等量的临时磁盘空间并持有写锁，对超大老库可能耗时较久；
//     这是把历史欠账一次性还清的必要代价，故打印起止日志便于观测启动为何变慢。
func ensureAutoVacuumEffective(db *sql.DB) error {
	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return fmt.Errorf("read auto_vacuum mode: %w", err)
	}
	if mode == autoVacuumIncremental {
		return nil
	}
	// 老库：auto_vacuum 未生效，做一次性 VACUUM 使其落地并回收历史 freelist 空间。
	log.Printf("[store] logs.db auto_vacuum 未生效(mode=%d)，执行一次性 VACUUM 重整以启用增量回收并收缩文件，超大库可能耗时较久", mode)
	start := time.Now()
	if _, err := db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("vacuum to enable auto_vacuum: %w", err)
	}
	log.Printf("[store] logs.db VACUUM 重整完成，耗时=%s，auto_vacuum 已切换为 INCREMENTAL", time.Since(start).Round(time.Millisecond))
	return nil
}

// indexExists 报告指定名称的索引是否已存在于库中。
func indexExists(db *sql.DB, name string) (bool, error) {
	var found string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, name).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func ensurePipelineRunLogsHostNameColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(pipeline_run_logs)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "host_name" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE pipeline_run_logs ADD COLUMN host_name TEXT NOT NULL DEFAULT ''`)
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
	// 折叠 upsert 的冲突键是 (run_id, fold_key)：同一 run 内同 fold_key 累加计数，
	// 不同 run（含 agent 重启后的新会话）的同 fold_key 互不干扰，各自落新行。
	// UPSERT 不回写 timestamp，折叠段最新出现时间写入 last_seen_at。
	stmt, err := tx.Prepare(`
		INSERT INTO log_entries (deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key, seq, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, fold_key) WHERE fold_key != '' DO UPDATE SET
			repeat_count = excluded.repeat_count,
			last_seen_at = excluded.last_seen_at
	`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		repeatCount := e.RepeatCount
		if repeatCount <= 0 {
			repeatCount = 1
		}
		entryTime := e.Timestamp.UTC()
		if _, err := stmt.Exec(e.DeploymentID, e.RunID, entryTime, e.Level, e.Message, e.Stream, repeatCount, e.FoldKey, nullableSeq(e.Seq), entryTime); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// nullableSeq 把 0 值 seq 映射为 NULL：0 表示"未分配"（如 __desktop__ 打点等
// 未来可能的旁路），NULL 不参与 (deployment_id, seq) 唯一约束，不会互相撞键。
func nullableSeq(seq uint64) any {
	if seq == 0 {
		return nil
	}
	return int64(seq)
}

// scanLogEntry 扫描标准日志列清单，确保所有读路径一致带回 seq 与 last_seen_at。
func scanLogEntry(rows *sql.Rows) (model.LogEntry, error) {
	var e model.LogEntry
	var seq sql.NullInt64
	var lastSeen sql.NullTime
	err := rows.Scan(
		&e.ID,
		&e.DeploymentID,
		&e.RunID,
		&e.Timestamp,
		&e.Level,
		&e.Message,
		&e.Stream,
		&e.RepeatCount,
		&e.FoldKey,
		&seq,
		&lastSeen,
	)
	if err != nil {
		return e, err
	}
	if seq.Valid && seq.Int64 > 0 {
		e.Seq = uint64(seq.Int64)
	}
	if lastSeen.Valid {
		t := lastSeen.Time
		e.LastSeenAt = &t
	}
	return e, nil
}

// SeqWatermarks 返回每个 deployment 已分配的最大 seq，供 logbuf 启动时恢复水位。
//
// 返回：
//   - map[deploymentID]maxSeq；无日志的 deployment 不出现在 map 中
//   - 查询失败返回错误（调用方必须拒绝启动，不可静默从 1 开始撞历史）
func (s *Store) SeqWatermarks() (map[string]uint64, error) {
	rows, err := s.db.Query(`SELECT deployment_id, MAX(seq) FROM log_entries WHERE seq IS NOT NULL GROUP BY deployment_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]uint64{}
	for rows.Next() {
		var dep string
		var max sql.NullInt64
		if err := rows.Scan(&dep, &max); err != nil {
			return nil, err
		}
		if max.Valid {
			out[dep] = uint64(max.Int64)
		}
	}
	return out, rows.Err()
}

// Fetch 按指定参数查询日志条目，结果按 seq ASC 排序。
//
// 参数：
//   - p: 查询参数，DeploymentID/RunID 为空则不过滤该字段；
//     BeforeSeq > 0 时仅返回 seq < BeforeSeq 的记录（用于向前翻页）；
//     BeforeTime 非空时仅返回 timestamp 早于该时间的记录；
//     Limit <= 0 时默认取 1000 条。
//
// 返回：
//   - 匹配的日志条目列表
//   - 查询或扫描失败时返回错误
func (s *Store) Fetch(p FetchParams) ([]model.LogEntry, error) {
	if p.Limit <= 0 {
		p.Limit = 1000
	}

	query := `SELECT id, deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key, seq, last_seen_at FROM log_entries WHERE 1=1`
	args := []any{}

	if p.DeploymentID != "" {
		query += " AND deployment_id = ?"
		args = append(args, p.DeploymentID)
	}
	if p.RunID != "" {
		query += " AND run_id = ?"
		args = append(args, p.RunID)
	}
	if p.BeforeSeq > 0 {
		query += " AND seq < ?"
		args = append(args, int64(p.BeforeSeq))
	}
	if p.BeforeTime != nil {
		query += " AND timestamp < ?"
		args = append(args, p.BeforeTime.UTC())
	}
	// 始终用 DESC 取最接近游标（或最新）的 N 条，返回前翻转为 ASC，保证调用方顺序一致。
	// seq 是正常日志身份；COALESCE 仅服务 seq 为 NULL 的旁路/测试数据，避免旧路径无序。
	query += fmt.Sprintf(" ORDER BY COALESCE(seq, id) DESC LIMIT %d", p.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []model.LogEntry
	for rows.Next() {
		// modernc.org/sqlite 将 DATETIME 列以 time.Time 形式返回，直接 Scan 避免格式解析歧义。
		e, err := scanLogEntry(rows)
		if err != nil {
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
		"SELECT id, deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key, seq, last_seen_at FROM log_entries WHERE %s ORDER BY timestamp ASC, id ASC LIMIT %d",
		entryWhere,
		p.Limit+1,
	)
	rows, err := s.db.Query(entryQuery, entryArgs...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
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
	result := ContextResult{TargetID: p.TargetID, ItemsByDeployment: map[string][]model.LogEntry{}}
	if p.TargetID <= 0 || len(p.DeploymentIDs) == 0 {
		return result, ErrLogEntryNotFound
	}

	targetQuery := "SELECT timestamp FROM log_entries WHERE id = ?"
	args := []any{p.TargetID}
	if p.TargetDeploymentID != "" {
		// TargetDeploymentID identifies the selected log; DeploymentIDs remains the context scope.
		targetQuery += " AND deployment_id = ?"
		args = append(args, p.TargetDeploymentID)
	} else {
		targetQuery = fmt.Sprintf("%s AND deployment_id IN (%s)", targetQuery, placeholders(len(p.DeploymentIDs)))
		args = appendDeploymentArgs(args, p.DeploymentIDs)
	}
	if err := s.db.QueryRow(targetQuery, args...).Scan(&result.AnchorTime); err != nil {
		if err == sql.ErrNoRows {
			return result, ErrLogEntryNotFound
		}
		return result, err
	}

	return s.fetchContextWindow(p.TargetID, result.AnchorTime, p.DeploymentIDs, p.Before, p.After, p.LimitPerDeployment)
}

// FetchContextAt 以已知锚点时间拉取指定部署集合在时间窗口内的日志。
//
// 参数：
//   - p: AnchorTime 为上下文中心时间，DeploymentIDs 限定部署集合，Before/After 控制时间窗口
//
// 返回：
//   - 按 deployment_id 分组的日志上下文
//   - 锚点时间为空或部署集合为空时返回 ErrLogEntryNotFound
func (s *Store) FetchContextAt(p ContextAtParams) (ContextResult, error) {
	result := ContextResult{AnchorTime: p.AnchorTime, ItemsByDeployment: map[string][]model.LogEntry{}}
	if p.AnchorTime.IsZero() || len(p.DeploymentIDs) == 0 {
		return result, ErrLogEntryNotFound
	}
	return s.fetchContextWindow(0, p.AnchorTime, p.DeploymentIDs, p.Before, p.After, p.LimitPerDeployment)
}

func (s *Store) fetchContextWindow(targetID int64, anchorTime time.Time, deploymentIDs []string, before time.Duration, after time.Duration, limitPerDeployment int) (ContextResult, error) {
	result := ContextResult{
		TargetID:          targetID,
		AnchorTime:        anchorTime,
		ItemsByDeployment: map[string][]model.LogEntry{},
	}
	if before <= 0 {
		before = 30 * time.Second
	}
	if after <= 0 {
		after = 30 * time.Second
	}
	if limitPerDeployment <= 0 {
		limitPerDeployment = defaultContextLimitPerDeployment
	}

	from := result.AnchorTime.Add(-before)
	to := result.AnchorTime.Add(after)
	beforeLimit := limitPerDeployment / 2
	afterLimit := limitPerDeployment - beforeLimit
	for _, deploymentID := range deploymentIDs {
		result.ItemsByDeployment[deploymentID] = []model.LogEntry{}
		if beforeLimit > 0 {
			beforeEntries, err := s.fetchContextHalfWindow(deploymentID, from, result.AnchorTime, beforeLimit, true)
			if err != nil {
				return result, err
			}
			result.ItemsByDeployment[deploymentID] = append(result.ItemsByDeployment[deploymentID], beforeEntries...)
		}
		if afterLimit > 0 {
			afterEntries, err := s.fetchContextHalfWindow(deploymentID, result.AnchorTime, to, afterLimit, false)
			if err != nil {
				return result, err
			}
			result.ItemsByDeployment[deploymentID] = append(result.ItemsByDeployment[deploymentID], afterEntries...)
		}
	}
	return result, nil
}

func (s *Store) fetchContextHalfWindow(deploymentID string, from time.Time, to time.Time, limit int, beforeAnchor bool) ([]model.LogEntry, error) {
	comparator := "timestamp >= ? AND timestamp < ?"
	order := "DESC"
	if !beforeAnchor {
		comparator = "timestamp >= ? AND timestamp <= ?"
		order = "ASC"
	}
	query := fmt.Sprintf(`
		SELECT id, deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key, seq, last_seen_at
		FROM log_entries
		WHERE deployment_id = ? AND %s
		ORDER BY timestamp %s, id %s
		LIMIT ?
	`, comparator, order, order)
	rows, err := s.db.Query(query, deploymentID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []model.LogEntry{}
	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if beforeAnchor {
		for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
			entries[i], entries[j] = entries[j], entries[i]
		}
	}
	return entries, nil
}

// FetchContextPage 按部署和时间游标继续读取上下文日志。
//
// 参数：
//   - p: DeploymentID 限定单个部署，CursorTime/CursorSeq 定义当前位置，Direction 控制向前或向后翻页
//
// 返回：
//   - Entries: 按 timestamp ASC, seq ASC 排序的日志页
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
	cursorSeq := int64(p.CursorSeq)
	cursorKey := "COALESCE(seq, id)"
	comparator := fmt.Sprintf("(timestamp > ? OR (timestamp = ? AND %s > ?))", cursorKey)
	if p.Direction == ContextPageBefore {
		order = "DESC"
		comparator = fmt.Sprintf("(timestamp < ? OR (timestamp = ? AND %s < ?))", cursorKey)
	} else if p.Direction != ContextPageAfter {
		return result, fmt.Errorf("invalid context page direction: %s", p.Direction)
	}

	query := fmt.Sprintf(`
		SELECT id, deployment_id, run_id, timestamp, level, message, stream, repeat_count, fold_key, seq, last_seen_at
		FROM log_entries
		WHERE deployment_id = ? AND %s
		ORDER BY timestamp %s, %s %s
		LIMIT ?
	`, comparator, order, cursorKey, order)
	rows, err := s.db.Query(
		query,
		p.DeploymentID,
		p.CursorTime.UTC(),
		p.CursorTime.UTC(),
		cursorSeq,
		p.Limit+1,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()

	for rows.Next() {
		e, err := scanLogEntry(rows)
		if err != nil {
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

// SizeBytes 返回数据库当前占用字节数（page_count * page_size）。
//
// 返回：
//   - SQLite 当前页数与页大小相乘得到的整库体积
//   - PRAGMA 查询失败时返回错误
//
// 注意：
//   - 这是整库体积（含所有表与空闲页），用于容量兜底淘汰的水位判断；
//     轻量 PRAGMA 查询，不扫表。
func (s *Store) SizeBytes() (int64, error) {
	var pageCount, pageSize int64
	if err := s.db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, err
	}
	return pageCount * pageSize, nil
}

// deleteBatchSize 是容量淘汰每批删除的行数，避免单次大事务长锁。
const deleteBatchSize = 100

// DeleteToMaxBytes 当库体积超过 maxBytes 时，按 id 升序（最旧优先）分批删除，
// 直到体积回落到 maxBytes 以下、无更多日志可删，或 SQLite 暂未释放更多页。
//
// 参数：
//   - maxBytes: 体积上限；<=0 时视为不限，直接返回
//
// 返回：
//   - 删除的总行数
//   - 删除或度量失败时返回错误
//
// 注意：
//   - 行为是"丢最旧保运行"：宁可删未到保留期的老日志，也不让磁盘被打爆
//   - 停止判据是"删无可删"（本批 RowsAffected==0），而非"体积不再下降"。
//     历史实现用后者，在 auto_vacuum 未生效的老库上，删行后 page_count 纹丝不动，
//     第二轮就误判"体积没降"直接返回——每轮只删 100 行就放弃，永远追不上写入。
//     只要还删得到行就继续删，才能真正把体积压回上限之下（ensureAutoVacuumEffective
//     已保证 auto_vacuum 生效，incremental_vacuum 会真正回收页、体积随之下降）。
func (s *Store) DeleteToMaxBytes(maxBytes int64) (int64, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	var total int64
	for {
		size, err := s.SizeBytes()
		if err != nil {
			return total, err
		}
		if size <= maxBytes {
			return total, nil
		}

		res, err := s.db.Exec(`
			DELETE FROM log_entries
			WHERE id IN (SELECT id FROM log_entries ORDER BY id ASC LIMIT ?)
		`, deleteBatchSize)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		// 删无可删：日志已全部清空仍未达标（阈值被设得比空库还小，或空间全被
		// 索引/其他表占据），继续循环也删不动，停止避免空转死循环。
		if n == 0 {
			return total, nil
		}
		_, _ = s.db.Exec("PRAGMA incremental_vacuum")
	}
}
