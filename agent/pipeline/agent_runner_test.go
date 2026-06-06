// agent_runner_test.go 验证隧道后端 agent runner 的协议适配。
//
// 职责：
//   - 用内存 WebSocket 和自定义 HTTP client 模拟远端 agent
//   - 验证输出流、退出码、multipart 上传和目录打包
//
// 边界：
//   - 不建立真实隧道
//   - 不测试 RoutingRunner 的健康状态选择
package pipeline

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/remoteexec"
)

type agentRunnerResolver struct {
	base string
	err  error
}

func (r agentRunnerResolver) BaseURL(hostID string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.base, nil
}

func TestAgentRunnerRunRemoteStreamsLinesAndExit(t *testing.T) {
	var gotReq remoteexec.CommandRequest
	runner := NewAgentRunner(agentRunnerResolver{base: "http://agent.local"})
	runner.dialWS = pipeAgentWebSocketDialer(t, "/ws/exec", func(conn *websocket.Conn) error {
		if err := conn.ReadJSON(&gotReq); err != nil {
			return err
		}
		if err := conn.WriteJSON(remoteexec.Message{Type: remoteexec.MessageOutput, Stream: "stdout", Line: "hello"}); err != nil {
			return err
		}
		if err := conn.WriteJSON(remoteexec.Message{Type: remoteexec.MessageOutput, Stream: "stderr", Line: "warn"}); err != nil {
			return err
		}
		return conn.WriteJSON(remoteexec.Message{Type: remoteexec.MessageExit, ExitCode: 0})
	})

	var lines []string
	err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "printf hi", "/srv/app", func(line, stream string) {
		lines = append(lines, stream+":"+line)
	})

	require.NoError(t, err)
	assert.Equal(t, remoteexec.CommandRequest{Command: "printf hi", WorkDir: "/srv/app"}, gotReq)
	assert.Equal(t, []string{"stdout:hello", "stderr:warn"}, lines)
}

func TestAgentRunnerRunRemoteMapsNonZeroExit(t *testing.T) {
	runner := NewAgentRunner(agentRunnerResolver{base: "http://agent.local"})
	runner.dialWS = pipeAgentWebSocketDialer(t, "/ws/exec", func(conn *websocket.Conn) error {
		var req remoteexec.CommandRequest
		if err := conn.ReadJSON(&req); err != nil {
			return err
		}
		return conn.WriteJSON(remoteexec.Message{Type: remoteexec.MessageExit, ExitCode: 9})
	})

	err := runner.RunRemote(context.Background(), Target{HostID: "h1"}, "exit 9", "", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote agent command")
	assert.Contains(t, err.Error(), "exit 9")
	assert.Contains(t, err.Error(), "code 9")
	var coded interface{ ExitCode() int }
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, 9, coded.ExitCode())
}

func TestAgentRunnerTransferUploadsMultipartFile(t *testing.T) {
	source := filepath.Join(t.TempDir(), "artifact.txt")
	require.NoError(t, os.WriteFile(source, []byte("payload"), 0o644))
	var gotTarget string
	var gotBody []byte
	runner := NewAgentRunner(agentRunnerResolver{base: "http://agent.local"})
	runner.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "/api/transfer", req.URL.Path)
		require.NoError(t, req.ParseMultipartForm(32<<20))
		gotTarget = req.FormValue("target")
		file, _, err := req.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		gotBody, err = io.ReadAll(file)
		require.NoError(t, err)
		return transferTestResponse(http.StatusNoContent), nil
	})}

	err := runner.Transfer(context.Background(), Target{HostID: "h1"}, source, "/srv/app/artifact.txt", nil)

	require.NoError(t, err)
	assert.Equal(t, "/srv/app/artifact.txt", gotTarget)
	assert.Equal(t, "payload", string(gotBody))
}

func TestAgentRunnerTransferPackagesDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("ok"), 0o644))
	var uploaded []byte
	runner := NewAgentRunner(agentRunnerResolver{base: "http://agent.local"})
	runner.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.NoError(t, req.ParseMultipartForm(32<<20))
		file, _, err := req.FormFile("file")
		require.NoError(t, err)
		defer file.Close()
		uploaded, err = io.ReadAll(file)
		require.NoError(t, err)
		return transferTestResponse(http.StatusNoContent), nil
	})}

	err := runner.Transfer(context.Background(), Target{HostID: "h1"}, dir, "/srv/app/site.tar.gz", nil)

	require.NoError(t, err)
	assert.True(t, tarBytesContain(t, uploaded, "index.html"))
}

func TestAgentRunnerTransferReturnsHTTPError(t *testing.T) {
	source := filepath.Join(t.TempDir(), "artifact.txt")
	require.NoError(t, os.WriteFile(source, []byte("payload"), 0o644))
	runner := NewAgentRunner(agentRunnerResolver{base: "http://agent.local"})
	runner.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return transferTestResponse(http.StatusBadGateway), nil
	})}

	err := runner.Transfer(context.Background(), Target{HostID: "h1"}, source, "/srv/app/artifact.txt", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote agent /api/transfer returned 502")
}

func tarBytesContain(t *testing.T, data []byte, name string) bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return false
		}
		require.NoError(t, err)
		if header.Name == name {
			return true
		}
	}
}

func pipeAgentWebSocketDialer(t *testing.T, wantPath string, serve func(*websocket.Conn) error) func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	return func(ctx context.Context, urlStr string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		t.Helper()
		clientConn, serverConn := net.Pipe()
		done := make(chan error, 1)
		go serveAgentPipeHTTP(serverConn, wantPath, serve, done)

		u, err := url.Parse(urlStr)
		require.NoError(t, err)
		conn, resp, err := websocket.NewClient(clientConn, u, headers, 1024, 1024)
		if err != nil {
			_ = clientConn.Close()
			return nil, nil, err
		}
		t.Cleanup(func() {
			_ = conn.Close()
			_ = clientConn.Close()
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for in-memory agent websocket server")
			}
		})
		return conn, resp, nil
	}
}

func serveAgentPipeHTTP(serverConn net.Conn, wantPath string, serve func(*websocket.Conn) error, done chan<- error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			done <- fmt.Errorf("agent websocket pipe server panic: %v", recovered)
		}
	}()
	reader := bufio.NewReader(serverConn)
	req, err := http.ReadRequest(reader)
	if err != nil {
		done <- err
		return
	}
	if req.URL.Path != wantPath {
		done <- fmt.Errorf("unexpected websocket path %q", req.URL.Path)
		return
	}
	req.RequestURI = ""
	rw := &agentPipeResponseWriter{
		conn:   serverConn,
		reader: reader,
		header: make(http.Header),
	}
	conn, err := websocket.Upgrade(rw, req, nil, 1024, 1024)
	if err != nil {
		done <- err
		return
	}
	defer conn.Close()
	done <- serve(conn)
}

type agentPipeResponseWriter struct {
	conn     net.Conn
	reader   *bufio.Reader
	header   http.Header
	hijacked bool
	wrote    bool
}

func (w *agentPipeResponseWriter) Header() http.Header {
	return w.header
}

func (w *agentPipeResponseWriter) WriteHeader(status int) {
	if w.hijacked || w.wrote {
		return
	}
	w.wrote = true
	fmt.Fprintf(w.conn, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	w.header.Write(w.conn) //nolint:errcheck
	fmt.Fprint(w.conn, "\r\n")
}

func (w *agentPipeResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return w.conn.Write(data)
}

func (w *agentPipeResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.hijacked = true
	return w.conn, bufio.NewReadWriter(w.reader, bufio.NewWriter(w.conn)), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func transferTestResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}
