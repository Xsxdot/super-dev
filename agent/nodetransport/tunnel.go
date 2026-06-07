package nodetransport

import (
	"context"
	"fmt"
	"io"
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
	u, err := t.ensureURLFor(hostID, req, false)
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
	t.applyTunnelHeaders(httpReq.Header, hostID, req.Headers)
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return NodeResponse{}, err
	}
	return NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

// Stream 对 hostID 建立 WebSocket 流。
func (t *TunnelTransport) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	u, err := t.ensureURLFor(hostID, req, true)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	t.applyTunnelHeaders(headers, hostID, req.Headers)
	conn, resp, err := t.wsDialer.DialContext(ctx, u, headers)
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		code := CodeTransportUnreachable
		if statusCode == http.StatusNotFound {
			code = CodeAgentAPIMissing
		}
		return nil, &NodeError{
			Code:          code,
			HostID:        hostID,
			TransportType: model.TransportTypeTunnel,
			Operation:     "stream",
			Message:       err.Error(),
			Cause:         err,
		}
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

// SubscribeHostNodes 订阅单个 tunnel host 的状态流，供 Dispatcher 选路后调用。
func (t *TunnelTransport) SubscribeHostNodes(ctx context.Context, host model.Host) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 16)
	go func() {
		defer close(out)
		t.watchNodeStatus(runCtx, host, out)
	}()
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
		ch, stop := t.SubscribeHostNodes(ctx, host)
		running[host.ID] = stop
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				stop()
				select {
				case done <- host.ID:
				default:
				}
			}()
			for {
				select {
				case <-ctx.Done():
					return
				case batch, ok := <-ch:
					if !ok {
						return
					}
					select {
					case out <- batch:
					case <-ctx.Done():
						return
					}
				}
			}
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
	if t.mgr == nil {
		t.emitUnreachable(ctx, out, host, ErrHostUnreachable)
		return
	}
	if _, err := t.mgr.EnsureConnected(host); err != nil {
		t.emitUnreachable(ctx, out, host, err)
		return
	}
	stream, err := t.Stream(ctx, host.ID, NodeRequest{
		Path: "/ws/node-status",
		Query: url.Values{
			"host_id":   []string{host.ID},
			"host_name": []string{host.Name},
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			t.emitNodeStatusFailure(ctx, out, host, err)
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
	t.emitStatus(ctx, out, host, model.AgentHealthUnreachable, false, err)
}

func (t *TunnelTransport) emitNodeStatusFailure(ctx context.Context, out chan<- []NodeStatus, host model.Host, err error) {
	if t.execHealthReachable(ctx, host.ID) {
		t.emitStatus(ctx, out, host, model.AgentHealthVersionMismatch, true, err)
		return
	}
	t.emitUnreachable(ctx, out, host, err)
}

func (t *TunnelTransport) emitStatus(ctx context.Context, out chan<- []NodeStatus, host model.Host, health model.AgentHealth, reachable bool, err error) {
	status := NodeStatus{
		HostID:    host.ID,
		Name:      host.Name,
		Reachable: reachable,
		Agent: model.AgentRuntime{
			Installed: reachable,
			Health:    health,
			Reachable: reachable,
		},
		UpdatedAt: time.Now().UTC(),
		Error:     err.Error(),
	}
	select {
	case out <- []NodeStatus{status}:
	case <-ctx.Done():
	}
}

func (t *TunnelTransport) execHealthReachable(ctx context.Context, hostID string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := t.Do(probeCtx, hostID, NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode/100 == 2
}

func (t *TunnelTransport) ensureURLFor(hostID string, req NodeRequest, stream bool) (string, error) {
	if t.mgr == nil {
		return "", ErrHostUnreachable
	}
	if t.mgr.LocalPort(hostID) == 0 {
		host, ok := t.hostByID(hostID)
		if !ok {
			return "", ErrHostUnreachable
		}
		if _, err := t.mgr.EnsureConnected(host); err != nil {
			return "", &NodeError{
				Code:          CodeTransportUnreachable,
				HostID:        hostID,
				TransportType: model.TransportTypeTunnel,
				Operation:     "connect",
				Message:       err.Error(),
				Cause:         err,
			}
		}
	}
	return t.urlFor(hostID, req, stream)
}

func (t *TunnelTransport) hostByID(hostID string) (model.Host, bool) {
	for _, host := range t.tunnelHosts() {
		if host.ID == hostID {
			return host, true
		}
	}
	return model.Host{}, false
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

func (t *TunnelTransport) applyTunnelHeaders(dst http.Header, hostID string, overrides http.Header) {
	if host, ok := t.hostByID(hostID); ok && host.Agent != nil && strings.TrimSpace(host.Agent.Token) != "" {
		dst.Set("Authorization", "Bearer "+strings.TrimSpace(host.Agent.Token))
	}
	for key, values := range overrides {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
