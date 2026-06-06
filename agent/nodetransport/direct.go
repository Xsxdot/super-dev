// direct.go 实现按地址直连远端 agent 的 NodeTransport。
//
// 职责：
//   - 使用 Host.Agent.Transport.Direct.Address 发起 HTTP 和 WebSocket 请求
//   - 为 NodeRegistry 订阅直连 agent 的 /ws/node-status
//   - 将旧 agent 缺少状态流接口的情况归类为 version-mismatch
//
// 边界：
//   - 不建立 SSH 隧道
//   - 不安装或升级远端 agent
//   - 不持久化节点状态
package nodetransport

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/model"
)

const defaultDirectStatusReconnectInterval = 5 * time.Second

// DirectTransport 通过直接可达地址访问远端 agent。
type DirectTransport struct {
	hosts                   HostSource
	client                  *http.Client
	wsDialer                *websocket.Dialer
	statusReconnectInterval time.Duration
}

// NewDirectTransport 创建直连传输实现。
func NewDirectTransport(hosts HostSource) *DirectTransport {
	return &DirectTransport{
		hosts:                   hosts,
		client:                  http.DefaultClient,
		wsDialer:                websocket.DefaultDialer,
		statusReconnectInterval: defaultDirectStatusReconnectInterval,
	}
}

// SetStatusReconnectIntervalForTest 缩短状态流重连间隔，供单元测试使用。
func (t *DirectTransport) SetStatusReconnectIntervalForTest(interval time.Duration) {
	t.statusReconnectInterval = interval
}

// Do 对 hostID 发起一次 HTTP 请求。
func (t *DirectTransport) Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error) {
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
		return NodeResponse{}, &NodeError{
			Code:          CodeTransportUnreachable,
			HostID:        hostID,
			TransportType: model.TransportTypeDirect,
			Operation:     "http",
			Message:       err.Error(),
			Cause:         err,
		}
	}
	return NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

// Stream 对 hostID 建立 WebSocket 流。
func (t *DirectTransport) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	u, err := t.urlFor(hostID, req, true)
	if err != nil {
		return nil, err
	}
	conn, resp, err := t.wsDialer.DialContext(ctx, u, req.Headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, &NodeError{
			Code:          CodeTransportUnreachable,
			HostID:        hostID,
			TransportType: model.TransportTypeDirect,
			Operation:     "stream",
			Message:       err.Error(),
			Cause:         err,
		}
	}
	return conn, nil
}

// SubscribeNodes 聚合本 transport 覆盖的所有 direct host 状态流。
func (t *DirectTransport) SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 32)
	go t.runNodeStatusSubscription(runCtx, out)
	return out, cancel
}

// Covers 返回当前 direct transport 覆盖的 hostID。
func (t *DirectTransport) Covers() []string {
	hosts := t.directHosts()
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		out = append(out, host.ID)
	}
	sort.Strings(out)
	return out
}

func (t *DirectTransport) runNodeStatusSubscription(ctx context.Context, out chan<- []NodeStatus) {
	interval := t.statusReconnectInterval
	if interval == 0 {
		interval = defaultDirectStatusReconnectInterval
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
		hosts := t.directHosts()
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

func (t *DirectTransport) watchNodeStatus(ctx context.Context, host model.Host, out chan<- []NodeStatus) {
	stream, err := t.Stream(ctx, host.ID, NodeRequest{
		Path: "/ws/node-status",
		Query: url.Values{
			"host_id":   []string{host.ID},
			"host_name": []string{host.Name},
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			t.emitDirectFailure(ctx, out, host, err)
		}
		return
	}
	defer stream.Close()

	for {
		var batch []NodeStatus
		if err := stream.ReadJSON(&batch); err != nil {
			if ctx.Err() == nil {
				t.emitDirectFailure(ctx, out, host, err)
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

func (t *DirectTransport) emitDirectFailure(ctx context.Context, out chan<- []NodeStatus, host model.Host, err error) {
	health := model.AgentHealthUnreachable
	reachable := false
	if t.execHealthReachable(ctx, host.ID) {
		health = model.AgentHealthVersionMismatch
		reachable = true
	}
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

func (t *DirectTransport) execHealthReachable(ctx context.Context, hostID string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	resp, err := t.Do(probeCtx, hostID, NodeRequest{Method: http.MethodGet, Path: "/api/exec/health"})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent
}

func (t *DirectTransport) urlFor(hostID string, req NodeRequest, stream bool) (string, error) {
	host, params, err := t.directParamsFor(hostID)
	if err != nil {
		return "", err
	}
	base, err := directBaseURL(params)
	if err != nil {
		return "", &NodeError{
			Code:          CodeAgentNotConfigured,
			HostID:        host.ID,
			TransportType: model.TransportTypeDirect,
			Operation:     "resolve",
			Message:       err.Error(),
			Cause:         ErrHostUnreachable,
		}
	}
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

func (t *DirectTransport) directParamsFor(hostID string) (model.Host, *model.DirectParams, error) {
	hosts := t.directHosts()
	for _, host := range hosts {
		if host.ID != hostID {
			continue
		}
		return host, host.Agent.Transport.Direct, nil
	}
	return model.Host{}, nil, &NodeError{
		Code:          CodeAgentNotConfigured,
		HostID:        hostID,
		TransportType: model.TransportTypeDirect,
		Operation:     "resolve",
		Message:       fmt.Sprintf("direct agent not configured for host %s", hostID),
		Cause:         ErrHostUnreachable,
	}
}

func (t *DirectTransport) directHosts() []model.Host {
	if t.hosts == nil {
		return []model.Host{}
	}
	hosts, err := t.hosts()
	if err != nil {
		return []model.Host{}
	}
	out := make([]model.Host, 0, len(hosts))
	for _, host := range hosts {
		if host.Agent == nil {
			continue
		}
		if host.Agent.Transport.Type != model.TransportTypeDirect {
			continue
		}
		if host.Agent.Transport.Direct == nil {
			continue
		}
		out = append(out, host)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func directBaseURL(params *model.DirectParams) (*url.URL, error) {
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return nil, fmt.Errorf("direct address is required")
	}
	if !strings.Contains(address, "://") {
		scheme := "http"
		if params.TLS {
			scheme = "https"
		}
		address = scheme + "://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("direct address must include a host")
	}
	return u, nil
}
