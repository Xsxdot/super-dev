// Package ingress 验证入口配置声明的纯模型语义。
//
// 职责：
//   - 验证 Ingress JSON 字段稳定
//   - 验证声明校验规则
//   - 验证 DNS A 记录目标推断规则
//
// 边界：
//   - 不访问 DNS provider
//   - 不渲染 nginx 配置
package ingress

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestProjectIngressJSONShape(t *testing.T) {
	in := Ingress{
		ID:        "ing-1",
		ProjectID: "proj-1",
		Name:      "api",
		Domain:    "api.example.com",
		SourceHint: SourceHint{
			EnvName:    "prod",
			PipelineID: "deploy-prod",
			Role:       "api_targets",
		},
		Proxy: ProxyConfig{
			Provider: ProviderNginx,
			HostIDs:  []string{"edge-a", "edge-b"},
		},
		Upstreams: []Upstream{{
			HostID: "app-a",
			IP:     "10.0.0.12",
			Port:   8080,
		}},
		ProxyOptions: ProxyOptions{
			Websocket:    true,
			ProxyTimeout: Duration{Duration: 60 * time.Second},
			RawTemplate:  "server { server_name api.example.com; }",
		},
		TLS: TLSConfig{Enabled: true, CertID: "cert-1"},
		DNS: DNSConfig{
			Provider: "cloudflare-prod",
			Records: []Record{
				{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300},
				{Type: RecordA, Name: "api.example.com", Value: "203.0.113.11", TTL: 300},
			},
		},
	}

	data, err := json.Marshal(in)
	require.NoError(t, err)
	var got Ingress
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "proj-1", got.ProjectID)
	assert.Equal(t, []string{"edge-a", "edge-b"}, got.Proxy.HostIDs)
	require.Len(t, got.Upstreams, 1)
	assert.Equal(t, "10.0.0.12", got.Upstreams[0].IP)
	assert.Equal(t, "cert-1", got.TLS.CertID)
	require.Len(t, got.DNS.Records, 2)
	assert.Equal(t, "203.0.113.11", got.DNS.Records[1].Value)
}

func TestManagedCertificateJSONShape(t *testing.T) {
	now := time.Date(2026, 6, 3, 10, 0, 0, 0, time.UTC)
	cert := ManagedCertificate{
		ID:          "cert-1",
		Domains:     []string{"*.example.com", "api.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertActive,
		Material: &Certificate{
			Domain:     "*.example.com",
			CertPEM:    "CERT",
			KeyPEM:     "KEY",
			Issuer:     string(CertificateIssuerACME),
			ObtainedAt: now,
			ExpiresAt:  now.Add(90 * 24 * time.Hour),
			Provider:   ProviderACME,
		},
		Deployments: []CertDeployment{{
			HostID:     "edge-a",
			CertPath:   "/etc/superdev/ingress/certs/api.example.com/fullchain.pem",
			KeyPath:    "/etc/superdev/ingress/certs/api.example.com/privkey.pem",
			DeployedAt: now,
		}},
		AutoRenew: true,
		CreatedAt: now,
		UpdatedAt: now,
	}

	raw, err := json.Marshal(cert)
	require.NoError(t, err)
	var got ManagedCertificate
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, []string{"*.example.com", "api.example.com"}, got.Domains)
	require.Equal(t, CertActive, got.Status)
	require.True(t, got.AutoRenew)
	require.Equal(t, "edge-a", got.Deployments[0].HostID)
}

func TestMatchCertificateExactPreferredOverWildcard(t *testing.T) {
	wildcard := ManagedCertificate{ID: "wildcard", Domains: []string{"*.example.com"}, Status: CertActive}
	exact := ManagedCertificate{ID: "exact", Domains: []string{"api.example.com"}, Status: CertActive}

	got, ok := MatchCertificate("api.example.com", []ManagedCertificate{wildcard, exact})

	require.True(t, ok)
	require.Equal(t, "exact", got.ID)
}

