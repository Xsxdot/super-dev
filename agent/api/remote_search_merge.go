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

// MergeStreams 将多个已排序流归并为一个已排序流。
//
// 参数：
//   - streams: hostID 到该 host 本批日志的映射,每个切片需已按 timestamp ASC, id ASC 排序
//   - limit: 输出条数上限,小于等于 0 时返回 nil
//
// 返回：
//   - 按 timestamp ASC, id ASC 排序的 MergeItem 列表,长度不超过 limit
//
// 注意：
//   - 当前按每轮线性扫描取最小值实现,适合少量远端 Host 的交互式搜索场景
func MergeStreams(streams map[string][]model.LogEntry, limit int) []MergeItem {
	if limit <= 0 {
		return nil
	}

	cursors := make(map[string]int, len(streams))
	for hostID := range streams {
		cursors[hostID] = 0
	}

	out := make([]MergeItem, 0, limit)
	for len(out) < limit {
		var minHost string
		var minEntry model.LogEntry
		hasAny := false
		for hostID, idx := range cursors {
			if idx >= len(streams[hostID]) {
				continue
			}
			entry := streams[hostID][idx]
			if !hasAny || lessLogEntry(entry, minEntry) {
				hasAny = true
				minHost = hostID
				minEntry = entry
			}
		}
		if !hasAny {
			break
		}
		out = append(out, MergeItem{HostID: minHost, Entry: minEntry})
		cursors[minHost]++
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
