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
var redactionAliasPattern = regexp.MustCompile(`\[REDACTED:[^\]]+\]`)

// Redactor 持有当前进程内的秘密值到随机序号别名映射。
type Redactor struct {
	mu           sync.Mutex
	aliases      map[string]string
	placeholders map[string]struct{}
	next         int
}

// NewRedactor 创建空的进程内脱敏器。
func NewRedactor() *Redactor {
	return &Redactor{aliases: map[string]string{}, placeholders: map[string]struct{}{}, next: 1}
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
	alias := r.nextAliasLocked(kind)
	r.aliases[value] = alias
	return alias
}

// redactOpaque 为指定证据路径生成占位符，但不把原值登记为全局秘密。
// 精确路径可能指向 0、端口或状态码；若登记为全局秘密，会污染 campaign 身份和普通诊断文本。
func (r *Redactor) redactOpaque(kind string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.nextAliasLocked(kind)
}

func applyEvidenceRedactions(request, response map[string]any, paths []string, redactor *Redactor) {
	for _, path := range paths {
		target := response
		trimmed := path
		if strings.HasPrefix(path, "request.") {
			target = request
			trimmed = strings.TrimPrefix(path, "request.")
		}
		redactAtPath(target, strings.Split(trimmed, "."), redactor)
	}
}

func redactAtPath(current any, parts []string, redactor *Redactor) {
	if len(parts) == 0 {
		return
	}
	switch typed := current.(type) {
	case map[string]any:
		part := parts[0]
		if part == "*" {
			for key := range typed {
				redactAtPath(typed[key], parts[1:], redactor)
			}
			return
		}
		value, ok := typed[part]
		if !ok {
			return
		}
		if len(parts) == 1 {
			// evidence.redact 是精确路径合同，只隐藏该位置；不能把 0、端口等普通标量升级成全局秘密。
			typed[part] = redactor.redactOpaque("EVIDENCE")
			return
		}
		redactAtPath(value, parts[1:], redactor)
	case []any:
		if parts[0] == "*" {
			for _, item := range typed {
				redactAtPath(item, parts[1:], redactor)
			}
		}
	}
}

func (r *Redactor) nextAliasLocked(kind string) string {
	kind = secretKindSanitizer.ReplaceAllString(strings.ToUpper(kind), "_")
	if kind == "" {
		kind = "SECRET"
	}
	alias := fmt.Sprintf("[REDACTED:%s:T%02d]", kind, r.next)
	r.next++
	r.placeholders[alias] = struct{}{}
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
		value = replaceOutsideRedactionAliases(value, secret, r.aliases[secret])
	}
	return value
}

func replaceOutsideRedactionAliases(value, secret, alias string) string {
	matches := redactionAliasPattern.FindAllStringIndex(value, -1)
	if len(matches) == 0 {
		return strings.ReplaceAll(value, secret, alias)
	}
	var out strings.Builder
	start := 0
	for _, match := range matches {
		out.WriteString(strings.ReplaceAll(value[start:match[0]], secret, alias))
		out.WriteString(value[match[0]:match[1]])
		start = match[1]
	}
	out.WriteString(strings.ReplaceAll(value[start:], secret, alias))
	return out.String()
}

func (r *Redactor) containsKnownSecret(value any) bool {
	encoded := CanonicalJSON(value)
	r.mu.Lock()
	defer r.mu.Unlock()
	// 只忽略本进程实际生成的占位符，避免把响应伪装成任意 [REDACTED:*] 文本来绕过泄漏检查。
	for placeholder := range r.placeholders {
		encoded = strings.ReplaceAll(encoded, placeholder, "")
	}
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
