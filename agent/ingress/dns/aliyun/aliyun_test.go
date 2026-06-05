// aliyun_test.go 验证阿里云 DNS provider 的 API 收敛语义。
//
// 职责：
//   - 验证 DNS 记录创建、更新、无需变更和删除请求
//   - 验证完整域名能拆分为 DomainName 与 RR
//
// 边界：
//   - 不访问真实阿里云 API
//   - 不测试阿里云控制台侧效果
package aliyun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xsxdot/super-dev/agent/ingress"
)

func TestEnsureRecordCreatesMissingRecord(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		actions = append(actions, r.Form.Get("Action"))
		switch r.Form.Get("Action") {
		case "DescribeDomainRecords":
			_, _ = w.Write([]byte(`{"DomainRecords":{"Record":[]}}`))
		case "AddDomainRecord":
			assertEqual(t, r.Form.Get("DomainName"), "example.com")
			assertEqual(t, r.Form.Get("RR"), "api")
			assertEqual(t, r.Form.Get("Type"), "A")
			assertEqual(t, r.Form.Get("Value"), "203.0.113.10")
			_, _ = w.Write([]byte(`{"RecordId":"rec-1"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	provider := New(Config{Name: "aliyun-prod", AccessKeyID: "ak", AccessKeySecret: "sk", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, true)
	assertEqual(t, result.Record.ID, "rec-1")
	assertStringSliceEqual(t, actions, []string{"DescribeDomainRecords", "AddDomainRecord"})
}

func TestEnsureRecordUpdatesChangedRecord(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		actions = append(actions, r.Form.Get("Action"))
		switch r.Form.Get("Action") {
		case "DescribeDomainRecords":
			_, _ = w.Write([]byte(`{"DomainRecords":{"Record":[{"RecordId":"rec-1","RR":"api","Type":"A","Value":"198.51.100.1","TTL":120}]}}`))
		case "UpdateDomainRecord":
			assertEqual(t, r.Form.Get("RecordId"), "rec-1")
			assertEqual(t, r.Form.Get("Value"), "203.0.113.10")
			_, _ = w.Write([]byte(`{"RecordId":"rec-1"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	provider := New(Config{Name: "aliyun-prod", AccessKeyID: "ak", AccessKeySecret: "sk", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, true)
	assertStringSliceEqual(t, actions, []string{"DescribeDomainRecords", "UpdateDomainRecord"})
}

func TestEnsureRecordKeepsMatchingRecord(t *testing.T) {
	var actions []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		actions = append(actions, r.Form.Get("Action"))
		_, _ = w.Write([]byte(`{"DomainRecords":{"Record":[{"RecordId":"rec-1","RR":"api","Type":"A","Value":"203.0.113.10","TTL":300}]}}`))
	}))
	defer srv.Close()

	provider := New(Config{Name: "aliyun-prod", AccessKeyID: "ak", AccessKeySecret: "sk", BaseURL: srv.URL})
	result, err := provider.EnsureRecord(context.Background(), ingress.Record{
		Type: ingress.RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300,
	})
	if err != nil {
		t.Fatalf("EnsureRecord() error = %v", err)
	}

	assertBool(t, result.Changed, false)
	assertEqual(t, result.Record.ID, "rec-1")
	assertStringSliceEqual(t, actions, []string{"DescribeDomainRecords"})
}

func TestRemoveRecordDeletesByRecordID(t *testing.T) {
	var action string
	var recordID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm() error = %v", err)
		}
		action = r.Form.Get("Action")
		recordID = r.Form.Get("RecordId")
		_, _ = w.Write([]byte(`{"RecordId":"rec-1"}`))
	}))
	defer srv.Close()

	provider := New(Config{Name: "aliyun-prod", AccessKeyID: "ak", AccessKeySecret: "sk", BaseURL: srv.URL})
	if err := provider.RemoveRecord(context.Background(), ingress.Record{ID: "rec-1", Name: "api.example.com"}); err != nil {
		t.Fatalf("RemoveRecord() error = %v", err)
	}
	assertEqual(t, action, "DeleteDomainRecord")
	assertEqual(t, recordID, "rec-1")
}

func TestSplitRecordNameUsesApexRR(t *testing.T) {
	domainName, rr := splitRecordName("example.com")
	assertEqual(t, domainName, "example.com")
	assertEqual(t, rr, "@")
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
