// stdio_main_test.go 覆盖 stdio MCP 入口的 agent URL 解析逻辑。
//
// 职责：
//   - 验证 ResolveStdioAgentURL 的默认值与 env 覆盖、trim 行为
//
// 边界：
//   - 不覆盖 RunStdioMain 本身（依赖真实 stdin/stdout 与本机 agent，留给冒烟验证）
package mcp_test

import (
	"testing"

	"github.com/xsxdot/super-dev/agent/mcp"
)

func TestResolveStdioAgentURL(t *testing.T) {
	if got := mcp.ResolveStdioAgentURL(func(string) string { return "" }); got != "http://127.0.0.1:57017" {
		t.Fatalf("default url = %q", got)
	}
	if got := mcp.ResolveStdioAgentURL(func(k string) string {
		if k == "SUPERDEV_AGENT_URL" {
			return " http://127.0.0.1:58000 "
		}
		return ""
	}); got != "http://127.0.0.1:58000" {
		t.Fatalf("env url = %q", got)
	}
}
