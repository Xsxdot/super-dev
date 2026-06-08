// direct.go 实现按地址直连远端 agent 的 NodeTransport。
//
// 职责：
//   - 使用 Agent.Transport.Direct.Address 发起 HTTP 和 WebSocket 请求
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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	defaultDirectStatusReconnectInterval = 5 * time.Second
	defaultDirectConnectTimeout          = 3 * time.Second
	defaultDirectRequestTimeout          = 30 * time.Second
)

// DirectTransport 通过直接可达地址访问远端 agent。
type DirectTransport struct {
	targets                 TargetSource
	statusReconnectInterval time.Duration
	connectTimeout          time.Duration
	requestTimeout          time.Duration
}

// NewDirectTransport 创建直连传输实现。
func NewDirectTransport(targets TargetSource) *DirectTransport {
	return &DirectTransport{
		targets:                 targets,
		statusReconnectInterval: defaultDirectStatusReconnectInterval,
		connectTimeout:          defaultDirectConnectTimeout,
		requestTimeout:          defaultDirectRequestTimeout,
	}
}

// SetStatusReconnectIntervalForTest 缩短状态流重连间隔，供单元测试使用。
func (t *DirectTransport) SetStatusReconnectIntervalForTest(interval time.Duration) {
	t.statusReconnectInterval = interval
}

// SetTimeoutsForTest 缩短 direct 网络超时，供单元测试验证失败路径。
func (t *DirectTransport) SetTimeoutsForTest(connectTimeout, requestTimeout time.Duration) {
	t.connectTimeout = connectTimeout
	t.requestTimeout = requestTimeout
}

