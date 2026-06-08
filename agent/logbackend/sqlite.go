// sqlite.go 实现基于本地 SQLite + logbuf 的日志后端。
//
// 职责：
//   - Query：从 store.Store 按 DeploymentID/RunID/游标查询历史日志
//   - Search：从 store.Store 按关键字搜索历史日志
//   - Subscribe：从 logbuf.Buffer 订阅实时日志，按 deploymentID 过滤后推送
//
// 边界：
//   - 不直接写日志（写入由 process.Manager → logbuf.Buffer 完成）
//   - Subscribe 返回的 channel 在 Cancel 后关闭，调用方不应再读
package logbackend

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

// SQLiteBackend 从本地 SQLite 读取历史日志，从 logbuf 接收实时日志。
type SQLiteBackend struct {
	store *store.Store
	buf   *logbuf.Buffer
}

var (
	_ LogReader = (*SQLiteBackend)(nil)
	_ LogWriter = (*SQLiteBackend)(nil)
)

// NewSQLiteBackend 创建 SQLiteBackend。
//
// 参数：
//   - s: 已初始化的 SQLite store，用于历史日志查询
//   - buf: 日志环形缓冲，用于实时订阅推送
//
// 返回：
//   - 实现了 LogBackend 接口的 *SQLiteBackend
func NewSQLiteBackend(s *store.Store, buf *logbuf.Buffer) *SQLiteBackend {
	return &SQLiteBackend{store: s, buf: buf}
}

// AppendBatch 批量写入日志到本地 SQLite。
//
// 参数：
//   - ctx: 上下文（当前本地写入不支持取消，保留以满足 LogWriter 契约）
//   - entries: 待写入的日志条目
//
// 返回：
//   - 写入失败时返回 store.AppendBatch 的错误
//
// 注意：
//   - 当前实现只写持久化 store，不反向注入实时 logbuf
func (b *SQLiteBackend) AppendBatch(ctx context.Context, entries []model.LogEntry) error {
	return b.store.AppendBatch(entries)
}

// Query 按 DeploymentID/RunID/不透明游标从 SQLite 拉取历史日志，结果按 timestamp ASC, id ASC 排序。
//
// 参数：
//   - ctx: 上下文（当前实现未使用，保留以满足接口契约）
//   - f: 查询过滤参数，Before.ID 会在 SQLite 后端内部解码成 rowid
//
// 返回：
//   - 匹配的日志条目列表
//   - 下一页游标（取最旧一条的 Timestamp 和 ID）
//   - 查询错误
func (b *SQLiteBackend) Query(ctx context.Context, f QueryFilter) ([]model.LogEntry, Cursor, error) {
	params := store.FetchParams{
		DeploymentID: f.DeploymentID,
		RunID:        f.RunID,
		Limit:        f.Limit,
		Before:       decodeSQLiteCursor(f.Before.ID),
	}

	entries, err := b.store.Fetch(params)
	if err != nil {
		return nil, Cursor{}, err
	}
	var next Cursor
	if len(entries) > 0 {
		first := entries[0]
		next = Cursor{Time: first.Timestamp, ID: encodeSQLiteCursor(first.ID)}
	}
	return entries, next, nil
}

// Search 按关键字从 SQLite 搜索历史日志，结果按 timestamp ASC, id ASC 排序。
//
// 参数：
//   - ctx: 上下文（当前实现未使用，保留以满足接口契约）
//   - q: 搜索参数，DeploymentIDs 不能为空，否则返回空结果
//
// 返回：
//   - 匹配的日志条目列表
//   - 下一页游标
//   - hasMore 是否还有更多匹配结果
//   - 查询错误
func (b *SQLiteBackend) Search(ctx context.Context, q SearchQuery) ([]model.LogEntry, Cursor, bool, error) {
	var from, to *time.Time
	if !q.From.IsZero() {
		from = &q.From
	}
	if !q.To.IsZero() {
		to = &q.To
	}
	var cursorTime *time.Time
	if !q.Cursor.Time.IsZero() {
		cursorTime = &q.Cursor.Time
	}
	result, err := b.store.Search(store.SearchParams{
		DeploymentIDs: q.DeploymentIDs,
		Query:         q.Text,
		Limit:         q.Limit,
		CursorTime:    cursorTime,
		CursorID:      decodeSQLiteCursor(q.Cursor.ID),
		From:          from,
		To:            to,
	})
	if err != nil {
		return nil, Cursor{}, false, err
	}
	var next Cursor
	if len(result.Entries) > 0 {
		last := result.Entries[len(result.Entries)-1]
		next = Cursor{Time: last.Timestamp, ID: encodeSQLiteCursor(last.ID)}
	}
	return result.Entries, next, result.HasMore, nil
}

// Subscribe 订阅实时日志流，可先回放最近日志并按 Since 锚点去重。
//
// 参数：
//   - ctx: 上下文，ctx.Done() 触发时自动停止流并关闭 Ch
//   - opts: 订阅过滤、回放窗口和重连去重锚点
//
// 返回：
//   - LogStream.Ch: 接收过滤后实时日志的 channel，Cancel 或 ctx 取消后关闭
//   - LogStream.Cancel: 主动停止流的函数，调用后 Ch 将被关闭；幂等，可多次调用
//
// 注意：
//   - 消费方过慢时丢弃新日志，不阻塞 logbuf.Buffer 的 Append 调用
//   - 调用方必须最终调用 Cancel 或取消 ctx，否则 goroutine 泄漏
func (b *SQLiteBackend) Subscribe(ctx context.Context, opts SubscribeOptions) LogStream {
	subID := uuid.NewString()
	raw := b.buf.Subscribe(subID)

	ch := make(chan model.LogEntry, 64)

	// once 保证 Unsubscribe 幂等：Cancel 和 ctx.Done 都可能触发，只执行一次
	var once sync.Once
	doCancel := func() {
		once.Do(func() {
			b.buf.Unsubscribe(subID)
		})
	}

	replay := []model.LogEntry{}
	if opts.ReplayLast > 0 {
		for _, e := range b.buf.Recent(opts.ReplayLast) {
			if opts.DeploymentID != "" && e.DeploymentID != opts.DeploymentID {
				continue
			}
			if !sinceAllows(e, opts.Since) {
				continue
			}
			replay = append(replay, e)
		}
	}

	go func() {
		defer close(ch)
		for _, e := range replay {
			select {
			case ch <- e:
			case <-ctx.Done():
				doCancel()
				return
			}
		}
		for {
			select {
			case entry, ok := <-raw:
				if !ok {
					// raw channel 已被 Unsubscribe 关闭，停止循环
					return
				}
				if opts.DeploymentID != "" && entry.DeploymentID != opts.DeploymentID {
					// 过滤掉不属于目标 deploymentID 的条目
					continue
				}
				if !sinceAllows(entry, opts.Since) {
					continue
				}
				select {
				case ch <- entry:
				default:
					// 消费方过慢时丢弃，不阻塞生产者
				}
			case <-ctx.Done():
				// ctx 取消时主动注销订阅并退出
				doCancel()
				return
			}
		}
	}()

	return LogStream{Ch: ch, Cancel: doCancel}
}

// sinceAllows 判断 entry 是否在 Since 锚点之后（只推 > Since 的条目）。
// Since 零值表示不过滤。
func sinceAllows(e model.LogEntry, since Cursor) bool {
	if since.Time.IsZero() && since.ID == "" {
		return true
	}
	if e.Timestamp.After(since.Time) {
		return true
	}
	if e.Timestamp.Equal(since.Time) {
		return encodeSQLiteCursor(e.ID) > since.ID
	}
	return false
}
