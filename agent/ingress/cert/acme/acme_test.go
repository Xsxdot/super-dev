// acme_test.go 验证 ACME DNS-01 provider 对 DnsProvider 的回调语义。
//
// 职责：
//   - 验证首次申请写入并清理 _acme-challenge TXT
//   - 验证清理 TXT 时复用创建结果中的记录 ID
//   - 验证续期返回新的证书过期时间
//
// 边界：
//   - 不访问真实 ACME CA
//   - 不访问真实 DNS provider
package acme

import (
	"context"
	"testing"
	"time"

	"github.com/superdev/agent/ingress"
)

type recordingDNS struct {
	ensured []ingress.Record
	removed []ingress.Record
}

func (r *recordingDNS) Name() string { return "dns-test" }

func (r *recordingDNS) EnsureRecord(ctx context.Context, record ingress.Record) (ingress.RecordResult, error) {
	record.ID = "txt-1"
	r.ensured = append(r.ensured, record)
	return ingress.RecordResult{Record: record, Changed: true}, nil
}

func (r *recordingDNS) ListRecords(ctx context.Context, domain string) ([]ingress.Record, error) {
	return nil, nil
}

func (r *recordingDNS) RemoveRecord(ctx context.Context, record ingress.Record) error {
	r.removed = append(r.removed, record)
	return nil
}

type fakeClient struct {
	obtainedDomains []string
	renewedDomains  []string
}

func (f *fakeClient) Obtain(ctx context.Context, domains []string, present func(string, string) error, cleanup func(string, string) error) (ingress.Certificate, error) {
	t := testingContext(ctx)
	f.obtainedDomains = append([]string(nil), domains...)
	domain := domains[0]
	if err := present("_acme-challenge."+domain, "token-value"); err != nil {
		t.Fatalf("present() error = %v", err)
	}
	if err := cleanup("_acme-challenge."+domain, "token-value"); err != nil {
		t.Fatalf("cleanup() error = %v", err)
	}
	return ingress.Certificate{
		Domain:    domain,
		CertPEM:   "CERT",
		KeyPEM:    "KEY",
		Provider:  ingress.ProviderACME,
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour),
	}, nil
}

func (f *fakeClient) Renew(ctx context.Context, cert ingress.Certificate, domains []string, present func(string, string) error, cleanup func(string, string) error) (ingress.Certificate, error) {
	f.renewedDomains = append([]string(nil), domains...)
	cert.CertPEM = "NEWCERT"
	cert.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
	return cert, nil
}

func TestObtainWritesAndRemovesChallengeTXT(t *testing.T) {
	ctx := context.WithValue(context.Background(), testKey{}, t)
	dns := &recordingDNS{}
	provider := NewWithClient(&fakeClient{})

	cert, err := provider.Obtain(ctx, []string{"api.example.com"}, dns)
	if err != nil {
		t.Fatalf("Obtain() error = %v", err)
	}

	assertEqual(t, cert.CertPEM, "CERT")
	if len(dns.ensured) != 1 {
		t.Fatalf("len(ensured) = %d, want 1", len(dns.ensured))
	}
	assertEqual(t, dns.ensured[0].Type, ingress.RecordTXT)
	assertEqual(t, dns.ensured[0].Name, "_acme-challenge.api.example.com")
	assertEqual(t, dns.ensured[0].Value, "token-value")
	if len(dns.removed) != 1 {
		t.Fatalf("len(removed) = %d, want 1", len(dns.removed))
	}
	assertEqual(t, dns.removed[0].ID, "txt-1")
	assertEqual(t, dns.removed[0].Name, "_acme-challenge.api.example.com")
}

func TestRenewReturnsUpdatedCertificate(t *testing.T) {
	ctx := context.WithValue(context.Background(), testKey{}, t)
	provider := NewWithClient(&fakeClient{})
	oldExpiry := time.Now().Add(10 * 24 * time.Hour)

	cert, err := provider.Renew(ctx, ingress.Certificate{
		Domain:    "api.example.com",
		CertPEM:   "CERT",
		KeyPEM:    "KEY",
		Provider:  ingress.ProviderACME,
		ExpiresAt: oldExpiry,
	}, []string{"api.example.com"}, &recordingDNS{})
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}

	assertEqual(t, cert.CertPEM, "NEWCERT")
	if !provider.ExpiresAt(cert).After(oldExpiry) {
		t.Fatalf("ExpiresAt() = %s, want after %s", provider.ExpiresAt(cert), oldExpiry)
	}
}

func TestObtainPassesAllDomains(t *testing.T) {
	ctx := context.WithValue(context.Background(), testKey{}, t)
	client := &fakeClient{}
	provider := NewWithClient(client)

	_, err := provider.Obtain(ctx, []string{"api.example.com", "www.example.com"}, &recordingDNS{})
	if err != nil {
		t.Fatalf("Obtain() error = %v", err)
	}
	assertStringSliceEqual(t, client.obtainedDomains, []string{"api.example.com", "www.example.com"})
}

func TestRenewPassesAllDomains(t *testing.T) {
	ctx := context.WithValue(context.Background(), testKey{}, t)
	client := &fakeClient{}
	provider := NewWithClient(client)

	_, err := provider.Renew(ctx, ingress.Certificate{Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY"}, []string{"api.example.com", "www.example.com"}, &recordingDNS{})
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	assertStringSliceEqual(t, client.renewedDomains, []string{"api.example.com", "www.example.com"})
}

type testKey struct{}

func testingContext(ctx context.Context) *testing.T {
	t, _ := ctx.Value(testKey{}).(*testing.T)
	return t
}

func assertEqual[T comparable](t *testing.T, got T, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
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
