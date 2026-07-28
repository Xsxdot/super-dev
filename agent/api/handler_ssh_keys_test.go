package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 空结果必须序列化为 []，不能是 null——前端按数组渲染列表。
func TestListSSHKeysReturnsEmptyArrayNotNull(t *testing.T) {
	app := &App{}
	req := httptest.NewRequest(http.MethodGet, "/api/ssh-keys", nil)
	rec := httptest.NewRecorder()

	app.listSSHKeys(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := strings.TrimSpace(rec.Body.String())
	if body == "null" {
		t.Fatal("empty result must encode as [] not null")
	}
	var keys []map[string]any
	if err := json.Unmarshal([]byte(body), &keys); err != nil {
		t.Fatalf("response is not a JSON array: %v (body=%q)", err, body)
	}
}
