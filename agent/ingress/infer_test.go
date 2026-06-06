// Package ingress 验证项目流水线到入口默认配置的推断逻辑。
//
// 职责：
//   - 验证从 pipeline role 推断 upstream host
//   - 验证 public_ip/private_ip 优先级
//   - 验证端口保持手动填写
//
// 边界：
//   - 不保存 Ingress
//   - 不调用 DNS 或 nginx provider
package ingress

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestInferDefaultsFromPipelineRole(t *testing.T) {
	project := model.Project{
		ID: "proj-1",
		Pipelines: []model.ProjectPipeline{{
			ID: "deploy-prod",
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {Hosts: []string{"app-a", "app-b"}},
			},
		}},
	}
	hosts := []model.Host{
		{ID: "edge-a", PublicIP: "203.0.113.10"},
		{ID: "edge-b", PublicIP: "203.0.113.11"},
		{ID: "app-a", PrivateIP: "10.0.0.12"},
		{ID: "app-b", PrivateIP: "10.0.0.13"},
	}

	got, err := InferDefaults(project, hosts, InferRequest{
		EnvName:      "prod",
		PipelineID:   "deploy-prod",
		Role:         "api_targets",
		ProxyHostIDs: []string{"edge-a", "edge-b"},
		Domain:       "api.example.com",
		RecordType:   RecordA,
	})
	require.NoError(t, err)
	assert.Equal(t, []Upstream{
		{HostID: "app-a", IP: "10.0.0.12"},
		{HostID: "app-b", IP: "10.0.0.13"},
	}, got.Upstreams)
	assert.Equal(t, []Record{
		{Type: RecordA, Name: "api.example.com", Value: "203.0.113.10", TTL: 300},
		{Type: RecordA, Name: "api.example.com", Value: "203.0.113.11", TTL: 300},
	}, got.DNSRecords)
	assert.True(t, got.RequiresPortInput)
}

func TestInferDefaultsFromServiceDeployment(t *testing.T) {
	project := model.Project{
		ID: "proj-1",
		Services: []model.Service{{
			ID:   "api",
			Name: "api",
			Deployments: []model.Deployment{{
				EnvName: "prod",
				HostIDs: []string{"app-a"},
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID: "deploy-prod",
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {FromService: "api"},
			},
		}},
	}
	hosts := []model.Host{
		{ID: "edge-a", PublicIP: "203.0.113.10"},
		{ID: "app-a", PrivateIP: "10.0.0.12"},
	}

	got, err := InferDefaults(project, hosts, InferRequest{
		EnvName:      "prod",
		PipelineID:   "deploy-prod",
		Role:         "api_targets",
		ProxyHostIDs: []string{"edge-a"},
		Domain:       "api.example.com",
		RecordType:   RecordA,
	})
	require.NoError(t, err)
	assert.Equal(t, []Upstream{{HostID: "app-a", IP: "10.0.0.12"}}, got.Upstreams)
}

func TestInferDefaultsWarnsWhenIdentityAddressMissing(t *testing.T) {
	project := model.Project{
		ID: "proj-1",
		Pipelines: []model.ProjectPipeline{{
			ID: "deploy-prod",
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {Hosts: []string{"app-a"}},
			},
		}},
	}
	hosts := []model.Host{
		testTunnelHost("edge-a", "203.0.113.10"),
		testTunnelHost("app-a", "10.0.0.12"),
	}

	got, err := InferDefaults(project, hosts, InferRequest{
		EnvName:      "prod",
		PipelineID:   "deploy-prod",
		Role:         "api_targets",
		ProxyHostIDs: []string{"edge-a"},
		Domain:       "api.example.com",
		RecordType:   RecordA,
	})
	require.NoError(t, err)
	assert.Empty(t, got.Upstreams[0].IP)
	assert.Empty(t, got.DNSRecords[0].Value)
	assert.Contains(t, got.Warnings, "host edge-a has no public_ip")
	assert.Contains(t, got.Warnings, "host app-a has no private_ip")
}
