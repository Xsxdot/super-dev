// dap.go 实现 Debug Adapter Protocol 的轻量客户端。
//
// 职责：
//   - 处理 DAP Content-Length framing
//   - 串行写请求、独立读响应和事件
//   - 提供调试会话需要的常用 DAP request helper
//
// 边界：
//   - 不启动 adapter 进程
//   - 不解释语言特定变量结构
package codedebug

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var _ DAP = (*DAPClient)(nil)

// DAPClient 是面向单个 adapter 连接的 DAP 客户端。
//
// 设计要点（为什么需要独立读协程）：
//   - DAP adapter 会在没有任何客户端请求时主动推送事件（stopped/output/
//     terminated）。如果只在某次 Request 的循环里被动读，continue 之后若不再
//     发请求，stopped 事件就永远读不到；这正是断点调试最核心的时序。
//   - 因此用单一 readLoop 协程独占读取 conn：response 按 request_seq 路由给
//     对应的等待者，event 投递到 events channel。所有调用方并发安全，且不再
//     有人直接读 conn。
type DAPClient struct {
	conn    net.Conn
	seq     atomic.Int64
	writeMu sync.Mutex

	mu       sync.Mutex
	pending  map[int]chan map[string]any
	events   chan map[string]any
	closed   chan struct{}
	closeErr error
}

// DialDAP 建立到 DAP adapter 的 TCP 连接并启动独立读协程。
func DialDAP(ctx context.Context, addr string, timeout time.Duration) (*DAPClient, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	c := &DAPClient{
		conn:    conn,
		pending: make(map[int]chan map[string]any),
		events:  make(chan map[string]any, 64),
		closed:  make(chan struct{}),
	}
	go c.readLoop(bufio.NewReader(conn))
	return c, nil
}

// Close 关闭 DAP TCP 连接。
func (c *DAPClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// readLoop 独占读取连接，是唯一从 conn 读数据的地方。
func (c *DAPClient) readLoop(r *bufio.Reader) {
	for {
		msg, err := readMessage(r)
		if err != nil {
			c.mu.Lock()
			c.closeErr = err
			// 唤醒所有在等响应的请求，避免调用方泄漏。
			for seq, ch := range c.pending {
				close(ch)
				delete(c.pending, seq)
			}
			c.mu.Unlock()
			close(c.closed)
			return
		}
		switch msg["type"] {
		case "response":
			seq := intFromAny(msg["request_seq"])
			c.mu.Lock()
			ch, ok := c.pending[seq]
			if ok {
				delete(c.pending, seq)
			}
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
		case "event":
			select {
			case c.events <- msg:
			default:
				// 事件通道满时丢弃新事件，避免阻塞唯一读协程拖死响应路由。
			}
		}
	}
}

// Initialize 发送 DAP initialize 请求并返回 adapter capabilities。
func (c *DAPClient) Initialize(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, "initialize", map[string]any{
		"adapterID":       "superdev",
		"pathFormat":      "path",
		"linesStartAt1":   true,
		"columnsStartAt1": true,
	})
}

// Request 发送一条 DAP request，并等待匹配 request_seq 的 response。
func (c *DAPClient) Request(ctx context.Context, command string, args map[string]any) (map[string]any, error) {
	seq := int(c.seq.Add(1))
	req := map[string]any{"type": "request", "seq": seq, "command": command}
	if args != nil {
		req["arguments"] = args
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// 在写之前登记等待通道，避免 adapter 极快响应时 response 早于 pending。
	respCh := make(chan map[string]any, 1)
	c.mu.Lock()
	if c.closeErr != nil {
		c.mu.Unlock()
		return nil, c.closeErr
	}
	c.pending[seq] = respCh
	c.mu.Unlock()

	c.writeMu.Lock()
	_, werr := fmt.Fprintf(c.conn, "Content-Length: %d\r\n\r\n%s", len(body), body)
	c.writeMu.Unlock()
	if werr != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, werr
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-c.closed:
		select {
		case msg, ok := <-respCh:
			return responseBody(command, msg, ok)
		default:
		}
		return nil, fmt.Errorf("dap connection closed: %w", c.closeErr)
	case msg, ok := <-respCh:
		return responseBody(command, msg, ok)
	}
}

func responseBody(command string, msg map[string]any, ok bool) (map[string]any, error) {
	if !ok {
		return nil, fmt.Errorf("dap connection closed before response")
	}
	if success, _ := msg["success"].(bool); !success {
		if message, _ := msg["message"].(string); message != "" {
			return nil, fmt.Errorf("dap %s failed: %s", command, message)
		}
		return nil, fmt.Errorf("dap %s failed", command)
	}
	if respBody, ok := msg["body"].(map[string]any); ok {
		return respBody, nil
	}
	return map[string]any{}, nil
}

