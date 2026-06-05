// Package mcp 验证 MCP 目标解析逻辑。
//
// 职责：
//   - 验证 deployment_id 精确解析
//   - 验证 service/env/name 组合解析
//   - 验证多 deployment 目标必须显式指定 env 或 deployment_id
//
// 边界：
//   - 不访问真实 agent HTTP 服务
package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestResolveDeploymentByID(t *testing.T) {
	p := sampleProject()
	target, errResp := resolveDeploymentTarget([]model.Project{p}, targetArgs{DeploymentID: "dep-api-dev"})
	require.Nil(t, errResp)
	assert.Equal(t, "api", target.Service.Name)
	assert.Equal(t, "dev", target.Deployment.EnvName)
}

func TestResolveDeploymentRequiresEnvWhenServiceHasMultipleDeployments(t *testing.T) {
	p := sampleProject()
	_, errResp := resolveDeploymentTarget([]model.Project{p}, targetArgs{ProjectName: "demo", ServiceName: "api"})
	require.NotNil(t, errResp)
	assert.Equal(t, "env_required", errResp.Code)
	assert.Len(t, errResp.Candidates, 2)
}

func TestResolveDeploymentByProjectServiceAndEnv(t *testing.T) {
	p := sampleProject()
	target, errResp := resolveDeploymentTarget([]model.Project{p}, targetArgs{ProjectName: "demo", ServiceName: "api", EnvName: "prod"})
	require.Nil(t, errResp)
	assert.Equal(t, "dep-api-prod", target.Deployment.ID)
}

func sampleProject() model.Project {
	return model.Project{
		ID:   "p1",
		Name: "demo",
		Services: []model.Service{{
			ID:        "svc-api",
			ProjectID: "p1",
			Name:      "api",
			Deployments: []model.Deployment{
				{ID: "dep-api-dev", EnvName: "dev"},
				{ID: "dep-api-prod", EnvName: "prod"},
			},
		}},
	}
}
