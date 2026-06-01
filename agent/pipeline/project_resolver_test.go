// Package pipeline_test 验证项目级流水线解析。
//
// 职责：
//   - 验证环境变量覆盖顺序
//   - 验证 from_service 角色解析到对应环境下的 deployment hosts
//   - 验证服务和环境缺失时返回明确错误
//
// 边界：
//   - 不展开 include 模板
//   - 不执行插件步骤
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

func projectForResolve() model.Project {
	return model.Project{
		ID:        "p1",
		Name:      "demo",
		Variables: map[string]string{"app_name": "demo", "config_file": "resources/base.yaml"},
		Services: []model.Service{{
			ID:   "svc-api",
			Name: "api",
			Deployments: []model.Deployment{{
				ID:       "dep-api-dev",
				EnvName:  "dev",
				Location: model.LocationRemote,
				HostIDs:  []string{"h1", "h2"},
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID:       "deploy-dev",
			Name:     "Deploy Dev",
			Services: []string{"api"},
			Variables: map[string]string{
				"target_api":  "api_targets",
				"config_file": "resources/default-dev.yaml",
			},
			Environments: map[string]model.PipelineEnvironment{
				"dev": {Variables: map[string]string{"config_file": "resources/dev.yaml"}},
			},
			Roles: map[string]model.ProjectPipelineRole{
				"api_targets": {FromService: "api"},
			},
			Pipeline: model.Pipeline{
				Variables: map[string]string{"pipeline_only": "x"},
				Deploy: []model.Step{{
					Name:  "Deploy API",
					Type:  "remote_command",
					Roles: []string{"${target_api}"},
					With:  map[string]interface{}{"cmd": "echo ${config_file}"},
				}},
			},
		}},
	}
}

func TestResolveProjectPipelineAppliesEnvVariablesAndServiceRoles(t *testing.T) {
	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:        projectForResolve(),
		PipelineID:     "deploy-dev",
		EnvName:        "dev",
		ServiceNames:   []string{"api"},
		RunVariables:   map[string]string{"version": "manual"},
		PreviewTempDir: "/tmp/super-debug-pipeline-preview",
	})
	require.NoError(t, err)
	assert.Equal(t, "resources/dev.yaml", resolved.Pipeline.Variables["config_file"])
	assert.Equal(t, "manual", resolved.Pipeline.Variables["version"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview", resolved.Pipeline.Variables["run_temp_dir"])
	assert.Equal(t, []string{"h1", "h2"}, resolved.Pipeline.Roles["api_targets"])
	require.Len(t, resolved.Pipeline.Deploy, 1)
	assert.Equal(t, []string{"api_targets"}, resolved.Pipeline.Deploy[0].Roles)
	assert.Equal(t, "echo resources/dev.yaml", resolved.Pipeline.Deploy[0].With["cmd"])
	assert.Equal(t, "project:p1:pipeline:deploy-dev:env:dev", resolved.RunID)
}

func TestResolveProjectPipelineRejectsMissingServiceDeployment(t *testing.T) {
	p := projectForResolve()
	p.Services[0].Deployments = nil
	_, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project: p, PipelineID: "deploy-dev", EnvName: "dev", ServiceNames: []string{"api"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service api has no deployment for env dev")
}

func TestResolveProjectPipelineRejectsUnknownPipeline(t *testing.T) {
	_, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project: projectForResolve(), PipelineID: "missing", EnvName: "dev",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pipeline missing not found")
}
