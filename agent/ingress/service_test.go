// service_test.go 验证入口配置子系统的应用层编排。
//
// 职责：
//   - 验证 apply 的 DNS、证书、proxy 顺序和状态持久化
//   - 验证 preview 无持久化副作用
//   - 验证孤儿资源检测和人工确认删除
//
// 边界：
//   - 不访问真实 DNS、ACME 或远端 host
//   - 不测试具体 provider 的协议细节
package ingress

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

func TestServiceApplyOrderAndState(t *testing.T) {
	store := NewFileStore(t.TempDir())
	seedActiveCertificate(t, store, "cert-1")
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a", Name: "gateway"}}, nil
		},
	})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)

	result, err := svc.Apply(context.Background(), in.ID, ApplyOptions{ConfirmedDNSValue: "203.0.113.10"})
	requireNoError(t, err)

	assertStringSliceEqual(t, events, []string{"dns.ensure", "proxy.render", "proxy.apply:host-a"})
	assertLen(t, result.Hosts, 1)
	state, ok, err := store.GetState(in.ID)
	requireNoError(t, err)
	assertBool(t, ok, true)
	assertLen(t, state.Records, 1)
}

func TestServiceApplyEnsuresAllDNSRecordsAndAppliesProxyHosts(t *testing.T) {
	store := NewFileStore(t.TempDir())
	in := validProjectIngress("proj-1", "api.example.com")
	in.Proxy.HostIDs = []string{"edge-a", "edge-b"}
	in.Upstreams = []Upstream{{IP: "10.0.0.12", Port: 8080}}
	in.DNS.Records = []Record{
		{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300},
		{Type: RecordA, Name: "api.example.com", Value: "203.0.113.11", TTL: 300},
	}
	saved, err := store.UpsertIngress(in)
	requireNoError(t, err)

	events := []string{}
	dns := &orderedDNS{name: ProviderManual, events: &events}
	proxy := &orderedProxy{name: ProviderNginx, events: &events}
	registry := NewRegistry()
	registry.RegisterDNS(dns)
	registry.RegisterProxy(proxy)

	service := NewService(ServiceConfig{
		Store:    store,
		Registry: registry,
		HostLookup: func(ids []string) ([]model.Host, error) {
			assertStringSliceEqual(t, ids, []string{"edge-a", "edge-b"})
			return []model.Host{
				{ID: "edge-a", Name: "edge-a"},
				{ID: "edge-b", Name: "edge-b"},
			}, nil
		},
	})

	state, err := service.Apply(context.Background(), saved.ID, ApplyOptions{})
	requireNoError(t, err)
	assertLen(t, dns.ensured, 2)
	assertEqual(t, dns.ensured[0].Value, "203.0.113.10")
	assertEqual(t, dns.ensured[1].Value, "203.0.113.11")
	assertStringSliceEqual(t, events, []string{"dns.ensure", "dns.ensure", "proxy.render", "proxy.apply:edge-a", "proxy.apply:edge-b"})
	assertLen(t, state.Records, 2)
	assertLen(t, state.Hosts, 2)
}

func TestServiceApplyStopsOnDNSFailure(t *testing.T) {
	store := NewFileStore(t.TempDir())
	seedActiveCertificate(t, store, "cert-1")
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events, err: errors.New("dns down")})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a"}}, nil
		},
	})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)

	_, err = svc.Apply(context.Background(), in.ID, ApplyOptions{ConfirmedDNSValue: "203.0.113.10"})
	requireErrorContains(t, err, "dns down")
	assertStringSliceEqual(t, events, []string{"dns.ensure"})
	_, ok, stateErr := store.GetState(in.ID)
	requireNoError(t, stateErr)
	assertBool(t, ok, false)
}

func TestServiceApplyRequiresConfirmedDNSValueForInferredHostIP(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{testTunnelHost("host-a", "203.0.113.10")}, nil
		},
	})
	in := validAutomaticIngress()
	in.TLS = TLSConfig{}
	in.DNS.Records[0].Value = ""
	saved, err := store.UpsertIngress(in)
	requireNoError(t, err)

	_, err = svc.Apply(context.Background(), saved.ID, ApplyOptions{})
	requireErrorContains(t, err, "confirmed_dns_value")
	assertStringSliceEqual(t, events, []string{})
}

