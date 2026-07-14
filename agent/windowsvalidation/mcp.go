// mcp.go 实现验证驱动到已安装 superdev-mcp.exe 的 stdio 客户端。
//
// 职责：
//   - 启动安装目录中的 MCP sidecar
//   - 执行 initialize、tools/list 和固定 tools/call
//   - 保持协议 stdout 与结构化运行日志分离

// 边界：
//   - 不内嵌 MCP server，也不调用 Agent HTTP 绕过 packaged MCP
//   - 不缓存审批 token 或业务状态
package windowsvalidation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
)

type rpcClientResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// MCPInitializeResult 保存运行时版本门禁需要的 initialize 字段。
type MCPInitializeResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
	Capabilities map[string]any `json:"capabilities"`
}

type mcpToolCaller interface {
	CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error)
}

type runtimeAttestationClient interface {
	mcpToolCaller
	Initialize(ctx context.Context) (MCPInitializeResult, error)
	ListTools(ctx context.Context) ([]map[string]any, []string, error)
}

// MCPProcess 是一次 campaign 独占的 packaged MCP 子进程。
type MCPProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *lockedBuffer
	done   chan error
	nextID int
	mu     sync.Mutex
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

// Write 以互斥方式追加 MCP stderr 字节，避免退出与错误读取产生数据竞争。
func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

// String 返回当前 MCP stderr 快照，仅用于协议失败上下文。
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// StartMCPProcess 启动指定安装路径的 superdev-mcp.exe。
func StartMCPProcess(ctx context.Context, executable, agentURL string) (*MCPProcess, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	if strings.TrimSpace(executable) == "" {
		return nil, fmt.Errorf("MCP executable path is required")
	}
	log.WithFields(map[string]any{"mcp_path": executable, "agent_url": agentURL}).Info("开始启动已安装的 packaged MCP")
	stderr := &lockedBuffer{}
	cmd := exec.CommandContext(ctx, executable)
	cmd.Env = append(os.Environ(), "SUPERDEV_AGENT_URL="+agentURL)
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open MCP stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open MCP stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		log.WithErr(err).WithField("mcp_path", executable).Error("packaged MCP 启动失败")
		return nil, fmt.Errorf("start packaged MCP: %w", err)
	}
	process := &MCPProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		stderr: stderr,
		done:   make(chan error, 1),
		nextID: 1,
	}
	go func() { process.done <- cmd.Wait() }()
	log.WithField("pid", cmd.Process.Pid).Info("packaged MCP 已启动")
	return process, nil
}

// Initialize 与 packaged MCP 完成协议握手并发送 initialized 通知。
func (c *MCPProcess) Initialize(ctx context.Context) (MCPInitializeResult, error) {
	var out MCPInitializeResult
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	log.Info("开始 MCP initialize 握手")
	raw, err := c.callRPC(ctx, "initialize", validationInitializeParams())
	if err != nil {
		log.WithErr(err).Error("MCP initialize 握手失败")
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("decode initialize result: %w", err)
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return out, err
	}
	log.WithFields(map[string]any{"server": out.ServerInfo.Name, "version": out.ServerInfo.Version, "protocol": out.ProtocolVersion}).Info("MCP initialize 握手完成")
	return out, nil
}

func validationInitializeParams() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-11-25",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]string{
			"name":    "superdev-windows-validation",
			"version": "1.0.0",
		},
	}
}

// ListTools 读取 packaged MCP 的完整工具定义和排序后名称集合。
func (c *MCPProcess) ListTools(ctx context.Context) ([]map[string]any, []string, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	log.Info("开始读取 packaged MCP tools/list")
	raw, err := c.callRPC(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, nil, err
	}
	var payload struct {
		Tools []map[string]any `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, nil, fmt.Errorf("decode tools/list: %w", err)
	}
	names := make([]string, 0, len(payload.Tools))
	for _, tool := range payload.Tools {
		name, _ := tool["name"].(string)
		if name == "" {
			return nil, nil, fmt.Errorf("tools/list returned unnamed tool")
		}
		names = append(names, name)
	}
	sortStrings(names)
	log.WithField("tool_count", len(names)).Info("packaged MCP tools/list 读取完成")
	return payload.Tools, names, nil
}

// CallTool 通过 packaged MCP 执行一次固定工具调用。
func (c *MCPProcess) CallTool(ctx context.Context, name string, arguments map[string]any) (ToolCallResult, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	log.WithField("tool", name).Info("开始调用 packaged MCP 工具")
	raw, err := c.callRPC(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		log.WithErr(err).WithField("tool", name).Error("packaged MCP 工具协议调用失败")
		return ToolCallResult{}, err
	}
	var result ToolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ToolCallResult{}, fmt.Errorf("decode %s result: %w", name, err)
	}
	log.WithFields(map[string]any{"tool": name, "is_error": result.IsError}).Info("packaged MCP 工具调用完成")
	return result, nil
}

// Close 关闭 stdio 并确保 packaged MCP 子进程退出。
func (c *MCPProcess) Close() error {
	if c == nil {
		return nil
	}
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	log.Info("开始关闭 packaged MCP")
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	select {
	case err := <-c.done:
		if err != nil {
			log.WithErr(err).Error("packaged MCP 退出异常")
			return err
		}
		log.Info("packaged MCP 已正常关闭")
		return nil
	case <-time.After(2 * time.Second):
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		select {
		case <-c.done:
		case <-time.After(2 * time.Second):
		}
		log.Info("packaged MCP 已强制关闭")
		return nil
	}
}

func (c *MCPProcess) callRPC(ctx context.Context, method string, params map[string]any) (result json.RawMessage, rpcErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationMCP")
	defer func() {
		if rpcErr != nil {
			log.WithErr(rpcErr).WithField("method", method).Error("packaged MCP JSON-RPC 调用失败")
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextID
	c.nextID++
	request := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	if err := c.writeJSON(request); err != nil {
		return nil, fmt.Errorf("write %s request: %w", method, err)
	}
	type readResult struct {
		line string
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := c.stdout.ReadString('\n')
		read <- readResult{line: line, err: err}
	}()
	var item readResult
	select {
	case <-ctx.Done():
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		return nil, fmt.Errorf("%s canceled: %w", method, ctx.Err())
	case item = <-read:
	}
	if item.err != nil {
		stderr := strings.TrimSpace(c.stderr.String())
		digest := sha256.Sum256([]byte(stderr))
		return nil, fmt.Errorf("read %s response: %w; MCP stderr_size=%d sha256=%x", method, item.err, len(stderr), digest)
	}
	var response rpcClientResponse
	if err := json.Unmarshal([]byte(item.line), &response); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", method, err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s JSON-RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	return response.Result, nil
}

func (c *MCPProcess) notify(method string, params map[string]any) error {
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

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
