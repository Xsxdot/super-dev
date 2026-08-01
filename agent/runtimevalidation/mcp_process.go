// mcp_process.go 实现 strict runner 到 packaged superdev-mcp 的持久 stdio JSON-RPC 客户端。
//
// 职责：
//   - 启动真实 target-native MCP，完成 initialize、tools/list 和 tools/call
//   - 保持协议 stdout 只在内存 framing，并把 stderr 交给 ingestion-time redactor
//   - 在协议污染、取消或超时时关闭整个 MCP 进程树
//
// 边界：
//   - 不内嵌 MCP server，不调用 Agent HTTP 冒充工具成功
//   - 不保存 raw stdout、approval token 或 tool arguments
package runtimevalidation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const mcpStdioExitGrace = 5 * time.Second

// MCPProcessSpec 描述 packaged MCP executable、Agent loopback origin 和脱敏 stderr sink。
type MCPProcessSpec struct {
	Executable string
	Arguments  []string
	Directory  string
	Env        map[string]string
	AgentURL   string
	Stderr     io.Writer
}

// MCPInitializeResult 保存 initialize 返回的协议、server identity 与 capabilities。
type MCPInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities map[string]any `json:"capabilities"`
}

type rpcClientResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// MCPProcess 是一个 campaign 独占的真实 packaged MCP stdio 会话。
type MCPProcess struct {
	managed *ManagedProcess
	stdin   io.WriteCloser
	stdout  *bufio.Reader
	stderr  *lockedBuffer
	nextID  int
	mu      sync.Mutex
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) digest() (int, string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sum := sha256.Sum256(b.b.Bytes())
	return b.b.Len(), fmt.Sprintf("%x", sum)
}

// StartMCPProcess 启动真实 packaged MCP 并绑定 loopback Agent origin。
//
// 参数：
//   - ctx: 启动前取消上下文
//   - spec: MCP binary、Agent URL、env 与脱敏 stderr sink
//
// 返回：
//   - 尚未 initialize 的持久 MCPProcess
//   - Agent origin、pipe、binary 或进程树启动错误
//
// 注意：
//   - AgentURL 必须是无 user/query/path 的 127.0.0.1 HTTP origin。
//   - 鉴权常开后仍不在这里注入 token 环境变量：packaged MCP 与 disposable Agent
//     同机同用户，靠自身的凭据自举完成鉴权（GET /api/security/health 换
//     local_token_path → 读本机文件 → 缓存），显式注入反而绕过了这条自举路径，
//     而它本身就是「credentialed agent 全工具可用」这条验收路径要证明的东西。
func StartMCPProcess(ctx context.Context, spec MCPProcessSpec) (*MCPProcess, error) {
	agentURL, err := canonicalLoopbackAgentURL(spec.AgentURL)
	if err != nil {
		return nil, err
	}
	stderr := &lockedBuffer{}
	stderrSink := spec.Stderr
	if stderrSink == nil {
		stderrSink = stderr
	} else {
		stderrSink = io.MultiWriter(stderr, stderrSink)
	}
	env := make(map[string]string, len(spec.Env)+1)
	for key, value := range spec.Env {
		env[key] = value
	}
	env["SUPERDEV_AGENT_URL"] = agentURL
	commandSpec := ProcessSpec{
		Name: "superdev-mcp", Executable: spec.Executable, Arguments: spec.Arguments,
		Directory: spec.Directory, Env: env, Stderr: stderrSink,
	}
	cmd, digest, err := commandFromSpec(commandSpec)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open MCP stdout: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	managed, err := startManagedCommand(cmd, "superdev-mcp", digest)
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	logger.GetLogger().WithEntryName("RuntimeValidationMCP").WithFields(map[string]any{"pid": managed.PID(), "agent_origin": agentURL}).Info("packaged MCP stdio 会话已启动")
	return &MCPProcess{managed: managed, stdin: stdin, stdout: bufio.NewReader(stdout), stderr: stderr, nextID: 1}, nil
}

// Initialize 与 packaged MCP 完成协议握手并发送 initialized 通知。
//
// 参数：
//   - ctx: 单次协议调用期限
//
// 返回：
//   - MCP server identity/capabilities
//   - JSON-RPC、解码或通知写入错误
func (c *MCPProcess) Initialize(ctx context.Context) (MCPInitializeResult, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationMCP")
	log.Info("开始 packaged MCP initialize")
	raw, err := c.callRPC(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name": "superdev-runtime-validation", "version": "1.0.0",
		},
	})
	if err != nil {
		log.WithErr(err).Error("packaged MCP initialize 失败")
		return MCPInitializeResult{}, err
	}
	var result MCPInitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode MCP initialize: %w", err)
	}
	if strings.TrimSpace(result.ServerInfo.Name) == "" || strings.TrimSpace(result.ProtocolVersion) == "" {
		return result, fmt.Errorf("MCP initialize returned incomplete server identity")
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return result, err
	}
	log.WithFields(map[string]any{"server": result.ServerInfo.Name, "version": result.ServerInfo.Version, "protocol": result.ProtocolVersion}).Info("packaged MCP initialize 完成")
	return result, nil
}