func TestMatchCertificateWildcardDoesNotMatchBareDomain(t *testing.T) {
	_, ok := MatchCertificate("example.com", []ManagedCertificate{{
		ID: "wildcard", Domains: []string{"*.example.com"}, Status: CertActive,
	}})
	require.False(t, ok)
}

func TestMatchCertificateMatchesSingleLevelWildcardAndMultiSAN(t *testing.T) {
	cert, ok := MatchCertificate("api.example.com", []ManagedCertificate{{
		ID: "multi", Domains: []string{"admin.foo.com", "*.example.com"}, Status: CertActive,
	}})
	require.True(t, ok)
	require.Equal(t, "multi", cert.ID)

	_, ok = MatchCertificate("v1.api.example.com", []ManagedCertificate{{
		ID: "wildcard", Domains: []string{"*.example.com"}, Status: CertActive,
	}})
	require.False(t, ok)
}

func TestIngressValidateRequiresProjectProxyUpstreamDNSAndTemplate(t *testing.T) {
	err := (Ingress{}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "project_id is required")

	err = Ingress{
		ProjectID: "proj-1",
		Domain:    "api.example.com",
		Proxy:     ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"edge-a"}},
		DNS:       DNSConfig{Provider: ProviderManual},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one upstream is required")

	err = Ingress{
		ProjectID:    "proj-1",
		Domain:       "api.example.com",
		Proxy:        ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"edge-a"}},
		Upstreams:    []Upstream{{IP: "10.0.0.12", Port: 8080}},
		DNS:          DNSConfig{Provider: ProviderManual, Records: []Record{{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10"}}},
		ProxyOptions: ProxyOptions{},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raw_template is required")
}

func TestIngressValidateRequiresCertIDWhenTLSEnabled(t *testing.T) {
	err := Ingress{
		ProjectID: "proj-1",
		Domain:    "api.example.com",
		Proxy:     ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"edge-a"}},
		Upstreams: []Upstream{{IP: "10.0.0.12", Port: 8080}},
		ProxyOptions: ProxyOptions{
			RawTemplate: "server { server_name api.example.com; }",
		},
		TLS: TLSConfig{Enabled: true},
		DNS: DNSConfig{
			Provider: ProviderManual,
			Records:  []Record{{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10"}},
		},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tls.cert_id is required")
}

func TestResolveDNSRecordValueRequiresExplicitValueForMultipleHosts(t *testing.T) {
	in := Ingress{Domain: "api.example.com", Proxy: ProxyConfig{HostIDs: []string{"host-a", "host-b"}}}
	decision := ResolveDNSRecordValue(in, []model.Host{
		testTunnelHost("host-a", "203.0.113.10"),
		testTunnelHost("host-b", "203.0.113.11"),
	})
	assert.False(t, decision.OK)
	assert.True(t, decision.RequiresInput)
	assert.Equal(t, "dns.record.value is required for multiple hosts", decision.Message)
}

func TestResolveDNSRecordValueInfersSinglePublicIPForPreview(t *testing.T) {
	in := Ingress{Domain: "api.example.com", Proxy: ProxyConfig{HostIDs: []string{"host-a"}}}
	decision := ResolveDNSRecordValue(in, []model.Host{testTunnelHost("host-a", "203.0.113.10")})
	assert.True(t, decision.OK)
	assert.True(t, decision.RequiresConfirmation)
	assert.Equal(t, "203.0.113.10", decision.Value)
}

func TestResolveDNSRecordValueAcceptsExplicitRecordValue(t *testing.T) {
	in := Ingress{
		Domain: "api.example.com",
		Proxy:  ProxyConfig{HostIDs: []string{"host-a", "host-b"}},
		DNS:    DNSConfig{Records: []Record{{Value: "198.51.100.8"}}},
	}
	decision := ResolveDNSRecordValue(in, nil)
	assert.True(t, decision.OK)
	assert.False(t, decision.RequiresConfirmation)
	assert.Equal(t, "198.51.100.8", decision.Value)
}
