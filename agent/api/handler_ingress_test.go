// handler_ingress_test.go 验证 Ingress HTTP API 的端到端行为。
//
// 职责：
//   - 覆盖入口声明 CRUD、预览、应用校验和孤儿删除接口
//   - 验证 DNS provider 配置列表会返回本地完整配置供编辑回填
//
// 边界：
//   - 不连接真实 DNS、ACME 或远端 nginx
//   - 只通过 httptest 观察 HTTP 层与 ingress service 的装配结果
package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/ingress"
)

// TestIngressCRUDAndPreview 验证入口声明可以创建并预览 nginx 配置。
func TestIngressCRUDAndPreview(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress", `{
		"project_id": "proj-a",
		"name": "api",
		"domain": "api.example.com",
		"proxy": {"provider": "nginx", "host_ids": ["self"]},
		"upstreams": [{"ip": "127.0.0.1", "port": 8080}],
		"proxy_options": {"raw_template": "server { server_name api.example.com; }"},
		"dns": {
			"provider": "manual",
			"records": [{
				"type": "A",
				"name": "api.example.com",
				"value": "203.0.113.10"
			}]
		}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created ingress.Ingress
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.ID)
	assert.Equal(t, "api.example.com", created.Domain)

	previewResp := postIngressJSON(t, srv.URL+"/api/ingress/"+created.ID+"/preview", `{}`)
	defer previewResp.Body.Close()
	require.Equal(t, http.StatusOK, previewResp.StatusCode)

	var preview ingress.PreviewResult
	require.NoError(t, json.NewDecoder(previewResp.Body).Decode(&preview))
	assert.Equal(t, "api.example.com", preview.Ingress.Domain)
	assert.Equal(t, "203.0.113.10", preview.DNSRecord.Value)
	assert.Contains(t, preview.RenderedConfigByHost["self"], "server_name api.example.com")
}

// TestIngressApplyRejectsTLSWithoutCertificateID 验证启用 TLS 但未引用证书会在应用前被拒绝。
func TestIngressApplyRejectsTLSWithoutCertificateID(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress", `{
		"project_id": "proj-a",
		"name": "tls-api",
		"domain": "tls.example.com",
		"proxy": {"provider": "nginx", "host_ids": ["self"]},
		"upstreams": [{"ip": "127.0.0.1", "port": 8080}],
		"proxy_options": {"raw_template": "server { server_name tls.example.com; }"},
		"tls": {"enabled": true},
		"dns": {
			"provider": "manual",
			"records": [{
				"type": "A",
				"name": "tls.example.com",
				"value": "203.0.113.10"
			}]
		}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created ingress.Ingress
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	applyResp := postIngressJSON(t, srv.URL+"/api/ingress/"+created.ID+"/apply", `{"confirmed_dns_value":"203.0.113.10"}`)
	defer applyResp.Body.Close()
	require.Equal(t, http.StatusBadRequest, applyResp.StatusCode)

	body, err := io.ReadAll(applyResp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "tls.cert_id is required")
}

// TestIngressDNSProviderListReturnsSecrets 验证保存 DNS provider 后列表接口会返回 secrets。
func TestIngressDNSProviderListReturnsSecrets(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress/providers/dns", `{
		"id": "cloudflare-prod",
		"name": "Cloudflare Production",
		"type": "cloudflare",
		"zone_id": "zone-1",
		"secrets": {"api_token": "secret-token"}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	listResp, err := http.Get(srv.URL + "/api/ingress/providers/dns")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var providers []ingress.DNSProviderConfig
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&providers))
	require.Len(t, providers, 1)
	assert.Equal(t, "cloudflare-prod", providers[0].ID)
	assert.Equal(t, "secret-token", providers[0].Secrets["api_token"])
}

// TestIngressDNSProviderDropsAliyunZoneID 验证 Aliyun provider 保存时会丢弃无效的 zone_id 字段。
func TestIngressDNSProviderDropsAliyunZoneID(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress/providers/dns", `{
		"id": "aliyun-prod",
		"name": "Aliyun Production",
		"type": "aliyun",
		"zone_id": "should-be-dropped",
		"secrets": {
			"access_key_id": "ak",
			"access_key_secret": "sk"
		}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var saved ingress.DNSProviderConfig
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&saved))
	assert.Empty(t, saved.ZoneID)

	listResp, err := http.Get(srv.URL + "/api/ingress/providers/dns")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode)

	var providers []ingress.DNSProviderConfig
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&providers))
	require.Len(t, providers, 1)
	assert.Equal(t, "aliyun-prod", providers[0].ID)
	assert.Empty(t, providers[0].ZoneID)
}

// TestIngressDNSProviderRejectsManualConfig 验证 manual DNS 是内置 provider，不允许保存成自定义配置。
func TestIngressDNSProviderRejectsManualConfig(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress/providers/dns", `{
		"id": "manual-prod",
		"name": "Manual Production",
		"type": "manual"
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "manual DNS provider is built in")
}

// TestIngressOrphanRemovalWithEmptySelectionIsNoop 验证空孤儿删除请求不会触发远端或 DNS 副作用。
func TestIngressOrphanRemovalWithEmptySelectionIsNoop(t *testing.T) {
	srv, _ := newTestApp(t)

	resp := postIngressJSON(t, srv.URL+"/api/ingress", `{
		"name": "api",
		"domain": "api.example.com",
		"proxy": {"provider": "nginx", "host_ids": ["self"]},
		"upstreams": [{"ip": "127.0.0.1", "port": 8080}],
		"proxy_options": {"raw_template": "server { server_name api.example.com; }"},
		"dns": {
			"provider": "manual",
			"records": [{
				"type": "A",
				"name": "api.example.com",
				"value": "203.0.113.10"
			}]
		}
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created ingress.Ingress
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	removeResp := postIngressJSON(t, srv.URL+"/api/ingress/"+created.ID+"/orphan-removals", `{"configs":[],"records":[]}`)
	defer removeResp.Body.Close()
	require.Equal(t, http.StatusOK, removeResp.StatusCode)
}

func postIngressJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(strings.TrimSpace(body)))
	require.NoError(t, err)
	return resp
}
