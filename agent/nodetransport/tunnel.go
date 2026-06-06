package nodetransport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// HostSource 返回当前已配置的远端 Host 列表。
type HostSource func() ([]model.Host, error)

// TunnelTransport 通过已建立的 SSH 隧道访问远端 agent。
type TunnelTransport struct {
	mgr                     *tunnel.Manager
	hosts                   HostSource
	client                  *http.Client
	wsDialer                *websocket.Dialer
	statusReconnectInterval time.Duration
}

// NewTunnelTransport 创建 SSH 隧道传输实现。
func NewTunnelTransport(mgr *tunnel.Manager, hosts HostSource) *TunnelTransport {
	return &TunnelTransport{
		mgr:                     mgr,
		hosts:                   hosts,
		client:                  http.DefaultClient,
		wsDialer:                websocket.DefaultDialer,
		statusReconnectInterval: 5 * time.Second,
	}
}

// SetStatusReconnectIntervalForTest 缩短状态流重连间隔，供单元测试使用。
//
// 参数：
//   - interval: 状态流 watcher 退出后的重新探测间隔
//
// 注意：
//   - 生产代码不调用此方法
func (t *TunnelTransport) SetStatusReconnectIntervalForTest(interval time.Duration) {
	t.statusReconnectInterval = interval
}

// Do 对 hostID 发起一次 HTTP 请求。
func (t *TunnelTransport) Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error) {
	u, err := t.urlFor(hostID, req, false)
	if err != nil {
		return NodeResponse{}, err
	}
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, u, req.Body)
	if err != nil {
		return NodeResponse{}, err
	}
	for key, values := range req.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return NodeResponse{}, err
	}
	return NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

// Stream 对 hostID 建立 WebSocket 流。
func (t *TunnelTransport) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	u, err := t.urlFor(hostID, req, true)
	if err != nil {
		return nil, err
	}
	conn, resp, err := t.wsDialer.DialContext(ctx, u, req.Headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// SubscribeNodes 聚合本 transport 覆盖的所有 tunnel host 状态流。
func (t *TunnelTransport) SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 32)
	go t.runNodeStatusSubscription(runCtx, out)
	return out, cancel
}

// Covers 返回当前 tunnel transport 覆盖的 hostID。
func (t *TunnelTransport) Covers() []string {
	hosts := t.tunnelHosts()
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		out = append(out, host.ID)
	}
	sort.Strings(out)
	return out
}

func (t *TunnelTransport) runNodeStatusSubscription(ctx context.Context, out chan<- []NodeStatus) {
	interval := t.statusReconnectInterval
	if interval == 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var wg sync.WaitGroup
	done := make(chan string, 128)
	running := map[string]context.CancelFunc{}

	cancelAll := func() {
		for _, cancel := range running {
			cancel()
		}
	}
	defer func() {
		cancelAll()
		wg.Wait()
		close(out)
	}()

	startWatcher := func(host model.Host) {
		hostCtx, cancel := context.WithCancel(ctx)
		running[host.ID] = cancel
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				cancel()
				select {
				case done <- host.ID:
				default:
				}
			}()
			t.watchNodeStatus(hostCtx, host, out)
		}()
	}

	reconcile := func() {
		hosts := t.tunnelHosts()
		seen := map[string]struct{}{}
		for _, host := range hosts {
			if host.ID == "" {
				continue
			}
			seen[host.ID] = struct{}{}
			if _, ok := running[host.ID]; ok {
				continue
			}
			startWatcher(host)
		}
		for hostID, cancel := range running {
			if _, ok := seen[hostID]; ok {
				continue
			}
			cancel()
			delete(running, hostID)
		}
	}

	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case hostID := <-done:
			delete(running, hostID)
		case <-ticker.C:
			reconcile()
		}
	}
}

func (t *TunnelTransport) tunnelHosts() []model.Host {
	if t.hosts == nil {
		return []model.Host{}
	}
	hosts, err := t.hosts()
	if err != nil {
		return []model.Host{}
	}
	out := make([]model.Host, 0, len(hosts))
	for _, host := range hosts {
		if _, ok := host.TunnelParams(); ok {
			out = append(out, host)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func (t *TunnelTransport) watchNodeStatus(ctx context.Context, host model.Host, out chan<- []NodeStatus) {
	stream, err := t.Stream(ctx, host.ID, NodeRequest{
		Path: "/ws/node-status",
		Query: url.Values{
			"host_id":   []string{host.ID},
			"host_name": []string{host.Name},
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			t.emitUnreachable(ctx, out, host, err)
		}
		return
	}
	defer stream.Close()

	for {
		var batch []NodeStatus
		if err := stream.ReadJSON(&batch); err != nil {
			if ctx.Err() == nil {
				t.emitUnreachable(ctx, out, host, err)
			}
			return
		}
		if len(batch) == 0 {
			continue
		}
		select {
		case out <- batch:
		case <-ctx.Done():
			return
		}
	}
}

func (t *TunnelTransport) emitUnreachable(ctx context.Context, out chan<- []NodeStatus, host model.Host, err error) {
	status := NodeStatus{
		HostID:    host.ID,
		Name:      host.Name,
		Reachable: false,
		Agent: model.AgentRuntime{
			Health:    model.AgentHealthUnreachable,
			Reachable: false,
		},
		UpdatedAt: time.Now().UTC(),
		Error:     err.Error(),
	}
	select {
	case out <- []NodeStatus{status}:
	case <-ctx.Done():
	}
}

func (t *TunnelTransport) urlFor(hostID string, req NodeRequest, stream bool) (string, error) {
	if t.mgr == nil {
		return "", ErrHostUnreachable
	}
	port := t.mgr.LocalPort(hostID)
	if port == 0 {
		return "", ErrHostUnreachable
	}
	base := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	rel, err := url.Parse(req.Path)
	if err != nil {
		return "", err
	}
	if rel.Path == "" {
		return "", fmt.Errorf("node request path is required")
	}
	u := base.ResolveReference(rel)
	query := u.Query()
	for key, values := range req.Query {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	u.RawQuery = query.Encode()
	if stream {
		switch u.Scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		default:
			return "", fmt.Errorf("unsupported stream scheme: %s", u.Scheme)
		}
	}
	return strings.TrimRight(u.String(), "/"), nil
}
