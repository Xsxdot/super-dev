// remote.go 实现通过节点传输读取远端 agent 日志的 LogBackend。
//
// 职责：
//   - Query：调远端 GET /api/logs，转换为 LogBackend.Query 语义
//   - Search：调远端 GET /api/log-search，转换为 LogBackend.Search 语义
//   - Context：优先调远端 GET /api/logs/context，旧远端不支持时退回日志页裁剪
//   - Subscribe：连接远端 GET /ws/logs WebSocket，转发实时日志
//
// 边界：
//   - 通过 NodeTransport 访问远端 agent，不直接管理隧道生命周期
//   - 单次请求 3 秒超时；WebSocket 断开不自动重连（重连由上层负责）
package logbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

const (
	remoteRequestTimeout       = 3 * time.Second
	remoteContextFallbackLimit = 5000
)

// RemoteAgentBackend 通过节点传输读取远端 agent 的日志。
type RemoteAgentBackend struct {
	hostID       string
	deploymentID string // 远端 collector 的虚拟 deploymentID（collector.CollectorID）
	transport    nodetransport.NodeTransport
}

// NewRemoteAgentBackend 创建 RemoteAgentBackend。
//
// deploymentID 是远端 collector 对应的虚拟 deploymentID（由 collector.CollectorID 生成）。
func NewRemoteAgentBackend(hostID, deploymentID string, transport nodetransport.NodeTransport) *RemoteAgentBackend {
	return &RemoteAgentBackend{hostID: hostID, deploymentID: deploymentID, transport: transport}
}

// Query 从远端 /api/logs 拉取历史日志。
func (b *RemoteAgentBackend) Query(ctx context.Context, f QueryFilter) ([]model.LogEntry, Cursor, error) {
	q := url.Values{}
	q.Set("deployment", b.deploymentID)
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	if f.Before.ID != "" {
		q.Set("before", strconv.FormatInt(decodeSQLiteCursor(f.Before.ID), 10))
	}

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
		Query:  q,
	})
	if err != nil {
		return nil, Cursor{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, Cursor{}, fmt.Errorf("remote /api/logs returned %d", resp.StatusCode)
	}
	var entries []model.LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, Cursor{}, err
	}
	if entries == nil {
		entries = []model.LogEntry{}
	}
	var next Cursor
	if len(entries) > 0 {
		first := entries[0]
		next = Cursor{Time: first.Timestamp, ID: encodeSQLiteCursor(first.ID)}
	}
	return entries, next, nil
}

// Search 从远端 /api/log-search 搜索日志。
func (b *RemoteAgentBackend) Search(ctx context.Context, q SearchQuery) ([]model.LogEntry, Cursor, bool, error) {
	params := url.Values{}
	params.Set("deployment", b.deploymentID)
	params.Set("q", q.Text)
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if !q.Cursor.Time.IsZero() {
		params.Set("cursor_time", q.Cursor.Time.Format(time.RFC3339Nano))
		params.Set("cursor_id", strconv.FormatInt(decodeSQLiteCursor(q.Cursor.ID), 10))
	}
	if !q.From.IsZero() {
		params.Set("from", q.From.Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		params.Set("to", q.To.Format(time.RFC3339Nano))
	}

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/log-search",
		Query:  params,
	})
	if err != nil {
		return nil, Cursor{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, Cursor{}, false, fmt.Errorf("remote /api/log-search returned %d", resp.StatusCode)
	}
	var payload struct {
		Items   []model.LogEntry `json:"items"`
		Total   int              `json:"total"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, Cursor{}, false, err
	}
	if payload.Items == nil {
		payload.Items = []model.LogEntry{}
	}
	var next Cursor
	if len(payload.Items) > 0 {
		last := payload.Items[len(payload.Items)-1]
		next = Cursor{Time: last.Timestamp, ID: encodeSQLiteCursor(last.ID)}
	}
	return payload.Items, next, payload.HasMore, nil
}

// Context 从远端 /api/logs/context 拉取单 deployment 上下文。
func (b *RemoteAgentBackend) Context(ctx context.Context, q ContextQuery) (ContextResult, error) {
	params := url.Values{}
	params.Set("deployment", b.deploymentID)
	params.Set("id", strconv.FormatInt(q.TargetID, 10))
	if q.Before > 0 {
		params.Set("before_ms", strconv.FormatInt(q.Before.Milliseconds(), 10))
	}
	if q.After > 0 {
		params.Set("after_ms", strconv.FormatInt(q.After.Milliseconds(), 10))
	}

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs/context",
		Query:  params,
	})
	if err != nil {
		return ContextResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return b.contextFromLogPage(ctx, q)
	}
	if resp.StatusCode/100 != 2 {
		if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusMethodNotAllowed {
			return b.contextFromLogPage(ctx, q)
		}
		return ContextResult{}, fmt.Errorf("remote /api/logs/context returned %d", resp.StatusCode)
	}
	var payload struct {
		TargetID          int64                       `json:"target_id"`
		AnchorTime        time.Time                   `json:"anchor_time"`
		ItemsByDeployment map[string][]model.LogEntry `json:"items_by_deployment"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ContextResult{}, err
	}
	return ContextResult{
		TargetID:   payload.TargetID,
		AnchorTime: payload.AnchorTime,
		Items:      payload.ItemsByDeployment[b.deploymentID],
	}, nil
}