func TestServiceApplyUsesReferencedActiveCertificate(t *testing.T) {
	store := NewFileStore(t.TempDir())
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains: []string{"api.example.com"},
		Issuer:  CertificateIssuerACME,
		Status:  CertActive,
		Material: &Certificate{
			Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY", Provider: ProviderACME,
		},
	})
	requireNoError(t, err)
	in := validAutomaticIngress()
	in.TLS = TLSConfig{Enabled: true, CertID: cert.ID}
	saved, err := store.UpsertIngress(in)
	requireNoError(t, err)
	events := []string{}
	reg := NewRegistry()
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a"}}, nil
		},
	})

	state, err := svc.Apply(context.Background(), saved.ID, ApplyOptions{ConfirmedDNSValue: "203.0.113.10"})
	requireNoError(t, err)

	assertStringSliceEqual(t, events, []string{"dns.ensure", "proxy.render", "proxy.apply:host-a"})
	assertLen(t, state.Hosts, 1)
	got, ok, err := store.GetCertificate(cert.ID)
	requireNoError(t, err)
	assertBool(t, ok, true)
	assertLen(t, got.Deployments, 1)
	assertEqual(t, got.Deployments[0].SourceType, "ingress")
	assertEqual(t, got.Deployments[0].SourceID, saved.ID)
	assertEqual(t, got.Deployments[0].PostDeployCommand, "nginx -t && reload nginx")
}

func TestServiceApplyRejectsNonActiveCertificate(t *testing.T) {
	store := NewFileStore(t.TempDir())
	cert, err := store.UpsertCertificate(ManagedCertificate{
		Domains: []string{"api.example.com"},
		Status:  CertPending,
	})
	requireNoError(t, err)
	in := validAutomaticIngress()
	in.TLS = TLSConfig{Enabled: true, CertID: cert.ID}
	saved, err := store.UpsertIngress(in)
	requireNoError(t, err)
	reg := NewRegistry()
	reg.RegisterDNS(&orderedDNS{name: "dns-prod"})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a"}}, nil
		},
	})

	_, err = svc.Apply(context.Background(), saved.ID, ApplyOptions{ConfirmedDNSValue: "203.0.113.10"})

	requireErrorContains(t, err, "证书 "+cert.ID+" 尚未就绪，请先在 SSL 管理页申请")
}

func TestServicePreviewHasNoSideEffects(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: ProviderManual, events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{testTunnelHost("host-a", "203.0.113.10")}, nil
		},
	})
	in := validManualIngress()

	preview, err := svc.Preview(context.Background(), in)
	requireNoError(t, err)

	assertEqual(t, preview.DNSRecord.Value, "203.0.113.10")
	assertContains(t, preview.RenderedConfigByHost["host-a"], "server")
	assertStringSliceEqual(t, events, []string{"dns.ensure", "proxy.render"})
	_, ok, stateErr := store.GetState(in.ID)
	requireNoError(t, stateErr)
	assertBool(t, ok, false)
}

func TestServiceDetectAggregatesProxyAndDNSOrphans(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", records: []Record{
		{ID: "rec-live", Type: RecordA, Name: "api.example.com", Value: "203.0.113.10"},
		{ID: "rec-old", Type: RecordA, Name: "old.example.com", Value: "203.0.113.10"},
	}})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, orphans: []OrphanConfig{
		{HostID: "host-a", Path: "/etc/nginx/conf.d/superdev-old.example.com.conf", Domain: "old.example.com"},
	}})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a"}}, nil
		},
	})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)

	report, err := svc.DetectOrphans(context.Background(), in.ID)
	requireNoError(t, err)

	assertLen(t, report.Configs, 1)
	assertEqual(t, report.Configs[0].Domain, "old.example.com")
	assertLen(t, report.Records, 1)
	assertEqual(t, report.Records[0].ID, "rec-old")
}

