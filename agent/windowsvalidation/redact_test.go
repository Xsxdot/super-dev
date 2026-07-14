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

func TestEvidencePathRedactionDoesNotRegisterCommonScalarGlobally(t *testing.T) {
	t.Parallel()
	r := NewRedactor()
	request := map[string]any{}
	response := map[string]any{
		"structuredContent": map[string]any{
			"data": map[string]any{
				"result": map[string]any{"variablesReference": float64(0)},
			},
		},
	}

	applyEvidenceRedactions(request, response, []string{"structuredContent.data.result.variablesReference"}, r)
	redacted := r.Redact(map[string]any{
		"response":        response,
		"product_version": "0.2.1",
		"campaign_id":     "w10x64-e3cc94f-20260714T065650Z-1e3e78",
		"readiness_url":   "http://127.0.0.1:18190/ready",
	}).(map[string]any)

	encoded := CanonicalJSON(redacted)
	if !strings.Contains(encoded, "[REDACTED:EVIDENCE:") {
		t.Fatalf("targeted evidence value was not redacted: %s", encoded)
	}
	if redacted["product_version"] != "0.2.1" || redacted["campaign_id"] != "w10x64-e3cc94f-20260714T065650Z-1e3e78" || redacted["readiness_url"] != "http://127.0.0.1:18190/ready" {
		t.Fatalf("targeted scalar redaction polluted unrelated evidence: %#v", redacted)
	}
}

func TestRedactorDoesNotRewriteExistingAliases(t *testing.T) {
	t.Parallel()
	r := NewRedactor()
	alias := r.RegisterSecret("TOKEN", "0")

	redacted := r.Redact(map[string]any{"alias": alias, "message": "value=0"}).(map[string]any)

	if redacted["alias"] != alias {
		t.Fatalf("existing alias was recursively redacted: got=%q want=%q", redacted["alias"], alias)
	}
	if strings.Contains(redacted["message"].(string), "value=0") {
		t.Fatalf("registered secret was not redacted: %#v", redacted)
	}
	if r.containsKnownSecret(redacted) {
		t.Fatalf("redaction aliases must not make the invariant report a leaked secret: %#v", redacted)
	}
}
