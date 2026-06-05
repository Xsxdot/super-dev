// Package main 是 SuperDev MCP server 的 stdio 启动入口。
//
// 职责：
//   - 读取 SUPERDEV_AGENT_URL 或默认 agent 地址
//   - 创建 MCP server
//   - 通过 stdin/stdout 运行 MCP JSON-RPC stdio transport
//
// 边界：
//   - 不启动 SuperDev agent
//   - 不写 stdout 日志，避免污染 MCP 协议流
package main

import (
	"context"
	"log"
	"os"

	"github.com/xsxdot/super-dev/agent/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	agentURL := os.Getenv("SUPERDEV_AGENT_URL")
	if agentURL == "" {
		agentURL = "http://127.0.0.1:57017"
	}
	client := mcp.NewHTTPAgentClient(agentURL, nil)
	server := mcp.NewServer(client)
	if err := server.RunStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