func TestServiceRemoveOrphansRequiresExplicitItems(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			return []model.Host{{ID: "host-a"}}, nil
		},
	})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)

	err = svc.RemoveOrphans(context.Background(), in.ID, OrphanReport{
		Configs: []OrphanConfig{{HostID: "host-a", Path: "/etc/nginx/conf.d/superdev-old.example.com.conf", Domain: "old.example.com"}},
		Records: []Record{{ID: "rec-old", Type: RecordA, Name: "old.example.com"}},
	})
	requireNoError(t, err)
	assertStringSliceEqual(t, events, []string{"proxy.remove:old.example.com", "dns.remove:old.example.com"})
}

func TestServiceRemoveOrphansCanUsePreviouslyAppliedHost(t *testing.T) {
	store := NewFileStore(t.TempDir())
	reg := NewRegistry()
	events := []string{}
	reg.RegisterDNS(&orderedDNS{name: "dns-prod", events: &events})
	reg.RegisterProxy(&orderedProxy{name: ProviderNginx, events: &events})
	svc := NewService(ServiceConfig{
		Store:    store,
		Registry: reg,
		HostLookup: func(ids []string) ([]model.Host, error) {
			assertStringSliceEqual(t, ids, []string{"host-a", "old-host"})
			return []model.Host{{ID: "host-a"}, {ID: "old-host"}}, nil
		},
	})
	in, err := store.UpsertIngress(validAutomaticIngress())
	requireNoError(t, err)
	requireNoError(t, store.SaveState(AppliedState{
		IngressID: in.ID,
		Hosts:     []HostState{{HostID: "old-host"}},
	}))

	err = svc.RemoveOrphans(context.Background(), in.ID, OrphanReport{
		Configs: []OrphanConfig{{HostID: "old-host", Path: "/etc/nginx/conf.d/superdev-old.example.com.conf", Domain: "old.example.com"}},
	})
	requireNoError(t, err)
	assertStringSliceEqual(t, events, []string{"proxy.remove:old.example.com"})
}

type orderedDNS struct {
	name    string
	events  *[]string
	err     error
	records []Record
	ensured []Record
}

func (d *orderedDNS) Name() string { return d.name }

func (d *orderedDNS) EnsureRecord(ctx context.Context, record Record) (RecordResult, error) {
	if d.events != nil {
		*d.events = append(*d.events, "dns.ensure")
	}
	if d.err != nil {
		return RecordResult{}, d.err
	}
	d.ensured = append(d.ensured, record)
	return RecordResult{Record: record, Changed: true}, nil
}

func (d *orderedDNS) ListRecords(ctx context.Context, domain string) ([]Record, error) {
	return append([]Record(nil), d.records...), nil
}

func (d *orderedDNS) RemoveRecord(ctx context.Context, record Record) error {
	if d.events != nil {
		*d.events = append(*d.events, "dns.remove:"+record.Name)
	}
	return nil
}

type orderedCert struct {
	name   string
	events *[]string
	err    error
}

func (c *orderedCert) Name() string { return c.name }

func (c *orderedCert) Obtain(ctx context.Context, domains []string, dns DnsProvider) (Certificate, error) {
	if c.events != nil {
		*c.events = append(*c.events, "cert.obtain")
	}
	if c.err != nil {
		return Certificate{}, c.err
	}
	domain := ""
	if len(domains) > 0 {
		domain = domains[0]
	}
	return Certificate{Domain: domain, CertPEM: "CERT", KeyPEM: "KEY", Provider: c.name, ExpiresAt: time.Now().Add(90 * 24 * time.Hour)}, nil
}

func (c *orderedCert) Renew(ctx context.Context, cert Certificate, domains []string, dns DnsProvider) (Certificate, error) {
	if c.events != nil {
		*c.events = append(*c.events, "cert.renew")
	}
	if c.err != nil {
		return Certificate{}, c.err
	}
	cert.CertPEM = "NEWCERT"
	cert.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
	return cert, nil
}

func (c *orderedCert) ExpiresAt(cert Certificate) time.Time { return cert.ExpiresAt }

type orderedProxy struct {
	name    string
	events  *[]string
	orphans []OrphanConfig
}

func (p *orderedProxy) Name() string { return p.name }