// ListTools 读取当前 live tools/list 并返回排序后的唯一名称集合。
//
// 参数：
//   - ctx: 单次协议调用期限
//
// 返回：
//   - 动态 live tool 名称
//   - JSON-RPC、解码、空名或重复名错误
//
// 注意：调用方必须在任何业务 mutation 前把返回值交给 CompareCoverage。
func (c *MCPProcess) ListTools(ctx context.Context) ([]string, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationMCP")
	log.Info("开始读取 packaged MCP live tools/list")
	raw, err := c.callRPC(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode MCP tools/list: %w", err)
	}
	names := make([]string, 0, len(payload.Tools))
	seen := map[string]struct{}{}
	for _, tool := range payload.Tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			return nil, fmt.Errorf("MCP tools/list returned unnamed tool")
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("MCP tools/list returned duplicate tool %s", name)
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Strings(names)
	log.WithField("tool_count", len(names)).Info("packaged MCP live tools/list 读取完成")
	return names, nil
}

// CallTool 通过真实 MCP tools/call 执行一个场景步骤。
//
// 参数：
//   - ctx: 单次工具调用期限
//   - name: manifest 已声明的工具名
//   - arguments: 变量解析后的业务参数；不会写入日志
//
// 返回：
//   - 完整 MCP ToolCallResult
//   - JSON-RPC 或结果解码错误
//
// 注意：isError/application ok/assertions 由 ToolExecutor 继续严格判定。
func (c *MCPProcess) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationMCP").WithField("tool", name)
	log.Info("开始 packaged MCP tools/call")
	raw, err := c.callRPC(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		log.WithErr(err).Error("packaged MCP tools/call 协议失败")
		return ToolCallResult{}, err
	}
	var result ToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return result, fmt.Errorf("decode MCP tool %s result: %w", name, err)
	}
	log.WithField("is_error", result.IsError).Info("packaged MCP tools/call 完成")
	return result, nil
}

// PID 返回 packaged MCP 真实进程 ID。
func (c *MCPProcess) PID() int {
	if c == nil {
		return 0
	}
	return c.managed.PID()
}

// Wait 等待 packaged MCP 退出或 context 结束。
func (c *MCPProcess) Wait(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.managed.Wait(ctx)
}

// Close 关闭 stdin 并有界回收 packaged MCP 进程树。
//
// 参数：
//   - ctx: cleanup deadline
//
// 返回：
//   - 进程树无法在 deadline 内关闭时的错误
func (c *MCPProcess) Close(ctx context.Context) error {
	if c == nil {
		return nil
	}
	log := logger.GetLogger().WithEntryName("RuntimeValidationMCP").WithField("pid", c.PID())
	log.Info("开始关闭 packaged MCP stdio 会话")
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	// stdio EOF 是 MCP 的正式关闭信号。先观察协议进程自行退出，避免 Close 紧接着向
	// 已退出但 Wait 尚未完成的 process group 发 TERM，并把 Darwin EPERM 误记为残留。
	graceCtx, graceCancel := context.WithTimeout(ctx, mcpStdioExitGrace)
	_ = c.managed.Wait(graceCtx)
	graceCancel()
	err := c.managed.Close(ctx)
	if err != nil {
		log.WithErr(err).Error("packaged MCP stdio 会话关闭失败")
		return err
	}
	log.Info("packaged MCP stdio 会话已关闭")
	return nil
}

func (c *MCPProcess) callRPC(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	if err := c.writeJSON(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("write MCP %s request: %w", method, err)
	}
	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := c.stdout.ReadBytes('\n')
		read <- readResult{line: line, err: err}
	}()
	var item readResult
	select {
	case item = <-read:
	case <-ctx.Done():
		cleanupCtx, cancel := context.WithTimeout(context.Background(), processGracePeriod)
		defer cancel()
		_ = c.managed.Close(cleanupCtx)
		return nil, fmt.Errorf("MCP %s canceled: %w", method, ctx.Err())
	}
	if item.err != nil {
		size, digest := c.stderr.digest()
		return nil, fmt.Errorf("read MCP %s response: %w; stderr_size=%d sha256=%s", method, item.err, size, digest)
	}
	var response rpcClientResponse
	if err := json.Unmarshal(item.line, &response); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), processGracePeriod)
		defer cancel()
		_ = c.managed.Close(cleanupCtx)
		return nil, fmt.Errorf("decode MCP %s response: %w", method, err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("MCP %s JSON-RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	if response.JSONRPC != "2.0" || strings.TrimSpace(string(response.ID)) == "" {
		return nil, fmt.Errorf("MCP %s returned invalid JSON-RPC envelope", method)
	}
	return response.Result, nil
}

func (c *MCPProcess) notify(method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeJSON(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *MCPProcess) writeJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = c.stdin.Write(raw)
	return err
}

func canonicalLoopbackAgentURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if raw != trimmed {
		return "", fmt.Errorf("Agent URL is not canonical")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("Agent URL must be an unadorned HTTP 127.0.0.1 origin")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("Agent URL port is invalid")
	}
	canonical := "http://127.0.0.1:" + strconv.Itoa(port)
	if parsed.String() != canonical {
		return "", fmt.Errorf("Agent URL is not canonical")
	}
	return canonical, nil
}
