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

// TunnelTransport 通过已建立的 SSH 隧道访问远端 agent。
type TunnelTransport struct {
	mgr                     *tunnel.Manager
	targets                 TargetSource
	client                  *http.Client
	wsDialer                *websocket.Dialer
	statusReconnectInterval time.Duration
}

// NewTunnelTransport 创建 SSH 隧道传输实现。
func NewTunnelTransport(mgr *tunnel.Manager, targets TargetSource) *TunnelTransport {
	client, _ := httpClientForAgentTLS(model.AgentTLSSpec{Mode: model.AgentTLSModeOff}, defaultDirectConnectTimeout, defaultDirectRequestTimeout)
	dialer, _ := wsDialerForAgentTLS(model.AgentTLSSpec{Mode: model.AgentTLSModeOff}, defaultDirectConnectTimeout)
	return &TunnelTransport{
		mgr:                     mgr,
		targets:                 targets,
		client:                  client,
		wsDialer:                dialer,
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
	target, err := t.ensureTargetFor(hostID)
	if err != nil {
		return NodeResponse{}, err
	}
	tlsSpec := tlsSpecForRequest(target.Agent, req)
	u, err := t.urlForTarget(target, tlsSpec, req, false)
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
		t.markTunnelFailure(ctx, hostID, err)
		code := CodeTransportUnreachable
		if isTimeoutError(ctx, err) {
			code = CodeRequestTimeout
		}
		return NodeResponse{}, &NodeError{
			Code:          code,
			HostID:        hostID,
			TransportType: model.TransportTypeTunnel,
			Operation:     "http",
			Message:       err.Error(),
			Cause:         err,
		}
	}
	return NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

// Stream 对 hostID 建立 WebSocket 流。
func (t *TunnelTransport) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	target, err := t.ensureTargetFor(hostID)
	if err != nil {
		return nil, err
	}
	tlsSpec := tlsSpecForRequest(target.Agent, req)
	u, err := t.urlForTarget(target, tlsSpec, req, true)
	if err != nil {
		return nil, err
	}
	headers := http.Header{}
	applyAgentHeaders(headers, target.Agent, req.Headers)
	dialer, err := t.wsDialerFor(tlsSpec)
	if err != nil {
		return nil, directConfigError(hostID, "stream", err)
	}
	conn, resp, err := dialer.DialContext(ctx, u, headers)
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
		} else if statusCode == 0 {
			t.markTunnelFailure(ctx, hostID, err)
			if isTimeoutError(ctx, err) {
				code = CodeRequestTimeout
			}
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

// SubscribeHostNodes 订阅单个 tunnel target 的状态流，供 Dispatcher 选路后调用。
func (t *TunnelTransport) SubscribeHostNodes(ctx context.Context, target NodeTarget) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 16)
	go func() {
		defer close(out)
		t.watchNodeStatus(runCtx, target, out)
	}()
	return out, cancel
}

