// federated.go 实现多节点聚合日志后端。
//
// 职责：
//   - Query：并发调所有子 backend，k-way min-heap 归并，结果按 timestamp ASC, id ASC
//   - Search：并发调所有子 backend，归并排序后截取 limit
//   - Subscribe：fan-in 所有子 backend 的实时流，Cancel 时统一停止所有子流
//
// 边界：
//   - 子 backend 错误时降级（跳过该节点），不影响其他节点
//   - 不关心子 backend 的具体类型（可混合 SQLite + Remote）
package logbackend

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// defaultLimit 是 Query/Search 未指定 Limit 时的默认最大返回条数。
const defaultLimit = 1000

const federatedCursorPrefix = "fed:"

type federatedCursorState struct {
	Cursor    Cursor
	Exhausted bool
}

type federatedCursorWire struct {
	Time      string `json:"time,omitempty"`
	ID        string `json:"id,omitempty"`
	Exhausted bool   `json:"exhausted,omitempty"`
}

type federatedQueryResult struct {
	child   int
	entries []model.LogEntry
}

// FederatedBackend 聚合多个子 LogBackend，实现跨节点日志统一访问。
type FederatedBackend struct {
	children []LogBackend
}

// NewFederatedBackend 创建 FederatedBackend。
//
// 参数：
//   - children: 子 LogBackend 列表，可混合 SQLite、Remote 等不同类型
//
// 返回：
//   - 聚合了所有子 backend 的 FederatedBackend 实例
func NewFederatedBackend(children []LogBackend) *FederatedBackend {
	return &FederatedBackend{children: children}
}

// Query 并发拉取所有子 backend 的历史日志，取全局最新 limit 条后按升序返回。
//
// 参数：
//   - ctx: 上下文，用于控制超时和取消
//   - filter: 查询过滤条件，包含 DeploymentID、时间范围、Cursor、Limit 等
//
// 返回：
//   - 按 timestamp ASC, id ASC 排序的日志条目列表
//   - 联邦层不透明游标，内部记录每个子 backend 的独立位置
//   - 错误信息（子 backend 错误时降级跳过，不向上传播）
//
// 注意：
//   - Limit <= 0 时使用 defaultLimit（1000）
//   - 子 backend 返回错误时静默降级，不影响其他节点结果
func (f *FederatedBackend) Query(ctx context.Context, filter QueryFilter) ([]model.LogEntry, Cursor, error) {
	results := make([]federatedQueryResult, len(f.children))
	childCursors, hasFederatedCursor := decodeFederatedCursor(filter.Before.ID)
	var wg sync.WaitGroup
	for i, child := range f.children {
		childCursor := childCursors[i]
		if !hasFederatedCursor {
			childCursor.Cursor = filter.Before
		}
		if childCursor.Exhausted {
			results[i] = federatedQueryResult{child: i}
			continue
		}

		childFilter := filter
		childFilter.Before = childCursor.Cursor
		wg.Add(1)
		go func(idx int, b LogBackend, cf QueryFilter) {
			defer wg.Done()
			entries, _, _ := b.Query(ctx, cf)
			results[idx] = federatedQueryResult{child: idx, entries: entries}
		}(i, child, childFilter)
	}
	wg.Wait()

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	merged := mergeLatestFederatedResults(results, limit)
	entries := make([]model.LogEntry, 0, len(merged))
	emittedCount := map[int]int{}
	oldestEmitted := map[int]model.LogEntry{}
	for _, item := range merged {
		entries = append(entries, item.entry)
		emittedCount[item.child]++
		if current, ok := oldestEmitted[item.child]; !ok || lessLogEntry(item.entry, current) {
			oldestEmitted[item.child] = item.entry
		}
	}

	var next Cursor
	if len(entries) > 0 {
		next = Cursor{Time: entries[0].Timestamp, ID: encodeFederatedCursor(nextFederatedCursorStates(results, childCursors, filter.Before, hasFederatedCursor, emittedCount, oldestEmitted, limit))}
	}
	return entries, next, nil
}

// Search 并发搜索所有子 backend，归并排序后截取 limit。
//
// 参数：
//   - ctx: 上下文，用于控制超时和取消
//   - q: 搜索查询条件，包含关键词、DeploymentID、时间范围、Limit 等
//
// 返回：
//   - 按 timestamp ASC, id ASC 排序的匹配日志条目列表
//   - 指向最后一条记录的游标，供下一页查询使用
//   - 是否还有更多结果（任一子 backend 返回 hasMore=true 或截取时丢弃了数据）
//   - 错误信息（子 backend 错误时降级跳过，不向上传播）
//
// 注意：
//   - Limit <= 0 时使用 defaultLimit（1000）
//   - 子 backend 返回错误时静默降级，不影响其他节点结果
func (f *FederatedBackend) Search(ctx context.Context, q SearchQuery) ([]model.LogEntry, Cursor, bool, error) {
	type result struct {
		entries []model.LogEntry
		hasMore bool
	}
	results := make([]result, len(f.children))
	var wg sync.WaitGroup
	for i, child := range f.children {
		wg.Add(1)
		go func(idx int, b LogBackend) {
			defer wg.Done()
			entries, _, hasMore, _ := b.Search(ctx, q)
			results[idx] = result{entries: entries, hasMore: hasMore}
		}(i, child)
	}
	wg.Wait()

	var all []model.LogEntry
	hasMore := false
	for _, r := range results {
		all = append(all, r.entries...)
		if r.hasMore {
			hasMore = true
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return lessLogEntry(all[i], all[j])
	})

	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if len(all) > limit {
		all = all[:limit]
		hasMore = true
	}

	var next Cursor
	if len(all) > 0 {
		last := all[len(all)-1]
		next = Cursor{Time: last.Timestamp, ID: encodeSQLiteCursor(last.ID)}
	}
	return all, next, hasMore, nil
}

