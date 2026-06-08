// remote.go 实现通过节点传输读取远端 agent 日志的 LogBackend。
//
// 职责：
//   - Query：调远端 GET /api/logs，转换为 LogBackend.Query 语义
//   - Search：调远端 GET /api/log-search，转换为 LogBackend.Search 语义
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
	"strconv"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

const remoteRequestTimeout = 3 * time.Second

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
		last := entries[len(entries)-1]
		next = Cursor{Time: last.Timestamp, ID: encodeSQLiteCursor(last.ID)}
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
