// redact_test.go 验证证据落盘前的认证秘密替换。
//
// 职责：
//   - 覆盖结构化敏感字段和运行中获得的秘密值

// 边界：
//   - 普通路径、用户名、session 标识不会被无差别删除
package windowsvalidation

import (
	"strings"
	"testing"
)

func TestRedactorReplacesSensitiveFieldsAndKnownValues(t *testing.T) {
	t.Parallel()
	r := NewRedactor()
	alias := r.RegisterSecret("TOKEN", "top-secret-token")
	input := map[string]any{
		"authorization": "Bearer top-secret-token",
		"message":       "request used top-secret-token",
		"session_id":    "brs_visible",
		"path":          `C:\\Users\\validator\\work`,
	}
	redacted := r.Redact(input).(map[string]any)
	encoded := CanonicalJSON(redacted)
	if strings.Contains(encoded, "top-secret-token") {
		t.Fatalf("secret leaked: %s", encoded)
	}
	if !strings.Contains(encoded, alias) {
		t.Fatalf("alias missing: %s", encoded)
	}
	if redacted["session_id"] != "brs_visible" || redacted["path"] != input["path"] {
		t.Fatalf("non-secret diagnostic fields changed: %#v", redacted)
	}
	if r.containsKnownSecret(redacted) {
		t.Fatalf("redacted value still contains a registered secret: %s", encoded)
	}
}
