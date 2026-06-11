// sanitize.go 负责清洗返回给 AI/MCP 的 DAP 变量与求值结果。
//
// 职责：
//   - 递归复制 DAP 响应，避免修改底层 adapter 返回对象
//   - 对 secret-looking key/value 做脱敏
//   - 对长字符串、过深结构和过大集合做限长
//
// 边界：
//   - 不改变 DAP 请求内容
//   - 不负责审计日志，审计由 API 层处理
package codedebug

import (
	"sort"
	"strings"
)

const (
	debugRedactedValue  = "[redacted]"
	debugTruncatedValue = "[truncated]"
	debugMaxStringLen   = 1024
	debugMaxItems       = 100
	debugMaxDepth       = 8
)

var secretKeyFragments = []string{
	"password",
	"passwd",
	"pwd",
	"secret",
	"token",
	"apikey",
	"api_key",
	"accesskey",
	"access_key",
	"privatekey",
	"private_key",
	"auth",
	"authorization",
	"cookie",
	"session",
}

func sanitizeDAPMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out, _ := sanitizeDAPValue(in).(map[string]any)
	return out
}

func sanitizeDAPValue(value any) any {
	return sanitizeValue("", value, 0)
}

func sanitizeValue(key string, value any, depth int) any {
	if depth > debugMaxDepth {
		return debugTruncatedValue
	}
	if looksSecretKey(key) {
		return debugRedactedValue
	}
	switch v := value.(type) {
	case map[string]any:
		return sanitizeMap(v, depth)
	case []map[string]any:
		limit := len(v)
		if limit > debugMaxItems {
			limit = debugMaxItems
		}
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, sanitizeValue(key, v[i], depth+1))
		}
		return out
	case []any:
		limit := len(v)
		if limit > debugMaxItems {
			limit = debugMaxItems
		}
		out := make([]any, 0, limit)
		for i := 0; i < limit; i++ {
			out = append(out, sanitizeValue(key, v[i], depth+1))
		}
		return out
	case string:
		if looksSecretValue(v) {
			return debugRedactedValue
		}
		return truncateDebugString(v)
	default:
		return v
	}
}

func sanitizeMap(in map[string]any, depth int) map[string]any {
	out := map[string]any{}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > debugMaxItems {
		keys = keys[:debugMaxItems]
	}
	secretVariable := false
	if name, ok := in["name"].(string); ok && looksSecretKey(name) {
		secretVariable = true
	}
	for _, key := range keys {
		if secretVariable && (key == "name" || key == "value" || key == "result") {
			out[key] = debugRedactedValue
			continue
		}
		out[key] = sanitizeValue(key, in[key], depth+1)
	}
	return out
}

func truncateDebugString(value string) string {
	if len(value) <= debugMaxStringLen {
		return value
	}
	return value[:debugMaxStringLen] + "..."
}

func looksSecretKey(key string) bool {
	normalized := normalizeSecretText(key)
	if normalized == "" {
		return false
	}
	for _, fragment := range secretKeyFragments {
		if strings.Contains(normalized, normalizeSecretText(fragment)) {
			return true
		}
	}
	return false
}

func looksSecretValue(value string) bool {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "bearer ") ||
		strings.Contains(lower, "basic ") ||
		strings.Contains(lower, "authorization:") ||
		strings.Contains(lower, "password=") ||
		strings.Contains(lower, "secret") {
		return true
	}
	if strings.HasPrefix(trimmed, "sk-") && len(trimmed) >= 24 {
		return true
	}
	if (strings.HasPrefix(trimmed, "ghp_") || strings.HasPrefix(trimmed, "github_pat_")) && len(trimmed) >= 24 {
		return true
	}
	return false
}

func normalizeSecretText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return replacer.Replace(value)
}
