// certmanager_test.go 验证托管证书续期巡检。
//
// 职责：
//   - 验证即将过期的证书会续期并重新分发到 host
//   - 验证未到续期窗口的证书会跳过
//
// 边界：
//   - 不访问真实 ACME CA
//   - 不访问真实远端 host
package ingress

import (
	"context"
	"testing"
	"time"

	"github.com/superdev/agent/model"
)

func TestCertManagerRenewsManagedCertificateAndRedeploys(t *testing.T) {
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
	service := NewCertService(CertServiceConfig{
		Store: store, Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "edge-a"}}, nil
		},
	})
	manager := NewCertManager(CertManagerConfig{
		Store:       store,
		CertService: service,
		RenewBefore: 30 * 24 * time.Hour,
	})

	err = manager.RunOnce(context.Background(), time.Now())
	requireNoError(t, err)

	assertStringSliceContains(t, events, "cert.renew")
	assertStringSliceContains(t, events, "proxy.deploy-cert:edge-a")
	got, ok, err := store.GetCertificate(cert.ID)
	requireNoError(t, err)
	assertBool(t, ok, true)
	assertEqual(t, got.Material.CertPEM, "NEWCERT")
}

func TestCertManagerSkipsInactiveManualAndFreshCertificates(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "cloudflare-prod", events: &events})
	reg.RegisterCert(&orderedCert{name: ProviderACME, events: &events})
	service := NewCertService(CertServiceConfig{Store: store, Registry: reg})
	_, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"failed.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertFailed,
		AutoRenew:   true,
	})
	requireNoError(t, err)
	_, err = store.UpsertCertificate(ManagedCertificate{
		Domains:   []string{"manual.example.com"},
		Issuer:    CertificateIssuerManual,
		Status:    CertActive,
		AutoRenew: true,
		Material:  &Certificate{ExpiresAt: time.Now().Add(10 * 24 * time.Hour)},
	})
	requireNoError(t, err)
	_, err = store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"fresh.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertActive,
		AutoRenew:   true,
		Material:    &Certificate{ExpiresAt: time.Now().Add(60 * 24 * time.Hour)},
	})
	requireNoError(t, err)

	manager := NewCertManager(CertManagerConfig{
		Store:       store,
		CertService: service,
		RenewBefore: 30 * 24 * time.Hour,
	})

	requireNoError(t, manager.RunOnce(context.Background(), time.Now()))
	assertStringSliceEqual(t, events, []string{})
}
