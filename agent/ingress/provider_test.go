// provider_test.go 验证 Ingress provider 注册表和接口边界。
//
// 职责：
//   - 验证 proxy、DNS、cert provider 可按名称解析
//   - 验证缺失 provider 返回明确错误
//
// 边界：
//   - 不调用真实 provider
package ingress

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

type fakeProxyProvider struct{ name string }

func (f fakeProxyProvider) Name() string { return f.name }

func (f fakeProxyProvider) Render(in Ingress, cert *Certificate) (RenderedConfig, error) {
	return RenderedConfig{Domain: in.Domain, Content: "server {}"}, nil
}

func (f fakeProxyProvider) Apply(ctx context.Context, host model.Host, cfg RenderedConfig) (HostState, error) {
	return HostState{HostID: host.ID, ConfigPath: "/etc/nginx/conf.d/" + cfg.Filename}, nil
}

func (f fakeProxyProvider) Detect(ctx context.Context, host model.Host, declared []Ingress) ([]OrphanConfig, error) {
	return nil, nil
}

func (f fakeProxyProvider) Remove(ctx context.Context, host model.Host, orphan OrphanConfig) error {
	return nil
}

type fakeDNSProvider struct{ name string }

func (f fakeDNSProvider) Name() string { return f.name }

func (f fakeDNSProvider) EnsureRecord(ctx context.Context, record Record) (RecordResult, error) {
	return RecordResult{Record: record, Changed: true}, nil
}

func (f fakeDNSProvider) ListRecords(ctx context.Context, domain string) ([]Record, error) {
	return nil, nil
}

func (f fakeDNSProvider) RemoveRecord(ctx context.Context, record Record) error {
	return nil
}

type fakeCertProvider struct{ name string }

func (f fakeCertProvider) Name() string { return f.name }

func (f fakeCertProvider) Obtain(ctx context.Context, domains []string, dns DnsProvider) (Certificate, error) {
	domain := ""
	if len(domains) > 0 {
		domain = domains[0]
	}
	return Certificate{Domain: domain, Provider: f.name, ExpiresAt: time.Now().Add(90 * 24 * time.Hour)}, nil
}

func (f fakeCertProvider) Renew(ctx context.Context, cert Certificate, domains []string, dns DnsProvider) (Certificate, error) {
	cert.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
	return cert, nil
}

func (f fakeCertProvider) ExpiresAt(cert Certificate) time.Time { return cert.ExpiresAt }

func TestProviderRegistryResolvesRegisteredProviders(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterProxy(fakeProxyProvider{name: ProviderNginx})
	reg.RegisterDNS(fakeDNSProvider{name: "cloudflare-prod"})
	reg.RegisterCert(fakeCertProvider{name: ProviderACME})

	proxy, err := reg.Proxy(ProviderNginx)
	require.NoError(t, err)
	assert.Equal(t, ProviderNginx, proxy.Name())

	dns, err := reg.DNS("cloudflare-prod")
	require.NoError(t, err)
	assert.Equal(t, "cloudflare-prod", dns.Name())

	cert, err := reg.Cert(ProviderACME)
	require.NoError(t, err)
	assert.Equal(t, ProviderACME, cert.Name())
}

func TestProviderRegistryReturnsMissingProviderErrors(t *testing.T) {
	reg := NewRegistry()
	_, err := reg.Proxy("missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrProviderNotFound))
	assert.Contains(t, err.Error(), "proxy provider missing not found")
}
