// store_test.go 验证 Ingress 本机 JSON 存储。
//
// 职责：
//   - 验证入口声明和落地状态读写
//   - 验证 DNS provider 凭据密文字段不会从列表接口泄漏
//   - 验证 provider 文件使用 0600 权限
//
// 边界：
//   - 不访问真实 DNS 服务
//   - 不执行收敛流程
package ingress

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileStoreIngressCRUD(t *testing.T) {
	store := NewFileStore(t.TempDir())
	saved, err := store.UpsertIngress(validProjectIngress("proj-a", "api.example.com"))
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)

	list, err := store.ListIngress()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, saved.ID, list[0].ID)

	got, ok, err := store.GetIngress(saved.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "api.example.com", got.Domain)

	require.NoError(t, store.DeleteIngress(saved.ID))
	list, err = store.ListIngress()
	require.NoError(t, err)
	assert.Empty(t, list)
}

func TestFileStoreListIngressByProject(t *testing.T) {
	store := NewFileStore(t.TempDir())
	_, err := store.UpsertIngress(validProjectIngress("proj-a", "api-a.example.com"))
	require.NoError(t, err)
	_, err = store.UpsertIngress(validProjectIngress("proj-b", "api-b.example.com"))
	require.NoError(t, err)

	items, err := store.ListIngressByProject("proj-a")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "proj-a", items[0].ProjectID)
}

func TestFileStoreAppliedState(t *testing.T) {
	store := NewFileStore(t.TempDir())
	state := AppliedState{
		IngressID: "ing-1",
		Records:   []Record{{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10"}},
	}
	require.NoError(t, store.SaveState(state))

	got, ok, err := store.GetState("ing-1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, got.Records, 1)
	assert.Equal(t, "203.0.113.10", got.Records[0].Value)
}

func TestFileStoreProviderSecretsRedactedFromList(t *testing.T) {
	dir := t.TempDir()
	store := NewFileStore(dir)
	provider, err := store.UpsertDNSProvider(DNSProviderConfig{
		ID:     "cloudflare-prod",
		Name:   "Cloudflare Prod",
		Type:   "cloudflare",
		ZoneID: "zone-1",
		Secrets: map[string]string{
			"api_token": "secret-token",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "secret-token", provider.Secrets["api_token"])

	list, err := store.ListDNSProviders()
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Empty(t, list[0].Secrets)

	stat, err := os.Stat(filepath.Join(dir, "ingress-providers.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), stat.Mode().Perm())
}

func TestFileStoreCertificateCRUDAndRedaction(t *testing.T) {
	store := NewFileStore(t.TempDir())
	saved, err := store.UpsertCertificate(ManagedCertificate{
		Domains:     []string{"api.example.com"},
		Issuer:      CertificateIssuerACME,
		DNSProvider: "cloudflare-prod",
		Status:      CertActive,
		Material: &Certificate{
			Domain:    "api.example.com",
			CertPEM:   "CERT",
			KeyPEM:    "SECRET-KEY",
			Provider:  ProviderACME,
			ExpiresAt: time.Now().Add(90 * 24 * time.Hour),
		},
		AutoRenew: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, saved.ID)

	list, err := store.ListCertificates()
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "CERT", list[0].Material.CertPEM)
	require.Empty(t, list[0].Material.KeyPEM)

	full, ok, err := store.GetCertificate(saved.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "SECRET-KEY", full.Material.KeyPEM)

	require.NoError(t, store.DeleteCertificate(saved.ID))
	list, err = store.ListCertificates()
	require.NoError(t, err)
	require.Empty(t, list)
}

func TestFileStoreACMEAccountSingleton(t *testing.T) {
	store := NewFileStore(t.TempDir())
	initial, err := store.GetACMEAccount()
	require.NoError(t, err)
	require.Empty(t, initial.Email)

	require.NoError(t, store.SaveACMEAccount(ACMEAccount{
		Email:        "ops@example.com",
		DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
	}))
	got, err := store.GetACMEAccount()
	require.NoError(t, err)
	require.Equal(t, "ops@example.com", got.Email)
	require.NotZero(t, got.UpdatedAt)
}

func TestFileStoreCertificatesFileUses0600(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ingress-certs.json"), []byte(`{"certificates":[]}`), 0o644))
	store := NewFileStore(dir)
	_, err := store.UpsertCertificate(ManagedCertificate{
		Domains: []string{"api.example.com"},
		Issuer:  CertificateIssuerManual,
		Status:  CertPending,
	})
	require.NoError(t, err)

	stat, err := os.Stat(filepath.Join(dir, "ingress-certs.json"))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), stat.Mode().Perm())
}

func validProjectIngress(projectID string, domain string) Ingress {
	return Ingress{
		ProjectID: projectID,
		Domain:    domain,
		Proxy:     ProxyConfig{Provider: ProviderNginx, HostIDs: []string{"edge-a"}},
		Upstreams: []Upstream{{IP: "10.0.0.12", Port: 8080}},
		ProxyOptions: ProxyOptions{
			RawTemplate: "server { server_name " + domain + "; }",
		},
		DNS: DNSConfig{
			Provider: ProviderManual,
			Records:  []Record{{Type: RecordA, Name: domain, Value: "203.0.113.10", TTL: 300}},
		},
	}
}
