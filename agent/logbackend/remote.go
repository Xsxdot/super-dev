// remote.go 实现通过 SSH 隧道读取远端 agent 日志的 LogBackend。
//
// 职责：
//   - Query：调远端 GET /api/logs，转换为 LogBackend.Query 语义
//   - Search：调远端 GET /api/log-search，转换为 LogBackend.Search 语义
//   - Subscribe：连接远端 GET /ws/logs WebSocket，转发实时日志
//
// 边界：
//   - 通过 TunnelResolver 获取 baseURL，不直接管理隧道生命周期
//   - 单次请求 3 秒超时；WebSocket 断开不自动重连（重连由上层负责）
package logbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/superdev/agent/model"
)

const remoteRequestTimeout = 3 * time.Second

// TunnelResolver 返回指定 hostID 的本地隧道 HTTP baseURL。
type TunnelResolver interface {
	BaseURL(hostID string) (string, error)
}

// RemoteAgentBackend 通过隧道读取远端 agent 的日志。
type RemoteAgentBackend struct {
	hostID       string
	deploymentID string // 远端 collector 的虚拟 deploymentID（collector.CollectorID）
	resolver     TunnelResolver
	wsURLFn      func() (string, error) // 仅用于测试注入；正常情况下从 resolver 派生
}

// NewRemoteAgentBackend 创建 RemoteAgentBackend。
//
// deploymentID 是远端 collector 对应的虚拟 deploymentID（由 collector.CollectorID 生成）。
func NewRemoteAgentBackend(hostID, deploymentID string, resolver TunnelResolver) *RemoteAgentBackend {
	return &RemoteAgentBackend{hostID: hostID, deploymentID: deploymentID, resolver: resolver}
}

// NewRemoteAgentBackendWithWSURL 创建 RemoteAgentBackend，允许测试时注入 ws URL（绕过 http→ws 转换）。
func NewRemoteAgentBackendWithWSURL(hostID, deploymentID string, resolver TunnelResolver, wsURL string) *RemoteAgentBackend {
	b := NewRemoteAgentBackend(hostID, deploymentID, resolver)
	b.wsURLFn = func() (string, error) { return wsURL + "/ws/logs?deployment=" + url.QueryEscape(deploymentID), nil }
	return b
}

// Query 从远端 /api/logs 拉取历史日志。
func (b *RemoteAgentBackend) Query(ctx context.Context, f QueryFilter) ([]model.LogEntry, Cursor, error) {
	base, err := b.resolver.BaseURL(b.hostID)
	if err != nil {
		return nil, Cursor{}, err
	}
	if base == "" {
		return nil, Cursor{}, fmt.Errorf("tunnel not connected for host %s", b.hostID)
	}

	u, err := url.Parse(base + "/api/logs")
	if err != nil {
		return nil, Cursor{}, err
	}
	q := u.Query()
	q.Set("deployment", b.deploymentID)
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	u.RawQuery = q.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, Cursor{}, err
	}
	resp, err := http.DefaultClient.Do(req)
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
		next = Cursor{Time: last.Timestamp, ID: last.ID}
	}
	return entries, next, nil
}

// Search 从远端 /api/log-search 搜索日志。
func (b *RemoteAgentBackend) Search(ctx context.Context, q SearchQuery) ([]model.LogEntry, Cursor, bool, error) {
	base, err := b.resolver.BaseURL(b.hostID)
	if err != nil {
		return nil, Cursor{}, false, err
	}
	if base == "" {
		return nil, Cursor{}, false, fmt.Errorf("tunnel not connected for host %s", b.hostID)
	}

	u, err := url.Parse(base + "/api/log-search")
	if err != nil {
		return nil, Cursor{}, false, err
	}
	params := u.Query()
	params.Set("deployment", b.deploymentID)
	params.Set("q", q.Text)
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
	if !q.Cursor.Time.IsZero() {
		params.Set("cursor_time", q.Cursor.Time.Format(time.RFC3339Nano))
		params.Set("cursor_id", strconv.FormatInt(q.Cursor.ID, 10))
	}
	if !q.From.IsZero() {
		params.Set("from", q.From.Format(time.RFC3339Nano))
	}
	if !q.To.IsZero() {
		params.Set("to", q.To.Format(time.RFC3339Nano))
	}
	u.RawQuery = params.Encode()

	reqCtx, cancel := context.WithTimeout(ctx, remoteRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, Cursor{}, false, err
	}
	resp, err := http.DefaultClient.Do(req)
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
		next = Cursor{Time: last.Timestamp, ID: last.ID}
	}
	return payload.Items, next, payload.HasMore, nil
}

// Subscribe 连接远端 /ws/logs WebSocket，转发实时日志。
// ctx 取消和 Cancel 调用均可停止流并关闭 Ch；两者均幂等。
// 连接断开时自动关闭 Ch（不重连，由上层 FederatedBackend 决策）。
func (b *RemoteAgentBackend) Subscribe(ctx context.Context, deploymentID string) LogStream {
	ch := make(chan model.LogEntry, 64)

	wsURL, err := b.resolveWSURL()
	if err != nil {
		close(ch)
		return LogStream{Ch: ch, Cancel: func() {}}
	}

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		close(ch)
		return LogStream{Ch: ch, Cancel: func() {}}
	}

	done := make(chan struct{})
	var once sync.Once
	closeConn := func() {
		once.Do(func() {
			close(done)
			_ = conn.Close()
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
			if err := conn.ReadJSON(&entry); err != nil {
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

// resolveWSURL 获取 WebSocket 连接地址。
// 优先使用测试注入的 wsURLFn，否则从 resolver 派生。
func (b *RemoteAgentBackend) resolveWSURL() (string, error) {
	if b.wsURLFn != nil {
		return b.wsURLFn()
	}
	base, err := b.resolver.BaseURL(b.hostID)
	if err != nil {
		return "", err
	}
	if base == "" {
		return "", fmt.Errorf("tunnel not connected for host %s", b.hostID)
	}
	// 将 http(s):// 替换为 ws(s)://，避免裸字符串切片的 panic 风险
	var wsBase string
	switch {
	case strings.HasPrefix(base, "https://"):
		wsBase = "wss://" + base[len("https://"):]
	case strings.HasPrefix(base, "http://"):
		wsBase = "ws://" + base[len("http://"):]
	default:
		return "", fmt.Errorf("unsupported scheme in tunnel base URL: %s", base)
	}
	return wsBase + "/ws/logs?deployment=" + url.QueryEscape(b.deploymentID), nil
}
