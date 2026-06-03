// handler_ingress_project_test.go 验证项目级 Ingress HTTP API。
//
// 职责：
//   - 验证项目级 Ingress 列表按 project_id 过滤
//   - 验证入口 defaults 接口从项目流水线和 Host 地址推断默认值
//
// 边界：
//   - 不访问真实 DNS、ACME 或远端 nginx
//   - 不测试桌面端 API client
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/ingress"
	"github.com/superdev/agent/model"
)

func TestProjectIngressListFiltersByProject(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	postProjectIngressJSON(t, srv.URL+"/api/projects/proj-a/ingress", `{
		"name":"api-a",
		"domain":"api-a.example.com",
		"proxy":{"provider":"nginx","host_ids":["self"]},
		"upstreams":[{"ip":"10.0.0.12","port":8080}],
		"proxy_options":{"raw_template":"server { server_name api-a.example.com; }"},
		"dns":{"provider":"manual","records":[{"type":"A","name":"api-a.example.com","value":"203.0.113.10"}]}
	}`)
	postProjectIngressJSON(t, srv.URL+"/api/projects/proj-b/ingress", `{
		"name":"api-b",
		"domain":"api-b.example.com",
		"proxy":{"provider":"nginx","host_ids":["self"]},
		"upstreams":[{"ip":"10.0.0.13","port":8080}],
		"proxy_options":{"raw_template":"server { server_name api-b.example.com; }"},
		"dns":{"provider":"manual","records":[{"type":"A","name":"api-b.example.com","value":"203.0.113.11"}]}
	}`)

	resp, err := http.Get(srv.URL + "/api/projects/proj-a/ingress")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var items []ingress.Ingress
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))
	require.Len(t, items, 1)
	assert.Equal(t, "proj-a", items[0].ProjectID)
	assert.Equal(t, "api-a.example.com", items[0].Domain)
}

func TestProjectIngressInferDefaults(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	app.projects = []model.Project{{
		ID: "proj-a",
		Pipelines: []model.ProjectPipeline{{
			ID: "deploy-prod",
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {Hosts: []string{"app-a"}},
			},
		}},
	}}
	_, err := app.remoteStore.AddHost(model.Host{ID: "edge-a", Name: "edge", PublicIP: "203.0.113.10"})
	require.NoError(t, err)
	_, err = app.remoteStore.AddHost(model.Host{ID: "app-a", Name: "app", PrivateIP: "10.0.0.12"})
	require.NoError(t, err)

	resp := postProjectIngressJSON(t, srv.URL+"/api/projects/proj-a/ingress/defaults", `{
		"env_name":"prod",
		"pipeline_id":"deploy-prod",
		"role":"api_targets",
		"proxy_host_ids":["edge-a"],
		"domain":"api.example.com",
		"record_type":"A"
	}`)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result ingress.InferResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	require.Len(t, result.Upstreams, 1)
	require.Len(t, result.DNSRecords, 1)
	assert.Equal(t, "10.0.0.12", result.Upstreams[0].IP)
	assert.Equal(t, "203.0.113.10", result.DNSRecords[0].Value)
}

func postProjectIngressJSON(t *testing.T, url string, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	return resp
}
