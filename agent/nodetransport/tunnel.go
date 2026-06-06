package nodetransport

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

// HostSource 返回当前已配置的远端 Host 列表。
type HostSource func() ([]model.Host, error)

// TunnelTransport 通过已建立的 SSH 隧道访问远端 agent。
type TunnelTransport struct {
	mgr      *tunnel.Manager
	hosts    HostSource
	client   *http.Client
	wsDialer *websocket.Dialer
}

// NewTunnelTransport 创建 SSH 隧道传输实现。
func NewTunnelTransport(mgr *tunnel.Manager, hosts HostSource) *TunnelTransport {
	return &TunnelTransport{
		mgr:      mgr,
		hosts:    hosts,
		client:   http.DefaultClient,
		wsDialer: websocket.DefaultDialer,
	}
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

// SubscribeNodes 本阶段只预留状态线入口，不启动任何后台状态连接。
func (t *TunnelTransport) SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func()) {
	ch := make(chan []NodeStatus)
	close(ch)
	return ch, func() {}
}

// Covers 返回当前 tunnel transport 覆盖的 hostID。
func (t *TunnelTransport) Covers() []string {
	if t.hosts == nil {
		return []string{}
	}
	hosts, err := t.hosts()
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		if _, ok := host.TunnelParams(); ok {
			out = append(out, host.ID)
		}
	}
	sort.Strings(out)
	return out
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