// ContextPage 从远端 /api/logs/context/page 继续读取单 deployment 上下文。
func (b *RemoteAgentBackend) ContextPage(ctx context.Context, q ContextPageQuery) (ContextPageResult, error) {
	params := url.Values{}
	params.Set("deployment", b.deploymentID)
	params.Set("direction", string(q.Direction))
	params.Set("cursor_time", q.Cursor.Time.Format(time.RFC3339Nano))
	params.Set("cursor_id", strconv.FormatInt(decodeSQLiteCursor(q.Cursor.ID), 10))
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs/context/page",
		Query:  params,
	})
	if err != nil {
		return ContextPageResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusMethodNotAllowed {
		return b.contextPageFromLogPage(ctx, q)
	}
	if resp.StatusCode/100 != 2 {
		return ContextPageResult{}, fmt.Errorf("remote /api/logs/context/page returned %d", resp.StatusCode)
	}
	var payload struct {
		DeploymentID string               `json:"deployment_id"`
		Direction    ContextPageDirection `json:"direction"`
		Items        []model.LogEntry     `json:"items"`
		HasMore      bool                 `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ContextPageResult{}, err
	}
	if payload.Items == nil {
		payload.Items = []model.LogEntry{}
	}
	return ContextPageResult{Entries: payload.Items, HasMore: payload.HasMore}, nil
}

func (b *RemoteAgentBackend) contextFromLogPage(ctx context.Context, q ContextQuery) (ContextResult, error) {
	params := url.Values{}
	params.Set("deployment", b.deploymentID)
	params.Set("limit", strconv.Itoa(remoteContextFallbackLimit))
	// 旧远端没有 context API，只支持 before 游标；向 targetID 后方多取一段，
	// 让同一时间窗口内 target 之后的日志也有机会进入裁剪集合。
	params.Set("before", strconv.FormatInt(q.TargetID+remoteContextFallbackLimit, 10))

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
		Query:  params,
	})
	if err != nil {
		return ContextResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ContextResult{}, fmt.Errorf("remote /api/logs fallback returned %d", resp.StatusCode)
	}
	var entries []model.LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return ContextResult{}, err
	}

	targetIndex := -1
	for i, entry := range entries {
		if entry.ID == q.TargetID {
			targetIndex = i
			break
		}
	}
	if targetIndex < 0 {
		return ContextResult{}, ErrLogContextNotFound
	}
	anchor := entries[targetIndex].Timestamp
	lower := anchor.Add(-q.Before)
	upper := anchor.Add(q.After)
	items := make([]model.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Timestamp.Before(lower) || entry.Timestamp.After(upper) {
			continue
		}
		items = append(items, entry)
	}
	return ContextResult{
		TargetID:   q.TargetID,
		AnchorTime: anchor,
		Items:      items,
	}, nil
}

func (b *RemoteAgentBackend) contextPageFromLogPage(ctx context.Context, q ContextPageQuery) (ContextPageResult, error) {
	result := ContextPageResult{Entries: []model.LogEntry{}}
	if q.Cursor.Time.IsZero() || q.Cursor.ID == "" {
		return result, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	cursorID := decodeSQLiteCursor(q.Cursor.ID)
	if cursorID <= 0 {
		return result, nil
	}

	params := url.Values{}
	params.Set("deployment", b.deploymentID)
	params.Set("limit", strconv.Itoa(remoteContextFallbackLimit))
	beforeID := cursorID
	if q.Direction == ContextPageAfter {
		// 旧远端只有 before 游标；向后读时扩大 before 上界，再按时间/ID 裁出 cursor 之后的行。
		beforeID = cursorID + remoteContextFallbackLimit
	} else if q.Direction != ContextPageBefore {
		return result, fmt.Errorf("invalid context page direction: %s", q.Direction)
	}
	params.Set("before", strconv.FormatInt(beforeID, 10))

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	resp, err := b.transport.Do(reqCtx, b.hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/logs",
		Query:  params,
	})
	if err != nil {
		return ContextPageResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return ContextPageResult{}, fmt.Errorf("remote /api/logs page fallback returned %d", resp.StatusCode)
	}
	var entries []model.LogEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return ContextPageResult{}, err
	}
	filtered := make([]model.LogEntry, 0, len(entries))
	for _, entry := range entries {
		cmp := compareLogCursor(entry, q.Cursor.Time, cursorID)
		if q.Direction == ContextPageBefore && cmp < 0 {
			filtered = append(filtered, entry)
		}
		if q.Direction == ContextPageAfter && cmp > 0 {
			filtered = append(filtered, entry)
		}
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Timestamp.Equal(filtered[j].Timestamp) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].Timestamp.Before(filtered[j].Timestamp)
	})
	if q.Direction == ContextPageBefore && len(filtered) > limit+1 {
		filtered = filtered[len(filtered)-(limit+1):]
	}
	if len(filtered) > limit {
		result.HasMore = true
		if q.Direction == ContextPageBefore {
			filtered = filtered[len(filtered)-limit:]
		} else {
			filtered = filtered[:limit]
		}
	}
	result.Entries = filtered
	return result, nil
}

func compareLogCursor(entry model.LogEntry, cursorTime time.Time, cursorID int64) int {
	if entry.Timestamp.Before(cursorTime) {
		return -1
	}
	if entry.Timestamp.After(cursorTime) {
		return 1
	}
	if entry.ID < cursorID {
		return -1
	}
	if entry.ID > cursorID {
		return 1
	}
	return 0
}

// Subscribe 连接远端 /ws/logs WebSocket，转发实时日志。
// ctx 取消和 Cancel 调用均可停止流并关闭 Ch；两者均幂等。
// 连接断开时自动关闭 Ch（不重连，由上层 FederatedBackend 决策）。
func (b *RemoteAgentBackend) Subscribe(ctx context.Context, opts SubscribeOptions) LogStream {
	ch := make(chan model.LogEntry, 64)
	q := url.Values{"deployment": []string{b.deploymentID}}
	if opts.ReplayLast > 0 {
		q.Set("replay", strconv.Itoa(opts.ReplayLast))
	}
	if !opts.Since.Time.IsZero() {
		q.Set("since_time", opts.Since.Time.Format(time.RFC3339Nano))
	}
	if opts.Since.ID != "" {
		q.Set("since_id", opts.Since.ID)
	}

	stream, err := b.transport.Stream(ctx, b.hostID, nodetransport.NodeRequest{
		Path:  "/ws/logs",
		Query: q,
	})
	if err != nil {
		close(ch)
		return LogStream{Ch: ch, Cancel: func() {}}
	}

	done := make(chan struct{})
	var once sync.Once
	closeConn := func() {
		once.Do(func() {
			close(done)
			_ = stream.Close()
		})
	}

	// ctx watcher：ctx 取消或 closeConn 调用时均可退出，避免 goroutine 泄漏
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		closeConn()
	}()

	go func() {
		defer close(ch)
		defer closeConn()
		for {
			var entry model.LogEntry
			if err := stream.ReadJSON(&entry); err != nil {
				return
			}
			select {
			case ch <- entry:
			default:
			}
		}
	}()

	return LogStream{Ch: ch, Cancel: closeConn}
}
