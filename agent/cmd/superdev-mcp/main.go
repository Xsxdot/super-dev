// Package main 是 SuperDev MCP server 的 stdio 启动入口。
//
// 职责：
//   - 读取 SUPERDEV_AGENT_URL 或默认 agent 地址
//   - 凭据自举：SUPERDEV_AGENT_TOKEN 显式优先，缺省自动读取本机 local-access-token
//   - 创建 MCP server 并通过 stdin/stdout 运行 JSON-RPC stdio transport
//
// 边界：
//   - 不启动 SuperDev agent
//   - 不写 stdout 日志，避免污染 MCP 协议流
//   - token 只进内存与请求头：不写日志、不写任何配置文件
package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/xsxdot/super-dev/agent/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	agentURL := strings.TrimSpace(os.Getenv("SUPERDEV_AGENT_URL"))
	if agentURL == "" {
		agentURL = "http://127.0.0.1:57017"
	}
	// 凭据自举链：显式 env（远程/CI/系统级安装）→ 本机 local-access-token 自动发现。
	var source mcp.TokenSource
	if token := strings.TrimSpace(os.Getenv("SUPERDEV_AGENT_TOKEN")); token != "" {
		source = &mcp.StaticTokenSource{Value: token}
		log.Printf("[SuperDev] mcp: using token from SUPERDEV_AGENT_TOKEN")
	} else {
		source = mcp.NewLocalFileTokenSource(agentURL, nil)
		log.Printf("[SuperDev] mcp: no SUPERDEV_AGENT_TOKEN, bootstrapping from local access token")
	}
	client := mcp.NewHTTPAgentClientWithToken(agentURL, nil, source)
	server := mcp.NewServer(client)
	if err := server.RunStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