// readMessage 解析一条 DAP 报文，Content-Length 头大小写不敏感。
func readMessage(r *bufio.Reader) (map[string]any, error) {
	length := 0
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			length, _ = strconv.Atoi(strings.TrimSpace(line[len("content-length:"):]))
		}
	}
	if length <= 0 {
		return nil, fmt.Errorf("dap message missing content length")
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, err
	}
	var msg map[string]any
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

// WaitForStopped 等待 adapter 主动推送的 stopped 事件。
//
// 由独立读协程把事件喂进 c.events，因此 continue 之后无需再发任何请求。
func (c *DAPClient) WaitForStopped(ctx context.Context) (map[string]any, error) {
	for {
		select {
		case event := <-c.events:
			if body, ok := stoppedBody(event); ok {
				return body, nil
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-c.closed:
			select {
			case event := <-c.events:
				if body, ok := stoppedBody(event); ok {
					return body, nil
				}
			default:
			}
			return nil, fmt.Errorf("dap connection closed: %w", c.closeErr)
		case event := <-c.events:
			if body, ok := stoppedBody(event); ok {
				return body, nil
			}
		}
	}
}

func stoppedBody(event map[string]any) (map[string]any, bool) {
	if event["event"] != "stopped" {
		return nil, false
	}
	if body, ok := event["body"].(map[string]any); ok {
		return body, true
	}
	return map[string]any{}, true
}

// Launch 发送 DAP launch 请求。
func (c *DAPClient) Launch(ctx context.Context, args map[string]any) error {
	_, err := c.Request(ctx, "launch", args)
	return err
}

// SetBreakpoints 在指定源码路径设置断点。
func (c *DAPClient) SetBreakpoints(ctx context.Context, source string, lines []int) (map[string]any, error) {
	points := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		points = append(points, map[string]any{"line": line})
	}
	return c.Request(ctx, "setBreakpoints", map[string]any{
		"source":      map[string]any{"path": source},
		"breakpoints": points,
	})
}

// Continue 让指定线程继续运行。
func (c *DAPClient) Continue(ctx context.Context, threadID int) error {
	_, err := c.Request(ctx, "continue", map[string]any{"threadId": threadID})
	return err
}

// Pause 暂停指定线程。
func (c *DAPClient) Pause(ctx context.Context, threadID int) error {
	_, err := c.Request(ctx, "pause", map[string]any{"threadId": threadID})
	return err
}

// Next 对指定线程执行 step over。
func (c *DAPClient) Next(ctx context.Context, threadID int) error {
	_, err := c.Request(ctx, "next", map[string]any{"threadId": threadID})
	return err
}

// StepIn 对指定线程执行 step in。
func (c *DAPClient) StepIn(ctx context.Context, threadID int) error {
	_, err := c.Request(ctx, "stepIn", map[string]any{"threadId": threadID})
	return err
}

// StepOut 对指定线程执行 step out。
func (c *DAPClient) StepOut(ctx context.Context, threadID int) error {
	_, err := c.Request(ctx, "stepOut", map[string]any{"threadId": threadID})
	return err
}

// StackTrace 读取指定线程的调用栈。
func (c *DAPClient) StackTrace(ctx context.Context, threadID int) (map[string]any, error) {
	return c.Request(ctx, "stackTrace", map[string]any{"threadId": threadID})
}

// Scopes 读取指定 stack frame 的 scopes。
func (c *DAPClient) Scopes(ctx context.Context, frameID int) (map[string]any, error) {
	return c.Request(ctx, "scopes", map[string]any{"frameId": frameID})
}

// Variables 读取指定 variablesReference 下的变量列表。
func (c *DAPClient) Variables(ctx context.Context, variablesReference int) (map[string]any, error) {
	return c.Request(ctx, "variables", map[string]any{"variablesReference": variablesReference})
}

// Evaluate 在指定 frame 上执行 DAP evaluate 请求。
func (c *DAPClient) Evaluate(ctx context.Context, expression string, frameID int) (map[string]any, error) {
	return c.Request(ctx, "evaluate", map[string]any{"expression": expression, "frameId": frameID, "context": "repl"})
}

// Disconnect 关闭 DAP 调试会话并终止被调试进程。
func (c *DAPClient) Disconnect(ctx context.Context) error {
	_, err := c.Request(ctx, "disconnect", map[string]any{"terminateDebuggee": true})
	return err
}