// Covers 返回当前 tunnel transport 覆盖的 hostID。
func (t *TunnelTransport) Covers() []string {
	targets := t.tunnelTargets()
	out := make([]string, 0, len(targets))
	for _, target := range targets {
		out = append(out, target.Host.ID)
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
		targets := t.tunnelTargets()
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

func (t *TunnelTransport) tunnelTargets() []NodeTarget {
	if t.targets == nil {
		return []NodeTarget{}
	}
	targets, err := t.targets()
	if err != nil {
		return []NodeTarget{}
	}
	out := make([]NodeTarget, 0, len(targets))
	for _, target := range targets {
		if _, ok := target.Agent.TunnelParams(); ok {
			out = append(out, target)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Host.Name != out[j].Host.Name {
			return out[i].Host.Name < out[j].Host.Name
		}
		return out[i].Host.ID < out[j].Host.ID
	})
	return out
}

func (t *TunnelTransport) watchNodeStatus(ctx context.Context, target NodeTarget, out chan<- []NodeStatus) {
	if t.mgr == nil {
		t.emitUnreachable(ctx, out, target, ErrHostUnreachable)
		return
	}
	if _, err := t.mgr.EnsureConnected(TunnelTargetFromNodeTarget(target)); err != nil {
		t.emitUnreachable(ctx, out, target, err)
		return
	}
	stream, err := t.Stream(ctx, target.Host.ID, NodeRequest{
		Path: "/ws/node-status",
		Query: url.Values{
			"host_id":   []string{target.Host.ID},
			"host_name": []string{target.Host.Name},
		},
	})
	if err != nil {
		if ctx.Err() == nil {
			t.emitNodeStatusFailure(ctx, out, target, err)
		}
		return
	}
	defer stream.Close()

	for {
		var batch []NodeStatus
		if err := stream.ReadJSON(&batch); err != nil {
			if ctx.Err() == nil {
				t.markTunnelFailure(ctx, target.Host.ID, err)
				t.emitUnreachable(ctx, out, target, err)
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

func (t *TunnelTransport) emitUnreachable(ctx context.Context, out chan<- []NodeStatus, target NodeTarget, err error) {
	t.emitStatus(ctx, out, target, model.AgentHealthUnreachable, false, err)
}

func (t *TunnelTransport) emitNodeStatusFailure(ctx context.Context, out chan<- []NodeStatus, target NodeTarget, err error) {
	if t.execHealthReachable(ctx, target.Host.ID) {
		t.emitStatus(ctx, out, target, model.AgentHealthVersionMismatch, true, err)
		return
	}
	t.emitUnreachable(ctx, out, target, err)
}

func (t *TunnelTransport) emitStatus(ctx context.Context, out chan<- []NodeStatus, target NodeTarget, health model.AgentHealth, reachable bool, err error) {
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

func (t *TunnelTransport) markTunnelFailure(ctx context.Context, hostID string, err error) {
	if t.mgr == nil || ctx.Err() != nil {
		return
	}
	t.mgr.MarkFailed(hostID, err)
}

func (t *TunnelTransport) ensureTargetFor(hostID string) (NodeTarget, error) {
	if t.mgr == nil {
		return NodeTarget{}, ErrHostUnreachable
	}
	target, ok := t.targetByHostID(hostID)
	if !ok {
		return NodeTarget{}, ErrHostUnreachable
	}
	if t.mgr.LocalPort(hostID) == 0 {
		if _, err := t.mgr.EnsureConnected(TunnelTargetFromNodeTarget(target)); err != nil {
			return NodeTarget{}, &NodeError{
				Code:          CodeTransportUnreachable,
				HostID:        hostID,
				TransportType: model.TransportTypeTunnel,
				Operation:     "connect",
				Message:       err.Error(),
				Cause:         err,
			}
		}
	}
	return target, nil
}

func (t *TunnelTransport) targetByHostID(hostID string) (NodeTarget, bool) {
	for _, target := range t.tunnelTargets() {
		if target.Host.ID == hostID {
			return target, true
		}
	}
	return NodeTarget{}, false
}

func (t *TunnelTransport) urlForTarget(target NodeTarget, tlsSpec model.AgentTLSSpec, req NodeRequest, stream bool) (string, error) {
	if t.mgr == nil {
		return "", ErrHostUnreachable
	}
	hostID := target.Host.ID
	port := t.mgr.LocalPort(hostID)
	if port == 0 {
		return "", ErrHostUnreachable
	}
	scheme := "http"
	if agentTLSEnabled(tlsSpec) {
		scheme = "https"
	}
	base := &url.URL{Scheme: scheme, Host: "127.0.0.1:" + strconv.Itoa(port)}
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

func (t *TunnelTransport) httpClientFor(tlsSpec model.AgentTLSSpec) (*http.Client, error) {
	if !agentTLSEnabled(tlsSpec) {
		return t.client, nil
	}
	return httpClientForAgentTLS(tlsSpec, defaultDirectConnectTimeout, defaultDirectRequestTimeout)
}

func (t *TunnelTransport) wsDialerFor(tlsSpec model.AgentTLSSpec) (*websocket.Dialer, error) {
	if !agentTLSEnabled(tlsSpec) {
		return t.wsDialer, nil
	}
	return wsDialerForAgentTLS(tlsSpec, defaultDirectConnectTimeout)
}

// TunnelTargetFromNodeTarget 将 Host SSH 与 Agent tunnel 配置合成为 tunnel.Manager 目标。
//
// 参数：
//   - target: Host 持久化配置与 Agent transport/runtime 的组合目标
//
// 返回：
//   - 含认证秘密、可信 host-key pin、远端 Agent 端口和本地端口偏好的 tunnel 目标
//
// 注意：
//   - 本函数不提供默认 pin；旧 Host 缺 pin 时由 Manager fail closed
func TunnelTargetFromNodeTarget(target NodeTarget) tunnel.Target {
	params, _ := target.Agent.TunnelParams()
	remotePort := model.DefaultRemoteAgentPort
	if params != nil && params.RemoteAgentPort != 0 {
		remotePort = params.RemoteAgentPort
	}
	sshPort := target.Host.SSHPort
	if sshPort == 0 {
		sshPort = model.DefaultSSHPort
	}
	return tunnel.Target{
		HostID:                target.Host.ID,
		SSHHost:               target.Host.SSHHost,
		SSHPort:               sshPort,
		SSHUser:               target.Host.SSHUser,
		SSHPassword:           target.Host.SSHPassword,
		SSHPrivateKey:         target.Host.SSHPrivateKey,
		SSHHostKeyFingerprint: target.Host.SSHHostKeyFingerprint,
		RemoteAgentPort:       remotePort,
		LocalPort:             target.Agent.Runtime.LocalPort,
	}
}

func applyAgentHeaders(dst http.Header, agent model.Agent, overrides http.Header) {
	if strings.TrimSpace(agent.Secret.Token) != "" {
		dst.Set("Authorization", "Bearer "+strings.TrimSpace(agent.Secret.Token))
	}
	for key, values := range overrides {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