// Do 对 hostID 发起一次 HTTP 请求。
func (t *DirectTransport) Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error) {
	target, params, err := t.directParamsFor(hostID)
	if err != nil {
		return NodeResponse{}, err
	}
	tlsSpec := tlsSpecForRequest(target.Agent, req)
	u, err := t.urlForParams(target.Host.ID, tlsSpec, params, req, false)
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
	applyAgentHeaders(httpReq.Header, target.Agent, req.Headers)
	client, err := t.httpClientFor(tlsSpec)
	if err != nil {
		return NodeResponse{}, directConfigError(hostID, "http", err)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		code := CodeTransportUnreachable
		if isTimeoutError(ctx, err) {
			code = CodeRequestTimeout
		}
		return NodeResponse{}, &NodeError{
			Code:          code,
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
	target, params, err := t.directParamsFor(hostID)
	if err != nil {
		return nil, err
	}
	tlsSpec := tlsSpecForRequest(target.Agent, req)
	u, err := t.urlForParams(target.Host.ID, tlsSpec, params, req, true)
	if err != nil {
		return nil, err
	}
	dialer, err := t.wsDialerFor(tlsSpec)
	if err != nil {
		return nil, directConfigError(hostID, "stream", err)
	}
	headers := http.Header{}
	applyAgentHeaders(headers, target.Agent, req.Headers)
	conn, resp, err := dialer.DialContext(ctx, u, headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		code := CodeTransportUnreachable
		if isTimeoutError(ctx, err) {
			code = CodeRequestTimeout
		}
		return nil, &NodeError{
			Code:          code,
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

// SubscribeHostNodes 订阅单个 direct target 的状态流，供 Dispatcher 选路后调用。
func (t *DirectTransport) SubscribeHostNodes(ctx context.Context, target NodeTarget) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 16)
	go func() {
		defer close(out)
		t.watchNodeStatus(runCtx, target, out)
	}()
	return out, cancel
}

// Covers 返回当前 direct transport 覆盖的 hostID。
func (t *DirectTransport) Covers() []string {
	targets := t.directTargets()
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Host.ID)
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

	startWatcher := func(target NodeTarget) {
		ch, stop := t.SubscribeHostNodes(ctx, target)
		running[target.Host.ID] = stop
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				stop()
				select {
				case done <- target.Host.ID:
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
		targets := t.directTargets()
		seen := map[string]struct{}{}
		for _, target := range targets {
			if target.Host.ID == "" {
				continue
			}
			seen[target.Host.ID] = struct{}{}
			if _, ok := running[target.Host.ID]; ok {
				continue
			}
			startWatcher(target)
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

func (t *DirectTransport) watchNodeStatus(ctx context.Context, target NodeTarget, out chan<- []NodeStatus) {
	stream, err := t.Stream(ctx, target.Host.ID, NodeRequest{
		Path: "/ws/node-status",
		Query: url.Values{
			"host_id":   []string{target.Host.ID},
			"host_name": []string{target.Host.Name},
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			t.emitDirectFailure(ctx, out, target, err)
		}
		return
	}
	defer stream.Close()

	for {
		var batch []NodeStatus
		if err := stream.ReadJSON(&batch); err != nil {
			if ctx.Err() == nil {
				t.emitDirectFailure(ctx, out, target, err)
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

func (t *DirectTransport) emitDirectFailure(ctx context.Context, out chan<- []NodeStatus, target NodeTarget, err error) {
	health := model.AgentHealthUnreachable
	reachable := false
	if t.execHealthReachable(ctx, target.Host.ID) {
		health = model.AgentHealthVersionMismatch
		reachable = true
	}
	status := NodeStatus{
		HostID:    target.Host.ID,
		Name:      target.Host.Name,
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
	target, params, err := t.directParamsFor(hostID)
	if err != nil {
		return "", err
	}
	return t.urlForParams(target.Host.ID, target.Agent.Security.TLS, params, req, stream)
}

func (t *DirectTransport) urlForParams(hostID string, tlsSpec model.AgentTLSSpec, params *model.DirectParams, req NodeRequest, stream bool) (string, error) {
	base, err := directBaseURL(params, tlsSpec)
	if err != nil {
		return "", &NodeError{
			Code:          CodeAgentNotConfigured,
			HostID:        hostID,
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

func (t *DirectTransport) httpClientFor(tlsSpec model.AgentTLSSpec) (*http.Client, error) {
	connectTimeout := t.connectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultDirectConnectTimeout
	}
	requestTimeout := t.requestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultDirectRequestTimeout
	}
	return httpClientForAgentTLS(tlsSpec, connectTimeout, requestTimeout)
}

func (t *DirectTransport) wsDialerFor(tlsSpec model.AgentTLSSpec) (*websocket.Dialer, error) {
	connectTimeout := t.connectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultDirectConnectTimeout
	}
	return wsDialerForAgentTLS(tlsSpec, connectTimeout)
}

func isTimeoutError(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &netErr) && netErr.Timeout()) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func directConfigError(hostID string, operation string, err error) error {
	return &NodeError{
		Code:          CodeAgentNotConfigured,
		HostID:        hostID,
		TransportType: model.TransportTypeDirect,
		Operation:     operation,
		Message:       err.Error(),
		Cause:         err,
	}
}

func (t *DirectTransport) directParamsFor(hostID string) (NodeTarget, *model.DirectParams, error) {
	targets := t.directTargets()
	for _, target := range targets {
		if target.Host.ID != hostID {
			continue
		}
		params, _ := target.Agent.DirectParams()
		return target, params, nil
	}
	return NodeTarget{}, nil, &NodeError{
		Code:          CodeAgentNotConfigured,
		HostID:        hostID,
		TransportType: model.TransportTypeDirect,
		Operation:     "resolve",
		Message:       fmt.Sprintf("direct agent not configured for host %s", hostID),
		Cause:         ErrHostUnreachable,
	}
}

func (t *DirectTransport) directTargets() []NodeTarget {
	if t.targets == nil {
		return []NodeTarget{}
	}
	targets, err := t.targets()
	if err != nil {
		return []NodeTarget{}
	}
	out := make([]NodeTarget, 0, len(targets))
	for _, target := range targets {
		if _, ok := target.Agent.DirectParams(); !ok {
			continue
		}
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Host.Name != out[j].Host.Name {
			return out[i].Host.Name < out[j].Host.Name
		}
		return out[i].Host.ID < out[j].Host.ID
	})
	return out
}

func directBaseURL(params *model.DirectParams, tlsSpec model.AgentTLSSpec) (*url.URL, error) {
	address := strings.TrimSpace(params.Address)
	if address == "" {
		return nil, fmt.Errorf("direct address is required")
	}
	if !strings.Contains(address, "://") {
		scheme := "http"
		if agentTLSEnabled(tlsSpec) {
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
