// remote_search_merge.go 提供跨节点搜索的 k-way 归并算法和复合游标编码。
//
// 职责：
//   - MergeStreams:将多个已排序(timestamp ASC, id ASC)的流归并为单流
//   - MergeCursor:记录每个 Host 的游标进度,并编码为 base64(json)
//
// 边界：
//   - 不发起 HTTP 请求,调用方负责并发拉取
//   - 不读取 Store,输入是各 Host 已返回的本批日志切片
package api

import (
	"container/heap"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/superdev/agent/model"
)

// MergeItem 是跨节点归并输出的单元,附带来源 Host。
type MergeItem struct {
	HostID string         `json:"host_id"`
	Entry  model.LogEntry `json:"entry"`
}

// mergeHeapItem 是 min-heap 中的单个元素，持有来源信息和切片游标。
type mergeHeapItem struct {
	hostID string
	entry  model.LogEntry
	slice  []model.LogEntry
	cursor int // 下一个待读取的索引
}

// mergeHeap 实现 heap.Interface，按 timestamp ASC, id ASC 排序。
type mergeHeap []*mergeHeapItem

func (h mergeHeap) Len() int      { return len(h) }
func (h mergeHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h mergeHeap) Less(i, j int) bool {
	return lessLogEntry(h[i].entry, h[j].entry)
}
func (h *mergeHeap) Push(x any) { *h = append(*h, x.(*mergeHeapItem)) }
func (h *mergeHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// MergeStreams 将多个已排序流归并为一个已排序流。
//
// 参数：
//   - streams: hostID 到该 host 本批日志的映射,每个切片需已按 timestamp ASC, id ASC 排序
//   - limit: 输出条数上限,小于等于 0 时返回 nil
//
// 返回：
//   - 按 timestamp ASC, id ASC 排序的 MergeItem 列表,长度不超过 limit
func MergeStreams(streams map[string][]model.LogEntry, limit int) []MergeItem {
	if limit <= 0 {
		return nil
	}

	h := make(mergeHeap, 0, len(streams))
	for hostID, slice := range streams {
		if len(slice) == 0 {
			continue
		}
		h = append(h, &mergeHeapItem{
			hostID: hostID,
			entry:  slice[0],
			slice:  slice,
			cursor: 1,
		})
	}
	heap.Init(&h)

	out := make([]MergeItem, 0, limit)
	for h.Len() > 0 && len(out) < limit {
		item := heap.Pop(&h).(*mergeHeapItem)
		out = append(out, MergeItem{HostID: item.hostID, Entry: item.entry})
		if item.cursor < len(item.slice) {
			item.entry = item.slice[item.cursor]
			item.cursor++
			heap.Push(&h, item)
		}
	}
	return out
}

// lessLogEntry 定义跨 Host 日志归并时使用的排序规则:先按时间,再按数据库自增 ID。
func lessLogEntry(a, b model.LogEntry) bool {
	if a.Timestamp.Equal(b.Timestamp) {
		return a.ID < b.ID
	}
	return a.Timestamp.Before(b.Timestamp)
}

// HostCursor 表示单个 Host 在跨节点搜索中的游标进度。
type HostCursor struct {
	CursorTime time.Time `json:"cursor_time"`
	CursorID   int64     `json:"cursor_id"`
	Exhausted  bool      `json:"exhausted"`
}

// MergeCursor 表示跨节点搜索的复合游标。
type MergeCursor map[string]HostCursor

// Encode 将复合游标序列化为 base64(json) 字符串。
//
// 返回：
//   - 可直接放入 next_cursor 字段的字符串
func (c MergeCursor) Encode() string {
	data, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(data)
}

// DecodeMergeCursor 解析 next_cursor 字符串。
//
// 参数：
//   - s: Encode 生成的 base64(json) 字符串,空字符串表示首次查询
//
// 返回：
//   - 解析后的 MergeCursor
//   - base64 或 JSON 无法解析时返回错误
func DecodeMergeCursor(s string) (MergeCursor, error) {
	if s == "" {
		return MergeCursor{}, nil
	}
	data, err := base64.URLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var cursor MergeCursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, err
	}
	return cursor, nil
}