// Subscribe fan-in 所有子 backend 的实时流。
//
// 参数：
//   - ctx: 上下文，用于控制取消；ctx 取消时会触发所有子流的 Cancel
//   - opts: 订阅过滤、回放窗口和重连去重锚点
//
// 返回：
//   - LogStream，包含日志条目 channel 和 Cancel 函数
//
// 注意：
//   - fan-in goroutine 使用阻塞写，背压自然传导给消费方
//   - ctx 取消时本层也会立即响应，不依赖子流自己退出
//   - Cancel 与 ctx 取消均可触发子流停止，两者互为补充
//   - ctx 为 context.Background() 时，ctx watcher goroutine 会永久阻塞，这是已知可接受行为
func (f *FederatedBackend) Subscribe(ctx context.Context, opts SubscribeOptions) LogStream {
	streams := make([]LogStream, len(f.children))
	for i, child := range f.children {
		streams[i] = child.Subscribe(ctx, opts)
	}

	ch := make(chan model.LogEntry, 64)
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(done)
			for _, s := range streams {
				s.Cancel()
			}
		})
	}

	// ctx watcher：ctx 取消或 cancel 调用时均可退出，避免 goroutine 泄漏
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		cancel()
	}()

	// fan-in：每个子流一个 goroutine，阻塞写入统一的 ch
	// 使用阻塞写而非 select default，让背压自然传导给消费方，避免静默丢数据
	var wg sync.WaitGroup
	for _, s := range streams {
		wg.Add(1)
		go func(sub LogStream) {
			defer wg.Done()
			for entry := range sub.Ch {
				select {
				case ch <- entry:
				case <-ctx.Done():
					return
				}
			}
		}(s)
	}
	// 所有子流关闭后关闭 ch
	go func() {
		wg.Wait()
		close(ch)
	}()

	return LogStream{Ch: ch, Cancel: cancel}
}

type federatedMergedItem struct {
	child int
	entry model.LogEntry
}

func mergeLatestFederatedResults(results []federatedQueryResult, limit int) []federatedMergedItem {
	all := []federatedMergedItem{}
	for _, result := range results {
		for _, entry := range result.entries {
			all = append(all, federatedMergedItem{child: result.child, entry: entry})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		return lessLogEntry(all[i].entry, all[j].entry)
	})
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all
}

func nextFederatedCursorStates(
	results []federatedQueryResult,
	oldStates map[int]federatedCursorState,
	legacyBefore Cursor,
	hasFederatedCursor bool,
	emittedCount map[int]int,
	oldestEmitted map[int]model.LogEntry,
	limit int,
) map[int]federatedCursorState {
	next := map[int]federatedCursorState{}
	for _, result := range results {
		state := oldStates[result.child]
		if !hasFederatedCursor {
			state.Cursor = legacyBefore
		}
		if state.Exhausted {
			next[result.child] = state
			continue
		}

		count := emittedCount[result.child]
		switch {
		case count == 0 && len(result.entries) == 0:
			next[result.child] = federatedCursorState{Exhausted: true}
		case count == 0:
			next[result.child] = state
		case count == len(result.entries) && len(result.entries) < limit:
			next[result.child] = federatedCursorState{Exhausted: true}
		default:
			oldest := oldestEmitted[result.child]
			next[result.child] = federatedCursorState{Cursor: Cursor{Time: oldest.Timestamp, ID: encodeSQLiteCursor(oldest.ID)}}
		}
	}
	return next
}

func encodeFederatedCursor(states map[int]federatedCursorState) string {
	wire := map[string]federatedCursorWire{}
	for child, state := range states {
		if state.Exhausted || state.Cursor.ID != "" || !state.Cursor.Time.IsZero() {
			item := federatedCursorWire{ID: state.Cursor.ID, Exhausted: state.Exhausted}
			if !state.Cursor.Time.IsZero() {
				item.Time = state.Cursor.Time.Format(time.RFC3339Nano)
			}
			wire[strconv.Itoa(child)] = item
		}
	}
	if len(wire) == 0 {
		return ""
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return ""
	}
	return federatedCursorPrefix + base64.RawURLEncoding.EncodeToString(data)
}

func decodeFederatedCursor(id string) (map[int]federatedCursorState, bool) {
	if !strings.HasPrefix(id, federatedCursorPrefix) {
		return map[int]federatedCursorState{}, false
	}
	raw := strings.TrimPrefix(id, federatedCursorPrefix)
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return map[int]federatedCursorState{}, false
	}
	wire := map[string]federatedCursorWire{}
	if err := json.Unmarshal(data, &wire); err != nil {
		return map[int]federatedCursorState{}, false
	}
	out := map[int]federatedCursorState{}
	for rawChild, item := range wire {
		child, err := strconv.Atoi(rawChild)
		if err != nil {
			continue
		}
		var cursorTime time.Time
		if item.Time != "" {
			parsed, err := time.Parse(time.RFC3339Nano, item.Time)
			if err != nil {
				continue
			}
			cursorTime = parsed
		}
		out[child] = federatedCursorState{
			Cursor:    Cursor{Time: cursorTime, ID: item.ID},
			Exhausted: item.Exhausted,
		}
	}
	return out, true
}

// lessLogEntry 按 timestamp ASC 比较，时间相同时按游标 ID 的字符串字典序兜底。
// 字符串序兜底让数值 ID（sqlite/remote）与非数值 ID（云后端）能在同一次归并里共存。
func lessLogEntry(a, b model.LogEntry) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return encodeSQLiteCursor(a.ID) < encodeSQLiteCursor(b.ID)
	}
	return a.Timestamp.Before(b.Timestamp)
}
