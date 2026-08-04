// stdio_main.go 提炼 stdio MCP 入口的可复用核心逻辑。
//
// 职责：
//   - 解析 stdio MCP 入口连接的 agent 地址（SUPERDEV_AGENT_URL 优先，默认本机 57017）
//   - 凭据自举：SUPERDEV_AGENT_TOKEN 显式优先，缺省自动读取本机 local-access-token
//   - 创建 MCP server 并通过传入的 stdin/stdout 运行 JSON-RPC stdio transport
//
// 边界：
//   - 不解析命令行 flag（由调用方——独立二进制 main 或 agent 子命令分派——负责）
//   - 不写 stdout 日志：stdout 是 MCP 协议流，任何非协议输出都会破坏协议，
//     日志必须由调用方提前 log.SetOutput(os.Stderr)
//   - 不启动 SuperDev agent 本身，只作为 MCP client 连接既有 agent
package mcp

import (
	"context"
	"io"
	"log"
	"os"
	"strings"
)

// ResolveStdioAgentURL 解析 stdio MCP 入口连接的 agent 地址（env 优先，默认本机 57017）。
func ResolveStdioAgentURL(getenv func(string) string) string {
	if url := strings.TrimSpace(getenv("SUPERDEV_AGENT_URL")); url != "" {
		return url
	}
	return "http://127.0.0.1:57017"
}

// RunStdioMain 运行 stdio MCP 服务直至 stdin 关闭或 ctx 取消。
// 凭据自举链：SUPERDEV_AGENT_TOKEN 显式优先，缺省自动读取本机 local-access-token。
func RunStdioMain(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	agentURL := ResolveStdioAgentURL(os.Getenv)
	var source TokenSource
	if token := strings.TrimSpace(os.Getenv("SUPERDEV_AGENT_TOKEN")); token != "" {
		source = &StaticTokenSource{Value: token}
		log.Printf("[SuperDev] mcp: using token from SUPERDEV_AGENT_TOKEN")
	} else {
		source = NewLocalFileTokenSource(agentURL, nil)
		log.Printf("[SuperDev] mcp: no SUPERDEV_AGENT_TOKEN, bootstrapping from local access token")
	}
	client := NewHTTPAgentClientWithToken(agentURL, nil, source)
	return NewServer(client).RunStdio(ctx, stdin, stdout)
}