func (p *orderedProxy) Render(in Ingress, cert *Certificate) (RenderedConfig, error) {
	if p.events != nil {
		*p.events = append(*p.events, "proxy.render")
	}
	return RenderedConfig{Domain: in.Domain, Filename: in.Domain + ".conf", Content: "server_name " + in.Domain + ";", Certificate: cert}, nil
}

func (p *orderedProxy) Apply(ctx context.Context, host model.Host, cfg RenderedConfig) (HostState, error) {
	if p.events != nil {
		*p.events = append(*p.events, "proxy.apply:"+host.ID)
	}
	state := HostState{HostID: host.ID, ConfigPath: "/etc/nginx/conf.d/superdev-" + cfg.Filename}
	if cfg.Certificate != nil {
		state.CertPaths = []string{
			"/etc/superdev/ingress/certs/" + cfg.Domain + "/fullchain.pem",
			"/etc/superdev/ingress/certs/" + cfg.Domain + "/privkey.pem",
		}
		state.CertDeployment = &CertDeployment{
			HostID:            host.ID,
			CertPath:          state.CertPaths[0],
			KeyPath:           state.CertPaths[1],
			PostDeployCommand: "nginx -t && reload nginx",
		}
	}
	return state, nil
}

func (p *orderedProxy) DeployCertificate(ctx context.Context, host model.Host, domain string, cert Certificate) (CertDeployment, error) {
	if p.events != nil {
		*p.events = append(*p.events, "proxy.deploy-cert:"+host.ID)
	}
	return CertDeployment{
		HostID:     host.ID,
		CertPath:   "/etc/superdev/ingress/certs/" + domain + "/fullchain.pem",
		KeyPath:    "/etc/superdev/ingress/certs/" + domain + "/privkey.pem",
		DeployedAt: time.Now().UTC(),
	}, nil
}

func (p *orderedProxy) Detect(ctx context.Context, host model.Host, declared []Ingress) ([]OrphanConfig, error) {
	return append([]OrphanConfig(nil), p.orphans...), nil
}

func (p *orderedProxy) Remove(ctx context.Context, host model.Host, orphan OrphanConfig) error {
	if p.events != nil {
		*p.events = append(*p.events, "proxy.remove:"+orphan.Domain)
	}
	return nil
}

func validAutomaticIngress() Ingress {
	return Ingress{
		ID:           "ing-1",
		ProjectID:    "proj-1",
		Domain:       "api.example.com",
		Proxy:        ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"host-a"}},
		Upstreams:    []Upstream{{IP: "127.0.0.1", Port: 8080}},
		ProxyOptions: ProxyOptions{RawTemplate: "server { server_name api.example.com; }"},
		TLS:          TLSConfig{Enabled: true, CertID: "cert-1"},
		DNS: DNSConfig{
			Provider: "dns-prod",
			Records:  []Record{{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10"}},
		},
	}
}

func seedActiveCertificate(t *testing.T, store Store, certID string) {
	t.Helper()
	_, err := store.UpsertCertificate(ManagedCertificate{
		ID:      certID,
		Domains: []string{"api.example.com"},
		Issuer:  CertificateIssuerACME,
		Status:  CertActive,
		Material: &Certificate{
			Domain: "api.example.com", CertPEM: "CERT", KeyPEM: "KEY", Provider: ProviderACME,
		},
	})
	requireNoError(t, err)
}

func validManualIngress() Ingress {
	return Ingress{
		ID:           "ing-manual",
		ProjectID:    "proj-1",
		Domain:       "api.example.com",
		Proxy:        ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"host-a"}},
		Upstreams:    []Upstream{{IP: "127.0.0.1", Port: 8080}},
		ProxyOptions: ProxyOptions{RawTemplate: "server { server_name api.example.com; }"},
		DNS: DNSConfig{
			Provider: ProviderManual,
			Records:  []Record{{Type: RecordA, Name: "api.example.com"}},
		},
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want containing %q", err.Error(), want)
	}
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

func assertLen[T any](t *testing.T, got []T, want int) {
	t.Helper()
	if len(got) != want {
		t.Fatalf("len = %d, want %d: %#v", len(got), want, got)
	}
}

func assertContains(t *testing.T, got string, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%q does not contain %q", got, want)
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
