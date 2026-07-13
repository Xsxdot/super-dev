// redact.go 在任何验证证据落盘前替换认证秘密。
//
// 职责：
//   - 对结构化敏感字段和运行期已知秘密值做稳定别名替换
//   - 保留普通路径、用户名和非秘密诊断字段

// 边界：
//   - 映射只存于进程内存，不写入结果包
//   - 别名不由秘密值散列生成
package windowsvalidation

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

var secretKindSanitizer = regexp.MustCompile(`[^A-Z0-9]+`)
var bearerPattern = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+\-/=]+`)

// Redactor 持有当前进程内的秘密值到随机序号别名映射。
type Redactor struct {
	mu      sync.Mutex
	aliases map[string]string
	next    int
}

// NewRedactor 创建空的进程内脱敏器。
func NewRedactor() *Redactor {
	return &Redactor{aliases: map[string]string{}, next: 1}
}

// RegisterSecret 登记一个运行期秘密并返回稳定别名。
func (r *Redactor) RegisterSecret(kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "[REDACTED:EMPTY]"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if alias, ok := r.aliases[value]; ok {
		return alias
	}
	kind = secretKindSanitizer.ReplaceAllString(strings.ToUpper(kind), "_")
	if kind == "" {
		kind = "SECRET"
	}
	alias := fmt.Sprintf("[REDACTED:%s:T%02d]", kind, r.next)
	r.next++
	r.aliases[value] = alias
	return alias
}

// Redact 递归复制并脱敏一个 JSON 兼容值。
func (r *Redactor) Redact(value any) any {
	return r.redact(value, "")
}

// CanonicalJSON 返回无缩进、键稳定排序的 JSON 文本。
func CanonicalJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func (r *Redactor) redact(value any, parentKey string) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		credentialObject := typed["name"] != nil && (typed["desc"] != nil || typed["source"] != nil)
		for key, item := range typed {
			if isSensitiveKey(key) || (credentialObject && strings.EqualFold(key, "value")) {
				out[key] = r.redactSensitiveValue(key, item)
				continue
			}
			out[key] = r.redact(item, key)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = r.redact(item, parentKey)
		}
		return out
	case string:
		return r.replaceKnownSecrets(bearerPattern.ReplaceAllString(typed, "Bearer [REDACTED:TOKEN]"))
	default:
		return value
	}
}

func (r *Redactor) redactSensitiveValue(key string, value any) any {
	switch typed := value.(type) {
	case string:
		return r.RegisterSecret(key, typed)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = r.redactSensitiveValue(key, item)
		}
		return out
	default:
		return "[REDACTED:" + strings.ToUpper(key) + "]"
	}
}

func (r *Redactor) replaceKnownSecrets(value string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.aliases))
	for secret := range r.aliases {
		keys = append(keys, secret)
	}
	// 先替换长值，避免一个秘密是另一个秘密子串时留下残片。
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, secret := range keys {
		value = strings.ReplaceAll(value, secret, r.aliases[secret])
	}
	return value
}

func (r *Redactor) containsKnownSecret(value any) bool {
	encoded := CanonicalJSON(value)
	r.mu.Lock()
	defer r.mu.Unlock()
	for secret := range r.aliases {
		if strings.Contains(encoded, secret) {
			return true
		}
	}
	return false
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	switch normalized {
	case "authorization", "cookie", "set_cookie", "password", "token", "access_token", "refresh_token", "api_key", "apikey", "approval_token", "bootstrap_token", "private_key", "client_secret":
		return true
	default:
		return false
	}
}
