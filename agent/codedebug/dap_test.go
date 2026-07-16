// dap_test.go 验证 DAP wire client 的 framing、response 路由和异步事件处理。
//
// 职责：
//   - 使用 fake DAP TCP server 覆盖 request/response 和 stopped event 时序
//   - 验证 Content-Length 头大小写兼容
//
// 边界：
//   - 不启动真实 DAP adapter
package codedebug

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAPClientSendsInitializeAndParsesResponse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	done := make(chan string, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		msg := readDAPMessageForTest(t, conn)
		done <- msg["command"].(string)
		writeDAPMessageForTest(t, conn, map[string]any{
			"type":        "response",
			"seq":         1,
			"request_seq": msg["seq"],
			"success":     true,
			"command":     "initialize",
			"body":        map[string]any{"supportsConfigurationDoneRequest": true},
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	body, err := client.Initialize(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "initialize", <-done)
	assert.Equal(t, true, body["supportsConfigurationDoneRequest"])
}

func TestDAPClientParsesLowercaseContentLength(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		msg := readDAPMessageForTest(t, conn)
		writeDAPMessageForTestWithHeader(t, conn, "content-length", map[string]any{
			"type":        "response",
			"seq":         1,
			"request_seq": msg["seq"],
			"success":     true,
			"command":     "initialize",
			"body":        map[string]any{"ok": true},
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	body, err := client.Initialize(context.Background())
	require.NoError(t, err)
	assert.Equal(t, true, body["ok"])
}

// TestDAPClientReceivesAsyncStoppedEvent 验证最关键的时序：
// continue 的响应几乎立即返回，真正要等的 stopped 事件是 adapter
// 在没有任何后续请求的情况下主动推送的。读协程模型必须能在
// 调用方仅 WaitForStopped、不再发请求时把该事件投递出来。
// 这是 debug_capture_at 复合工具依赖的核心链路。
func TestDAPClientReceivesAsyncStoppedEvent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		// 读取 continue 请求并立即回响应（continue 的语义是"继续跑"）。
		req := readDAPMessageForTest(t, conn)
		require.Equal(t, "continue", req["command"])
		writeDAPMessageForTest(t, conn, map[string]any{
			"type": "response", "seq": 1, "request_seq": req["seq"],
			"success": true, "command": "continue",
		})
		// 模拟 adapter 在没有任何新请求的情况下，稍后异步推送 stopped 事件。
		time.Sleep(50 * time.Millisecond)
		writeDAPMessageForTest(t, conn, map[string]any{
			"type": "event", "seq": 2, "event": "stopped",
			"body": map[string]any{"reason": "breakpoint", "threadId": 1},
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.Continue(context.Background(), 1))

	// 关键：continue 之后不再发任何请求，仅等待 stopped 事件。
	// 若 broker 没有独立读协程，此处会一直阻塞到超时。
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	body, err := client.WaitForStopped(ctx)
	require.NoError(t, err)
	assert.Equal(t, "breakpoint", body["reason"])
	assert.Equal(t, float64(1), body["threadId"])
}

func TestDAPClientAttachSendsRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	done := make(chan map[string]any, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		msg := readDAPMessageForTest(t, conn)
		done <- msg
		writeDAPMessageForTest(t, conn, map[string]any{
			"type":        "response",
			"seq":         1,
			"request_seq": msg["seq"],
			"success":     true,
			"command":     "attach",
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.Attach(context.Background(), map[string]any{"mode": "local", "processId": 1234}))
	msg := <-done
	assert.Equal(t, "attach", msg["command"])
	args := msg["arguments"].(map[string]any)
	assert.Equal(t, "local", args["mode"])
	assert.Equal(t, float64(1234), args["processId"])
}

func TestDAPClientSetBreakpointsSendsCompleteSourceDescriptor(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	done := make(chan map[string]any, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		msg := readDAPMessageForTest(t, conn)
		done <- msg
		writeDAPMessageForTest(t, conn, map[string]any{
			"type":        "response",
			"seq":         1,
			"request_seq": msg["seq"],
			"success":     true,
			"command":     "setBreakpoints",
			"body":        map[string]any{"breakpoints": []any{}},
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	_, err = client.SetBreakpoints(context.Background(), "/tmp/src/FixtureServer.kt", []int{25})
	require.NoError(t, err)
	args := (<-done)["arguments"].(map[string]any)
	source := args["source"].(map[string]any)
	assert.Equal(t, "FixtureServer.kt", source["name"])
	assert.Equal(t, "/tmp/src/FixtureServer.kt", source["path"])
	assert.Equal(t, false, args["sourceModified"])
}

func TestDAPClientDetachDoesNotTerminateDebuggee(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	done := make(chan map[string]any, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		require.NoError(t, acceptErr)
		defer conn.Close()

		msg := readDAPMessageForTest(t, conn)
		done <- msg
		writeDAPMessageForTest(t, conn, map[string]any{
			"type":        "response",
			"seq":         1,
			"request_seq": msg["seq"],
			"success":     true,
			"command":     "disconnect",
		})
	}()

	client, err := DialDAP(context.Background(), ln.Addr().String(), 2*time.Second)
	require.NoError(t, err)
	defer client.Close()

	require.NoError(t, client.Detach(context.Background()))
	msg := <-done
	assert.Equal(t, "disconnect", msg["command"])
	args := msg["arguments"].(map[string]any)
	assert.Equal(t, false, args["terminateDebuggee"])
}

func TestDAPClientSubscribeFanOut(t *testing.T) {
	client := newTestDAPClient()
	sub1, cancel1 := client.Subscribe()
	defer cancel1()
	sub2, cancel2 := client.Subscribe()
	defer cancel2()

	client.dispatchEvent(map[string]any{"event": "stopped", "body": map[string]any{"threadId": float64(1)}})

	for i, ch := range []<-chan map[string]any{sub1, sub2} {
		select {
		case event := <-ch:
			if event["event"] != "stopped" {
				t.Fatalf("sub%d got %v", i+1, event["event"])
			}
		case <-time.After(time.Second):
			t.Fatalf("sub%d did not receive event", i+1)
		}
	}
}

func TestDAPClientSubscribeCancel(t *testing.T) {
	client := newTestDAPClient()
	sub, cancel := client.Subscribe()
	cancel()

	client.dispatchEvent(map[string]any{"event": "continued"})
	select {
	case _, ok := <-sub:
		if ok {
			t.Fatal("cancelled subscriber should not receive new events")
		}
	case <-time.After(200 * time.Millisecond):
	}
}

func TestResponseBodyIncludesDAPErrorFormat(t *testing.T) {
	_, err := responseBody("launch", map[string]any{
		"type":    "response",
		"success": false,
		"command": "launch",
		"message": "Failed to launch",
		"body": map[string]any{
			"error": map[string]any{
				"format": "Failed to launch: build error details",
			},
		},
	}, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "build error details")
}

func newTestDAPClient() *DAPClient {
	return &DAPClient{closed: make(chan struct{})}
}

func readDAPMessageForTest(t *testing.T, conn net.Conn) map[string]any {
	t.Helper()
	reader := bufio.NewReader(conn)
	header, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(header, "Content-Length: "))
	var length int
	_, err = fmt.Sscanf(strings.TrimSpace(header), "Content-Length: %d", &length)
	require.NoError(t, err)
	blank, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "\r\n", blank)
	body := make([]byte, length)
	_, err = io.ReadFull(reader, body)
	require.NoError(t, err)
	var msg map[string]any
	require.NoError(t, json.Unmarshal(body, &msg))
	return msg
}

func writeDAPMessageForTest(t *testing.T, conn net.Conn, msg map[string]any) {
	t.Helper()
	writeDAPMessageForTestWithHeader(t, conn, "Content-Length", msg)
}

func writeDAPMessageForTestWithHeader(t *testing.T, conn net.Conn, header string, msg map[string]any) {
	t.Helper()
	body, err := json.Marshal(msg)
	require.NoError(t, err)
	_, err = fmt.Fprintf(conn, "%s: %d\r\n\r\n%s", header, len(body), body)
	require.NoError(t, err)
}
