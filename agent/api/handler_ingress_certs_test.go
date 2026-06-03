// handler_ingress_certs_test.go 验证全局 SSL 证书 HTTP API。
//
// 职责：
//   - 覆盖证书 CRUD、ACME 账号读写、匹配和部署接口
//   - 验证列表和详情不会泄漏私钥
//
// 边界：
//   - 不访问真实 ACME CA
//   - 不访问真实远端 host
package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/ingress"
)

func TestIngressCertificateCRUDRedactsPrivateKey(t *testing.T) {
	srv, _ := newTestApp(t)
	resp := postIngressJSON(t, srv.URL+"/api/ingress/certs", `{
		"domains":["api.example.com"],
		"issuer":"manual",
		"auto_renew":false,
		"material":{"domain":"api.example.com","cert_pem":"CERT","key_pem":"SECRET","provider":"manual"}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created ingress.ManagedCertificate
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Equal(t, ingress.CertActive, created.Status)
	require.Empty(t, created.Material.KeyPEM)

	listResp, err := http.Get(srv.URL + "/api/ingress/certs")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var list []ingress.ManagedCertificate
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&list))
	require.Len(t, list, 1)
	require.Empty(t, list[0].Material.KeyPEM)

	getResp, err := http.Get(srv.URL + "/api/ingress/certs/" + created.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()
	var detail ingress.ManagedCertificate
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&detail))
	require.Empty(t, detail.Material.KeyPEM)

	deleteResp, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/ingress/certs/"+created.ID, nil)
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(deleteResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestIngressACMEAccountRoundTrip(t *testing.T) {
	srv, _ := newTestApp(t)
	resp := postIngressJSON(t, srv.URL+"/api/ingress/acme-account", `{
		"email":"ops@example.com",
		"directory_url":"https://acme-staging-v02.api.letsencrypt.org/directory"
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	getResp, err := http.Get(srv.URL + "/api/ingress/acme-account")
	require.NoError(t, err)
	defer getResp.Body.Close()
	var got ingress.ACMEAccount
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&got))
	assert.Equal(t, "ops@example.com", got.Email)
}

func TestIngressCertificateMatchEndpoint(t *testing.T) {
	srv, _ := newTestApp(t)
	resp := postIngressJSON(t, srv.URL+"/api/ingress/certs", `{
		"domains":["*.example.com"],
		"issuer":"manual",
		"material":{"domain":"*.example.com","cert_pem":"CERT","key_pem":"SECRET","provider":"manual"}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	matchResp, err := http.Get(srv.URL + "/api/ingress/certs/match?domain=api.example.com")
	require.NoError(t, err)
	defer matchResp.Body.Close()
	require.Equal(t, http.StatusOK, matchResp.StatusCode)
	var matched ingress.ManagedCertificate
	require.NoError(t, json.NewDecoder(matchResp.Body).Decode(&matched))
	assert.Equal(t, []string{"*.example.com"}, matched.Domains)
	require.Empty(t, matched.Material.KeyPEM)
}
