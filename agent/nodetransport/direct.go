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
	"crypto/tls"
	"crypto/x509"
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
	hosts                   HostSource
	statusReconnectInterval time.Duration
	connectTimeout          time.Duration
	requestTimeout          time.Duration
}

// NewDirectTransport 创建直连传输实现。
func NewDirectTransport(hosts HostSource) *DirectTransport {
	return &DirectTransport{
		hosts:                   hosts,
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
	host, params, err := t.directParamsFor(hostID)
	if err != nil {
		return NodeResponse{}, err
	}
	u, err := t.urlForParams(host.ID, params, req, false)
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
	applyDirectHeaders(httpReq.Header, host, req.Headers)
	client, err := t.httpClientFor(params)
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
	host, params, err := t.directParamsFor(hostID)
	if err != nil {
		return nil, err
	}
	u, err := t.urlForParams(host.ID, params, req, true)
	if err != nil {
		return nil, err
	}
	dialer, err := t.wsDialerFor(params)
	if err != nil {
		return nil, directConfigError(hostID, "stream", err)
	}
	headers := http.Header{}
	applyDirectHeaders(headers, host, req.Headers)
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

// SubscribeHostNodes 订阅单个 direct host 的状态流，供 Dispatcher 选路后调用。
func (t *DirectTransport) SubscribeHostNodes(ctx context.Context, host model.Host) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 16)
	go func() {
		defer close(out)
		t.watchNodeStatus(runCtx, host, out)
	}()
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
	return t.urlForParams(host.ID, params, req, stream)
}

func (t *DirectTransport) urlForParams(hostID string, params *model.DirectParams, req NodeRequest, stream bool) (string, error) {
	base, err := directBaseURL(params)
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

func (t *DirectTransport) httpClientFor(params *model.DirectParams) (*http.Client, error) {
	tlsConfig, err := tlsConfigForDirect(params)
	if err != nil {
		return nil, err
	}
	connectTimeout := t.connectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultDirectConnectTimeout
	}
	requestTimeout := t.requestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultDirectRequestTimeout
	}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   connectTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: connectTimeout,
			TLSClientConfig:     tlsConfig,
		},
	}, nil
}

func (t *DirectTransport) wsDialerFor(params *model.DirectParams) (*websocket.Dialer, error) {
	tlsConfig, err := tlsConfigForDirect(params)
	if err != nil {
		return nil, err
	}
	connectTimeout := t.connectTimeout
	if connectTimeout == 0 {
		connectTimeout = defaultDirectConnectTimeout
	}
	return &websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: connectTimeout,
		TLSClientConfig:  tlsConfig,
		NetDialContext: (&net.Dialer{
			Timeout:   connectTimeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}, nil
}

func tlsConfigForDirect(params *model.DirectParams) (*tls.Config, error) {
	if params == nil || !params.TLS {
		return nil, nil
	}
	if strings.TrimSpace(params.CACert) == "" {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(params.CACert)) {
		return nil, fmt.Errorf("invalid direct CA certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, nil
}

func applyDirectHeaders(dst http.Header, host model.Host, overrides http.Header) {
	if host.Agent != nil && strings.TrimSpace(host.Agent.Token) != "" {
		dst.Set("Authorization", "Bearer "+strings.TrimSpace(host.Agent.Token))
	}
	for key, values := range overrides {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
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

func (t *DirectTransport) directParamsFor(hostID string) (model.Host, *model.DirectParams, error) {
	hosts := t.directHosts()
	for _, host := range hosts {
		if host.ID != hostID {
			continue
		}
		params, _ := host.DirectParams()
		return host, params, nil
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
		if _, ok := host.DirectParams(); !ok {
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
