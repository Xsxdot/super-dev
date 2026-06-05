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

	"github.com/xsxdot/super-dev/agent/model"
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
	events := []string{}
	deployer := &recordingCertDeployer{events: &events}
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
		Deployer: deployer,
		HostLookup: func(ids []string) ([]model.Host, error) {
			assertStringSliceEqual(t, ids, []string{"edge-a"})
			return []model.Host{{ID: "edge-a", Name: "edge-a"}}, nil
		},
	})

	got, err := svc.Deploy(context.Background(), cert.ID, []CertificateDeploymentRequest{{
		HostID:            "edge-a",
		CertPath:          "/opt/certs/api/fullchain.pem",
		KeyPath:           "/opt/certs/api/privkey.pem",
		PostDeployCommand: "service caddy reload",
	}})
	requireNoError(t, err)

	assertStringSliceContains(t, events, "cert.deploy:edge-a")
	assertLen(t, got.Deployments, 1)
	assertEqual(t, got.Deployments[0].HostID, "edge-a")
	assertEqual(t, got.Deployments[0].CertPath, "/opt/certs/api/fullchain.pem")
	assertEqual(t, got.Deployments[0].KeyPath, "/opt/certs/api/privkey.pem")
	assertEqual(t, got.Deployments[0].PostDeployCommand, "service caddy reload")
	assertEqual(t, deployer.requests[0].PostDeployCommand, "service caddy reload")
}

func TestCertServiceRenewRedeploysExistingDeployments(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	deployer := &recordingCertDeployer{events: &events}
	reg.RegisterDNS(&orderedDNS{name: "cloudflare-prod", events: &events})
	reg.RegisterCert(&orderedCert{name: ProviderACME, events: &events})
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"api.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertActive,
		Material:    &Certificate{Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY", Provider: ProviderACME, ExpiresAt: time.Now().Add(10 * 24 * time.Hour)},
		Deployments: []CertDeployment{{HostID: "edge-a", CertPath: "/cert", KeyPath: "/key", PostDeployCommand: "service caddy reload"}},
		AutoRenew:   true,
	})
	requireNoError(t, err)
	svc := NewCertService(CertServiceConfig{
		Store:    store,
		Registry: reg,
		Deployer: deployer,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "edge-a", Name: "edge-a"}}, nil
		},
	})

	got, err := svc.Renew(context.Background(), cert.ID)
	requireNoError(t, err)

	assertEqual(t, got.Material.CertPEM, "NEWCERT")
	assertStringSliceContains(t, events, "cert.renew")
	assertStringSliceContains(t, events, "cert.deploy:edge-a")
	assertEqual(t, deployer.requests[0].CertPath, "/cert")
	assertEqual(t, deployer.requests[0].KeyPath, "/key")
	assertEqual(t, deployer.requests[0].PostDeployCommand, "service caddy reload")
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

type recordingCertDeployer struct {
	events   *[]string
	requests []CertificateDeploymentRequest
}

func (d *recordingCertDeployer) DeployCertificate(ctx context.Context, host model.Host, cert Certificate, request CertificateDeploymentRequest) (CertDeployment, error) {
	if d.events != nil {
		*d.events = append(*d.events, "cert.deploy:"+host.ID)
	}
	d.requests = append(d.requests, request)
	return CertDeployment{
		HostID:            host.ID,
		CertPath:          request.CertPath,
		KeyPath:           request.KeyPath,
		PostDeployCommand: request.PostDeployCommand,
		DeployedAt:        time.Now().UTC(),
	}, nil
}
