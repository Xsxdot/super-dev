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
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	return srv
}

// testServerHandler 包一层"未带 Authorization 时默认注入本机 token"，供真实
// httptest.Server 网络往返（http.Get/http.Post/自定义 *http.Client）的测试使用。
// 语义与 httptestDoWithHeader 的默认注入一致：真实 http.Client 请求没有"显式传
// 空值"这个概念（要么带头要么不带），所以这里退化为"没带就注入"，测试想验证
// 拒绝路径需自行在请求上设置一个非空但无效的 Authorization 覆盖默认值。
func testServerHandler(app *App) http.Handler {
	next := app.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
		}
		next.ServeHTTP(w, r)
	})
}

// httptestDoWithHeader 发起一次经 App.Handler 处理的测试请求。
//
// 鉴权语义（自 withSecurity 常开后生效）：
//   - headers 未显式包含 "Authorization" 键时，自动注入本机 token，
//     让绝大多数与鉴权无关的用例无需逐个手写凭据。
//   - 想模拟裸请求（无凭据）时，显式传 headers["Authorization"] = ""——
//     该值会被删除而不是当成一个空 Bearer 发送，从而真正复现"未带凭据"。
//   - 想用坏凭据/自定义凭据时，直接传非空 "Authorization" 值覆盖默认注入。
func httptestDoWithHeader(t *testing.T, app *App, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	// 鉴权常开后所有受保护端点都需要凭据：默认注入本机 token，
	// 测试想模拟裸请求/坏凭据时显式传 "Authorization": ""（删除头）或伪值覆盖。
	if v, explicit := headers["Authorization"]; explicit {
		if v == "" {
			req.Header.Del("Authorization")
		}
	} else if req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	}
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	return rr
}

func testTunnelHost(id, name, sshHost, user string) model.Host {
	return model.Host{
		ID:                    id,
		Name:                  name,
		Tags:                  []string{},
		SSHHost:               sshHost,
		SSHPort:               model.DefaultSSHPort,
		SSHUser:               user,
		SSHHostKeyFingerprint: "SHA256:NeZJ8Xqm8k2RJoaxC7XMjjoXdw5R8TNigSr9hkWjK7A",
	}
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
