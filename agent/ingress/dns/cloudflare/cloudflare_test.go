// cloudflare_test.go 验证 Cloudflare DNS provider 的 API 收敛语义。
//
// 职责：
//   - 验证 DNS 记录创建、更新、无需变更和删除请求
//   - 验证请求携带 Cloudflare Bearer Token
//
// 边界：
//   - 不访问真实 Cloudflare API
//   - 不测试 Cloudflare 控制台侧效果
package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/xsxdot/super-dev/agent/ingress"
)

func TestEnsureRecordCreatesMissingRecord(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		assertEqual(t, r.Header.Get("Authorization"), "Bearer token-1")
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == http.MethodPost:
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertEqual(t, body["type"], "A")
			assertEqual(t, body["name"], "api.example.com")
			assertEqual(t, body["content"], "203.0.113.10")
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec-1","type":"A","name":"api.example.com","content":"203.0.113.10","ttl":300}}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	provider := New(Config{Name: "cloudflare-prod", ZoneID: "zone-1", APIToken: "token-1", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, true)
	assertEqual(t, result.Record.ID, "rec-1")
	assertStringSliceEqual(t, methods, []string{
		"GET /client/v4/zones/zone-1/dns_records",
		"POST /client/v4/zones/zone-1/dns_records",
	})
}

func TestEnsureRecordUpdatesChangedRecord(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"rec-1","type":"A","name":"api.example.com","content":"198.51.100.1","ttl":120}]}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/dns_records/rec-1"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertEqual(t, body["content"], "203.0.113.10")
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec-1","type":"A","name":"api.example.com","content":"203.0.113.10","ttl":300}}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	provider := New(Config{Name: "cloudflare-prod", ZoneID: "zone-1", APIToken: "token-1", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, true)
	assertStringSliceEqual(t, methods, []string{
		"GET /client/v4/zones/zone-1/dns_records",
		"PUT /client/v4/zones/zone-1/dns_records/rec-1",
	})
}

func TestEnsureRecordKeepsMatchingRecord(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"rec-1","type":"A","name":"api.example.com","content":"203.0.113.10","ttl":300}]}`))
	}))
	defer srv.Close()

	provider := New(Config{Name: "cloudflare-prod", ZoneID: "zone-1", APIToken: "token-1", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, false)
	assertEqual(t, result.Record.ID, "rec-1")
	assertStringSliceEqual(t, methods, []string{"GET /client/v4/zones/zone-1/dns_records"})
}

func TestEnsureRecordDiscoversZoneIDWhenConfigOmitsIt(t *testing.T) {
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		assertEqual(t, r.Header.Get("Authorization"), "Bearer token-1")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/client/v4/zones" && r.URL.Query().Get("name") == "api.example.com":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/client/v4/zones" && r.URL.Query().Get("name") == "example.com":
			_, _ = w.Write([]byte(`{"success":true,"result":[{"id":"zone-1","name":"example.com"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/client/v4/zones/zone-1/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/client/v4/zones/zone-1/dns_records":
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec-1","type":"A","name":"api.example.com","content":"203.0.113.10","ttl":300}}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	provider := New(Config{Name: "cloudflare-prod", APIToken: "token-1", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, true)
	assertStringSliceEqual(t, methods, []string{
		"GET /client/v4/zones?name=api.example.com",
		"GET /client/v4/zones?name=example.com",
		"GET /client/v4/zones/zone-1/dns_records?name=api.example.com",
		"POST /client/v4/zones/zone-1/dns_records?",
	})
}

func TestRemoveRecordDeletesByRecordID(t *testing.T) {
	var deletedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deletedPath = r.URL.Path
		assertEqual(t, r.Method, http.MethodDelete)
		_, _ = w.Write([]byte(`{"success":true,"result":{"id":"rec-1"}}`))
	}))
	defer srv.Close()

	provider := New(Config{Name: "cloudflare-prod", ZoneID: "zone-1", APIToken: "token-1", BaseURL: srv.URL})
	if err := provider.RemoveRecord(context.Background(), ingress.Record{ID: "rec-1", Name: "api.example.com"}); err != nil {
		t.Fatalf("RemoveRecord() error = %v", err)
	}
	assertEqual(t, deletedPath, "/client/v4/zones/zone-1/dns_records/rec-1")
}

func assertEqual[T comparable](t *testing.T, got T, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func assertBool(t *testing.T, got bool, want bool) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func assertStringSliceEqual(t *testing.T, got []string, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}
