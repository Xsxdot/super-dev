// handler_exec_test.go 验证 agent 命令执行 WebSocket 接口。
//
// 职责：
//   - 验证 /ws/exec 能接收命令并流式返回 stdout/stderr
//   - 验证命令执行前会调用注入的 Authorizer
//   - 验证授权失败时返回 error 消息且不执行命令
//
// 边界：
//   - 不测试 pipeline 路由
//   - 不建立真实 SSH 隧道
package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/remoteexec"
)

type apiRecordingAuthorizer struct {
	mu       sync.Mutex
	commands []string
	err      error
}

func (a *apiRecordingAuthorizer) Authorize(ctx context.Context, command string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.commands = append(a.commands, command)
	return a.err
}

func (a *apiRecordingAuthorizer) Commands() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.commands...)
}

func TestWsExecStreamsOutputAndCallsAuthorizer(t *testing.T) {
	app := newTestAppForPackage(t)
	auth := &apiRecordingAuthorizer{}
	app.executionAuthorizer = auth
	conn := dialAppWebSocket(t, app, "/ws/exec")

	require.NoError(t, conn.WriteJSON(remoteexec.CommandRequest{
		Command: "printf 'hello\\n'; printf 'warn\\n' >&2",
	}))

	var got []remoteexec.Message
	for {
		var msg remoteexec.Message
		require.NoError(t, conn.ReadJSON(&msg))
		got = append(got, msg)
		if msg.Type == remoteexec.MessageExit || msg.Type == remoteexec.MessageError {
			break
		}
	}

	assert.Equal(t, []string{"printf 'hello\\n'; printf 'warn\\n' >&2"}, auth.Commands())
	assert.Contains(t, got, remoteexec.Message{Type: remoteexec.MessageOutput, Stream: "stdout", Line: "hello"})
	assert.Contains(t, got, remoteexec.Message{Type: remoteexec.MessageOutput, Stream: "stderr", Line: "warn"})
	assert.Contains(t, got, remoteexec.Message{Type: remoteexec.MessageExit, ExitCode: 0})
}

func TestWsExecReturnsErrorWhenAuthorizerRejects(t *testing.T) {
	app := newTestAppForPackage(t)
	app.executionAuthorizer = &apiRecordingAuthorizer{err: errors.New("denied")}
	conn := dialAppWebSocket(t, app, "/ws/exec")

	require.NoError(t, conn.WriteJSON(remoteexec.CommandRequest{Command: "printf blocked"}))

	var msg remoteexec.Message
	require.NoError(t, conn.ReadJSON(&msg))
	assert.Equal(t, remoteexec.MessageError, msg.Type)
	assert.Contains(t, msg.Error, "denied")
}

func TestExecHealthReturnsVersion(t *testing.T) {
	app := newTestAppForPackage(t)

	req := httptest.NewRequest(http.MethodGet, "/api/exec/health", nil)
	rr := httptest.NewRecorder()
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"version":"`+agentAPIVersion+`"`)
}

func dialAppWebSocket(t *testing.T, app *App, path string) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	done := make(chan error, 1)
	go servePipeHTTP(app, serverConn, done)

	u := &url.URL{Scheme: "ws", Host: "pipe.local", Path: path}
	// 鉴权常开后 /ws/exec 也要过 withSecurity：必须带凭据握手才能成功 Upgrade+Hijack。
	// 若鉴权失败，withSecurity 会写一个无 Content-Length 的 JSON 401 响应，
	// 而 pipeResponseWriter 不关闭连接，客户端读响应体会永久阻塞等 EOF——
	// 带上本机 token 让请求走回原本成功的 Upgrade 分支，避免触发这个测试桩的局限。
	header := http.Header{"Authorization": []string{"Bearer " + app.LocalAccessToken()}}
	conn, resp, err := websocket.NewClient(clientConn, u, header, 1024, 1024)
	require.NoError(t, err)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = clientConn.Close()
		select {
		case err := <-done:
			require.NoError(t, err)
		// 这里只是防死锁兜底，不是性能断言：客户端读到 exit 消息后，
		// 服务端还要经历 Execute 返回、handler 退出、ServeHTTP 收尾等
		// 多次 goroutine 调度，高负载 CI 上 1 秒不够用，会随机超时。
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for in-memory websocket server")
		}
	})
	return conn
}

func servePipeHTTP(app *App, serverConn net.Conn, done chan<- error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			done <- fmt.Errorf("websocket pipe server panic: %v", recovered)
		}
	}()
	reader := bufio.NewReader(serverConn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		done <- err
		return
	}
	req.RequestURI = ""
	rw := &pipeResponseWriter{
		conn:   serverConn,
		reader: reader,
		header: make(http.Header),
	}
	// Authorization 已由 dialAppWebSocket 写进握手请求，这里按原样透传即可，
	// 不再重复注入（重复注入会让人误以为服务端会用自己的 token 覆盖来路凭据）。
	app.Handler().ServeHTTP(rw, req)
	done <- nil
}

// pipeResponseWriter 是给 net.Pipe() 手搓的极简 http.ResponseWriter，
// 只为让 websocket.NewClient 能在内存管道上完成一次真实的 HTTP Upgrade 握手。
//
// 陷阱（务必先读再复用）：本 responder 只能安全表达"被 Hijack 的成功响应"。
// WriteHeader/Write 从不设置 Content-Length，也从不在写完后关闭 w.conn——
// 对于成功的 Upgrade（Hijack 接管连接后自行按 WS 帧协议收发，天然没有
// "response body 长度"这个问题）这没关系；但任何**未被 Hijack**的响应
// （例如 withSecurity 401/403 走 jsonError 写一个普通 JSON body）会让客户端
// 在没有 Content-Length、没有 chunked、连接又不关闭的情况下一直等 EOF——
// 永久阻塞，且没有超时会主动报错。
//
// 后果：想用 dialAppWebSocket/pipeResponseWriter 断言"握手被拒绝"（4xx/401/403）
// 的测试会当场卡死，且现象和普通的鉴权失败测试完全不一样，排查成本很高
// （这正是本文件曾经踩过的坑：鉴权改成常开后，未带凭据的握手请求命中这条路径
// 导致测试永久 hang，而不是失败）。
//
// 使用约束：调用方必须确保请求能拿到有效凭据、真正走到 Upgrade+Hijack 成功
// 分支（参见 dialAppWebSocket 对 Authorization 头的显式注入）；如果确实需要
// 断言拒绝路径，必须先扩展本 responder 补上 Content-Length 或写完后关闭
// w.conn，不能假设它能安全表达非 Hijack 响应。
type pipeResponseWriter struct {
	conn     net.Conn
	reader   *bufio.Reader
	header   http.Header
	hijacked bool
	wrote    bool
}

func (w *pipeResponseWriter) Header() http.Header {
	return w.header
}

func (w *pipeResponseWriter) WriteHeader(status int) {
	if w.hijacked || w.wrote {
		return
	}
	w.wrote = true
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	w.header.Write(w.conn) //nolint:errcheck
	fmt.Fprint(w.conn, "\r\n")
}

func (w *pipeResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.conn.Write(data)
}

func (w *pipeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, bufio.NewReadWriter(w.reader, bufio.NewWriter(w.conn)), nil
}
