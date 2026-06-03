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
	"github.com/superdev/agent/remoteexec"
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

func TestExecHealthReturnsNoContent(t *testing.T) {
	app := newTestAppForPackage(t)

	req := httptest.NewRequest(http.MethodGet, "/api/exec/health", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func dialAppWebSocket(t *testing.T, app *App, path string) *websocket.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	done := make(chan error, 1)
	go servePipeHTTP(app, serverConn, done)

	u := &url.URL{Scheme: "ws", Host: "pipe.local", Path: path}
	conn, resp, err := websocket.NewClient(clientConn, u, nil, 1024, 1024)
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
		case <-time.After(time.Second):
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
	app.Handler().ServeHTTP(rw, req)
	done <- nil
}

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
