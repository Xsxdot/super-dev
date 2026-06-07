// package_test_helpers_test.go 提供 api 包内部测试共享 helper。
//
// 职责：
//   - 创建 package api 测试用 App
//   - 创建绑定 App.Handler 的 httptest.Server
//
// 边界：
//   - 不提供 package api_test 的黑盒测试 helper
//   - 不注入业务数据，调用方自行准备项目、host、日志等状态
package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func newTestAppForPackage(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
}

func newHTTPServerForPackage(t *testing.T, app *App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func httptestDoWithHeader(t *testing.T, app *App, method, path string, body io.Reader, headers http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	return rr
}

func testTunnelHost(id, name, sshHost, user string) model.Host {
	return model.Host{ID: id, Name: name, Tags: []string{}, SSHHost: sshHost, SSHPort: model.DefaultSSHPort, SSHUser: user}
}

type testNodeTransport struct {
	table map[string]string
	err   error
}

func (t testNodeTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if t.err != nil {
		return nodetransport.NodeResponse{}, t.err
	}
	base, ok := t.table[hostID]
	if !ok {
		return nodetransport.NodeResponse{}, nodetransport.ErrHostUnreachable
	}
	u, err := url.Parse(base + req.Path)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	q := u.Query()
	for key, values := range req.Query {
		for _, value := range values {
			q.Add(key, value)
		}
	}
	u.RawQuery = q.Encode()
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, u.String(), req.Body)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	for key, values := range req.Headers {
		for _, value := range values {
			httpReq.Header.Add(key, value)
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	return nodetransport.NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

func (t testNodeTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t testNodeTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t testNodeTransport) Covers() []string {
	out := make([]string, 0, len(t.table))
	for hostID := range t.table {
		out = append(out, hostID)
	}
	return out
}
