// redact.go 提供 MCP 输出脱敏和截断工具。
//
// 职责：
//   - 对 env/runtime/log 中的敏感字段做统一脱敏
//   - 限制日志结果大小，避免一次工具调用塞满上下文
//
// 边界：
//   - 不修改 agent 原始数据
package mcp

import (
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

var secretKeyParts = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "KEY", "AUTH", "COOKIE", "SESSION"}

func redactSecretMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if isSecretKey(k) {
			out[k] = "[redacted]"
		} else {
			out[k] = v
		}
	}
	return out
}

func isSecretKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, part := range secretKeyParts {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return false
}

func truncateLogEntries(entries []model.LogEntry, limit int) ([]model.LogEntry, bool) {
	if limit <= 0 || len(entries) <= limit {
		return entries, false
	}
	return entries[:limit], true
}
