// Package main 是 SuperDev MCP server 的 stdio 启动入口。
//
// 职责：
//   - 将 stdout 让给 MCP 协议流，日志改写 stderr
//   - 委托 agent/mcp.RunStdioMain 运行 stdio MCP server（与 `superdev-agent mcp`
//     子命令共用同一实现，远端机器只需分发一个 agent 二进制）
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

	"github.com/xsxdot/super-dev/agent/mcp"
)

func main() {
	log.SetOutput(os.Stderr)
	if err := mcp.RunStdioMain(context.Background(), os.Stdin, os.Stdout); err != nil {
		log.Fatal(err)
	}
}
