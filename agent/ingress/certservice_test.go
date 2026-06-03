// certservice_test.go 验证全局托管证书服务的申请、续期、部署和匹配。
//
// 职责：
//   - 验证 CertService 状态流转和持久化
//   - 验证续期后会重新部署到已记录 host
//   - 验证匹配只返回 active 证书
//
// 边界：
//   - 不访问真实 ACME、DNS 或远端 host
//   - 不测试具体 nginx 命令内容
package ingress

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/superdev/agent/model"
)

func TestCertServiceIssueSuccessMarksActive(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	reg.RegisterDNS(&orderedDNS{name: "cloudflare-prod"})
	reg.RegisterCert(&orderedCert{name: ProviderACME})
	svc := NewCertService(CertServiceConfig{Store: store, Registry: reg})
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"api.example.com", "www.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertPending,
		AutoRenew:   true,
	})
	requireNoError(t, err)

	got, err := svc.Issue(context.Background(), cert.ID)
	requireNoError(t, err)

	assertEqual(t, got.Status, CertActive)
	assertEqual(t, got.LastError, "")
	if got.Material == nil {
		t.Fatal("Material = nil, want certificate")
	}
	assertEqual(t, got.Material.Domain, "api.example.com")
}

func TestCertServiceIssueFailurePersistsFailedStatus(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	reg.RegisterDNS(&orderedDNS{name: "cloudflare-prod"})
	reg.RegisterCert(&orderedCert{name: ProviderACME, err: errors.New("acme rejected account")})
	svc := NewCertService(CertServiceConfig{Store: store, Registry: reg})
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"api.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertPending,
	})
	requireNoError(t, err)

	got, err := svc.Issue(context.Background(), cert.ID)
	requireErrorContains(t, err, "acme rejected account")
	assertEqual(t, got.Status, CertFailed)
	assertEqual(t, got.LastError, "acme rejected account")
}

func TestCertServiceDeployRecordsHostPaths(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains: []string{"api.example.com"},
		Issuer:  CertificateIssuerManual,
		Status:  CertActive,
		Material: &Certificate{
			Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY", Provider: ProviderACME,
		},
	})
	requireNoError(t, err)
	svc := NewCertService(CertServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			assertStringSliceEqual(t, ids, []string{"edge-a"})
			return []model.Host{{ID: "edge-a", Name: "edge-a"}}, nil
		},
	})

	got, err := svc.Deploy(context.Background(), cert.ID, []string{"edge-a"})
	requireNoError(t, err)

	assertStringSliceContains(t, events, "proxy.deploy-cert:edge-a")
	assertLen(t, got.Deployments, 1)
	assertEqual(t, got.Deployments[0].HostID, "edge-a")
}

func TestCertServiceRenewRedeploysExistingDeployments(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "cloudflare-prod", events: &events})
	reg.RegisterCert(&orderedCert{name: ProviderACME, events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"api.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertActive,
		Material:    &Certificate{Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY", Provider: ProviderACME, ExpiresAt: time.Now().Add(10 * 24 * time.Hour)},
		Deployments: []CertDeployment{{HostID: "edge-a", CertPath: "/cert", KeyPath: "/key"}},
		AutoRenew:   true,
	})
	requireNoError(t, err)
	svc := NewCertService(CertServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "edge-a", Name: "edge-a"}}, nil
		},
	})

	got, err := svc.Renew(context.Background(), cert.ID)
	requireNoError(t, err)

	assertEqual(t, got.Material.CertPEM, "NEWCERT")
	assertStringSliceContains(t, events, "cert.renew")
	assertStringSliceContains(t, events, "proxy.deploy-cert:edge-a")
}

func TestCertServiceMatchOnlyReturnsActiveCertificate(t *testing.T) {
	store := NewFileStore(t.TempDir())
	_, err := store.UpsertCertificate(ManagedCertificate{Domains: []string{"api.example.com"}, Status: CertFailed})
	requireNoError(t, err)
	active, err := store.UpsertCertificate(ManagedCertificate{Domains: []string{"*.example.com"}, Status: CertActive})
	requireNoError(t, err)
	svc := NewCertService(CertServiceConfig{Store: store})

	got, ok, err := svc.Match("api.example.com")
	requireNoError(t, err)
	assertBool(t, ok, true)
	assertEqual(t, got.ID, active.ID)
}

func assertStringSliceContains(t *testing.T, got []string, want string) {
	t.Helper()
	for _, item := range got {
		if item == want {
			return
		}
	}
	t.Fatalf("got %#v, want item %q", got, want)
}
