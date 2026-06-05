// Package mcp 验证 MCP 输出脱敏和日志截断。
//
// 职责：
//   - 验证敏感键名被脱敏
//   - 验证日志条数按上限截断
//
// 边界：
//   - 不修改原始 agent 数据
package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestRedactSecretMapMasksSensitiveKeys(t *testing.T) {
	got := redactSecretMap(map[string]string{
		"DATABASE_URL": "postgres://localhost",
		"API_TOKEN":    "secret-token",
		"PASSWORD":     "secret-password",
	})

	assert.Equal(t, "postgres://localhost", got["DATABASE_URL"])
	assert.Equal(t, "[redacted]", got["API_TOKEN"])
	assert.Equal(t, "[redacted]", got["PASSWORD"])
}

func TestTruncateEntriesMarksLargeLogResults(t *testing.T) {
	entries := make([]model.LogEntry, 0, 3)
	for i := 0; i < 3; i++ {
		entries = append(entries, model.LogEntry{ID: int64(i + 1), Message: "line"})
	}

	got, truncated := truncateLogEntries(entries, 2)

	assert.True(t, truncated)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(1), got[0].ID)
}
