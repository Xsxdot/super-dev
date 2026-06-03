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
	"github.com/superdev/agent/model"
)

func TestIngressJSONShape(t *testing.T) {
	in := Ingress{
		ID:            "ing-1",
		ProjectID:     "proj-1",
		Name:          "api",
		Domain:        "api.example.com",
		HostIDs:       []string{"host-a", "host-b"},
		Backend:       "127.0.0.1:8080",
		ProxyProvider: ProviderNginx,
		ProxyOptions: ProxyOptions{
			Websocket:    true,
			ProxyTimeout: Duration{Duration: 60 * time.Second},
			ExtraLocations: []LocationOption{{
				Path: "/metrics",
				Raw:  "return 404;",
			}},
		},
		TLS: TLSConfig{Enabled: true, CertProvider: ProviderACME},
		DNS: DNSConfig{
			Provider: "cloudflare-prod",
			Record:   Record{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300},
		},
	}

	data, err := json.Marshal(in)
	require.NoError(t, err)
	var got Ingress
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, "api.example.com", got.Domain)
	assert.Equal(t, []string{"host-a", "host-b"}, got.HostIDs)
	assert.Equal(t, ProviderNginx, got.ProxyProvider)
	assert.True(t, got.ProxyOptions.Websocket)
	assert.Equal(t, 60*time.Second, got.ProxyOptions.ProxyTimeout.Duration)
	assert.True(t, got.TLS.Enabled)
	assert.Equal(t, ProviderACME, got.TLS.CertProvider)
	assert.Equal(t, RecordA, got.DNS.Record.Type)
}

func TestIngressValidateRejectsMissingRequiredFields(t *testing.T) {
	err := (Ingress{}).Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "domain is required")

	err = Ingress{
		Domain:        "api.example.com",
		Backend:       "127.0.0.1:8080",
		ProxyProvider: ProviderNginx,
		DNS:           DNSConfig{Provider: ProviderManual},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one host is required")
}

func TestIngressValidateRejectsManualDNSWithAutomaticACME(t *testing.T) {
	err := Ingress{
		Domain:        "api.example.com",
		HostIDs:       []string{"host-a"},
		Backend:       "127.0.0.1:8080",
		ProxyProvider: ProviderNginx,
		TLS:           TLSConfig{Enabled: true, CertProvider: ProviderACME},
		DNS:           DNSConfig{Provider: ProviderManual},
	}.Validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual DNS cannot automate ACME DNS-01")
}

func TestResolveDNSRecordValueRequiresExplicitValueForMultipleHosts(t *testing.T) {
	in := Ingress{Domain: "api.example.com", HostIDs: []string{"host-a", "host-b"}}
	decision := ResolveDNSRecordValue(in, []model.Host{
		{ID: "host-a", SSHHost: "203.0.113.10"},
		{ID: "host-b", SSHHost: "203.0.113.11"},
	})
	assert.False(t, decision.OK)
	assert.True(t, decision.RequiresInput)
	assert.Equal(t, "dns.record.value is required for multiple hosts", decision.Message)
}

func TestResolveDNSRecordValueInfersSinglePublicIPForPreview(t *testing.T) {
	in := Ingress{Domain: "api.example.com", HostIDs: []string{"host-a"}}
	decision := ResolveDNSRecordValue(in, []model.Host{{ID: "host-a", SSHHost: "203.0.113.10"}})
	assert.True(t, decision.OK)
	assert.True(t, decision.RequiresConfirmation)
	assert.Equal(t, "203.0.113.10", decision.Value)
}

func TestResolveDNSRecordValueAcceptsExplicitRecordValue(t *testing.T) {
	in := Ingress{
		Domain:  "api.example.com",
		HostIDs: []string{"host-a", "host-b"},
		DNS:     DNSConfig{Record: Record{Value: "198.51.100.8"}},
	}
	decision := ResolveDNSRecordValue(in, nil)
	assert.True(t, decision.OK)
	assert.False(t, decision.RequiresConfirmation)
	assert.Equal(t, "198.51.100.8", decision.Value)
}
