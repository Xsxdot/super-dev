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
	"container/heap"
	"context"
	"sort"
	"sync"

	"github.com/superdev/agent/model"
)

// FederatedBackend 聚合多个子 LogBackend，实现跨节点日志统一访问。
type FederatedBackend struct {
	children []LogBackend
}

// NewFederatedBackend 创建 FederatedBackend。
func NewFederatedBackend(children []LogBackend) *FederatedBackend {
	return &FederatedBackend{children: children}
}

// Query 并发拉取所有子 backend 的历史日志，k-way 归并后截取 limit。
func (f *FederatedBackend) Query(ctx context.Context, filter QueryFilter) ([]model.LogEntry, Cursor, error) {
	type result struct {
		entries []model.LogEntry
	}
	results := make([]result, len(f.children))
	var wg sync.WaitGroup
	for i, child := range f.children {
		wg.Add(1)
		go func(idx int, b LogBackend) {
			defer wg.Done()
			entries, _, _ := b.Query(ctx, filter)
			results[idx] = result{entries: entries}
		}(i, child)
	}
	wg.Wait()

	streams := make([][]model.LogEntry, 0, len(results))
	for _, r := range results {
		if len(r.entries) > 0 {
			streams = append(streams, r.entries)
		}
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = 1000
	}
	merged := mergeLogStreams(streams, limit)

	var next Cursor
	if len(merged) > 0 {
		last := merged[len(merged)-1]
		next = Cursor{Time: last.Timestamp, ID: last.ID}
	}
	return merged, next, nil
}

// Search 并发搜索所有子 backend，归并排序后截取 limit。
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
		limit = 1000
	}
	if len(all) > limit {
		all = all[:limit]
		hasMore = true
	}

	var next Cursor
	if len(all) > 0 {
		last := all[len(all)-1]
		next = Cursor{Time: last.Timestamp, ID: last.ID}
	}
	return all, next, hasMore, nil
}

// Subscribe fan-in 所有子 backend 的实时流。
// Cancel 时统一停止所有子流并关闭 Ch。
func (f *FederatedBackend) Subscribe(ctx context.Context, serviceID string) LogStream {
	streams := make([]LogStream, len(f.children))
	for i, child := range f.children {
		streams[i] = child.Subscribe(ctx, serviceID)
	}

	ch := make(chan model.LogEntry, 64)
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			for _, s := range streams {
				s.Cancel()
			}
		})
	}

	// fan-in：每个子流一个 goroutine，写入统一的 ch
	var wg sync.WaitGroup
	for _, s := range streams {
		wg.Add(1)
		go func(sub LogStream) {
			defer wg.Done()
			for entry := range sub.Ch {
				select {
				case ch <- entry:
				default:
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

// fedHeapItem 是 k-way 归并堆中的单个元素。
type fedHeapItem struct {
	entry  model.LogEntry
	slice  []model.LogEntry
	cursor int
}

type fedHeap []*fedHeapItem

func (h fedHeap) Len() int           { return len(h) }
func (h fedHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h fedHeap) Less(i, j int) bool { return lessLogEntry(h[i].entry, h[j].entry) }
func (h *fedHeap) Push(x any)        { *h = append(*h, x.(*fedHeapItem)) }
func (h *fedHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// mergeLogStreams 将多个已排序切片 k-way 归并为单一已排序切片，截取 limit 条。
func mergeLogStreams(streams [][]model.LogEntry, limit int) []model.LogEntry {
	h := &fedHeap{}
	heap.Init(h)
	for _, s := range streams {
		if len(s) > 0 {
			heap.Push(h, &fedHeapItem{entry: s[0], slice: s, cursor: 1})
		}
	}
	out := make([]model.LogEntry, 0, limit)
	for h.Len() > 0 && len(out) < limit {
		item := heap.Pop(h).(*fedHeapItem)
		out = append(out, item.entry)
		if item.cursor < len(item.slice) {
			heap.Push(h, &fedHeapItem{entry: item.slice[item.cursor], slice: item.slice, cursor: item.cursor + 1})
		}
	}
	return out
}

// lessLogEntry 按 timestamp ASC, id ASC 比较两条日志。
func lessLogEntry(a, b model.LogEntry) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.ID < b.ID
	}
	return a.Timestamp.Before(b.Timestamp)
}
