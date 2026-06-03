// Package main 测试 SuperDev onboarding 示例服务的可诊断日志格式。
//
// 职责：
//   - 验证示例服务输出稳定、可解析的单行日志
//   - 约束字段排序，避免演示日志在不同运行中抖动
//
// 边界：
//   - 不启动 HTTP 服务
//   - 不依赖 SuperDev agent 或 MCP server
//   - 不访问外部网络、数据库或用户文件
package main

import "testing"

func TestFormatLogLineSortsFields(t *testing.T) {
	got := formatLogLine("INFO", "startup", map[string]string{
		"seq":  "2",
		"addr": "127.0.0.1:18191",
	})
	want := `service=sample-api level=INFO event=startup addr="127.0.0.1:18191" seq="2"`
	if got != want {
		t.Fatalf("formatLogLine() = %q, want %q", got, want)
	}
}
