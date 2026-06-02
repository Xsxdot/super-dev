// Package main 验证 MCP Log Lab 的确定性日志生成逻辑。
//
// 职责：
//   - 验证 role 参数解析
//   - 验证稳定日志标记格式
//   - 验证 crasher 的错误序列可被 MCP 搜索和诊断
//
// 边界：
//   - 不启动真实 HTTP 服务
//   - 不依赖 SuperDev agent 或 MCP server
package main

import (
	"strings"
	"testing"
)

func TestRoleFromArgs(t *testing.T) {
	role, port := roleFromArgs([]string{"--role", "api", "--port", "19001"})
	if role != "api" || port != 19001 {
		t.Fatalf("roleFromArgs returned role=%s port=%d", role, port)
	}
}

func TestFormatLogLineContainsStableMarkers(t *testing.T) {
	line := formatLogLine("api", "INFO", "startup", map[string]string{"trace_id": targetTraceID})
	for _, want := range []string{"service=api", "level=INFO", "event=startup", "trace_id=mcp-lab-target"} {
		if !strings.Contains(line, want) {
			t.Fatalf("log line missing %q: %s", want, line)
		}
	}
}

func TestCrasherEventsContainDiagnosisEvidence(t *testing.T) {
	events := crasherEvents()
	joined := strings.Join(events, "\n")
	for _, want := range []string{"database connection refused", "retry exhausted", targetTraceID} {
		if !strings.Contains(joined, want) {
			t.Fatalf("crasher events missing %q: %s", want, joined)
		}
	}
}
