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
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

func projectForResolve() model.Project {
	return model.Project{
		ID:        "p1",
		Name:      "demo",
		RootPath:  "/repo/demo",
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
	project := projectForResolve()
	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:      project,
		PipelineID:   "deploy-dev",
		EnvName:      "dev",
		ServiceNames: []string{"api"},
		RunVariables: map[string]string{"version": "manual"},
		Preview:      true,
	})
	require.NoError(t, err)
	assert.Equal(t, "resources/dev.yaml", resolved.Pipeline.Variables["config_file"])
	assert.Equal(t, project.RootPath, resolved.Pipeline.Variables["workspace"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/output", resolved.Pipeline.Variables["output"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/artifacts", resolved.Pipeline.Variables["artifacts"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview", resolved.Pipeline.Variables["run_temp_dir"])
	assert.Equal(t, "dev", resolved.Pipeline.Variables["env"])
	assert.Equal(t, "manual", resolved.Pipeline.Variables["version"])
	assert.Equal(t, "20260101", resolved.Pipeline.Variables["date"])
	assert.Equal(t, "000000", resolved.Pipeline.Variables["time"])
	assert.Equal(t, []string{"h1", "h2"}, resolved.Pipeline.Roles["api_targets"])
	require.Len(t, resolved.Pipeline.Deploy, 1)
	assert.Equal(t, []string{"api_targets"}, resolved.Pipeline.Deploy[0].Roles)
	assert.Equal(t, "echo resources/dev.yaml", resolved.Pipeline.Deploy[0].With["cmd"])
	assert.Equal(t, "project:p1:pipeline:deploy-dev:env:dev", resolved.RunID)
}

func TestResolveProjectPipelineRendersArtifactVarAndConcurrency(t *testing.T) {
	project := model.Project{
		ID:        "p1",
		RootPath:  "/repo",
		Variables: map[string]string{"app_name": "api"},
		Environments: []model.Environment{
			{Name: "prod"},
		},
		Services: []model.Service{{
			ID:   "svc-api",
			Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-api-prod", EnvName: "prod", HostIDs: []string{"h1"},
			}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID:           "deploy-prod",
			Name:         "Deploy Prod",
			ArtifactKind: model.ArtifactKindFile,
			Variables:    map[string]string{"artifact": "${artifacts}/${app_name}-${version}.tar.gz"},
			Roles:        map[string]model.ProjectPipelineRole{"api_targets": {FromService: "api"}},
			Pipeline: model.Pipeline{Deploy: []model.Step{{
				Name: "Upload ${env}", Type: "transfer", Roles: []string{"api_targets"}, Concurrency: "batch:2",
			}}},
		}},
	}
	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:      project,
		PipelineID:   "deploy-prod",
		EnvName:      "prod",
		RunVariables: map[string]string{"version": "v1"},
		Preview:      true,
	})
	require.NoError(t, err)
	assert.Equal(t, model.ArtifactKindFile, resolved.ProjectPipeline.ArtifactKind)
	// 制品位置来自渲染后的保留字变量 artifact。
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/artifacts/api-v1.tar.gz", resolved.Pipeline.Variables["artifact"])
	assert.Equal(t, "batch:2", resolved.Pipeline.Deploy[0].Concurrency)
}

func TestResolveInjectsSyncModeVar(t *testing.T) {
	project := model.Project{
		ID:   "proj-1",
		Name: "demo",
		Pipelines: []model.ProjectPipeline{{
			ID:       "pp-1",
			Name:     "demo",
			SyncMode: model.SyncModeRemoteCmd,
			Pipeline: model.Pipeline{},
		}},
	}
	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "pp-1",
		EnvName:    "dev",
	})
	require.NoError(t, err)
	assert.Equal(t, "remote_cmd", resolved.Pipeline.Variables["sync_mode"])
}

func TestResolveSyncModeDefaultsTransfer(t *testing.T) {
	project := model.Project{
		ID:   "proj-1",
		Name: "demo",
		Pipelines: []model.ProjectPipeline{{
			ID:       "pp-1",
			Name:     "demo",
			Pipeline: model.Pipeline{},
		}},
	}
	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "pp-1",
		EnvName:    "dev",
	})
	require.NoError(t, err)
	assert.Equal(t, "transfer", resolved.Pipeline.Variables["sync_mode"])
}

func TestResolveProjectPipelineAppliesBuilderRoleToBuildSteps(t *testing.T) {
	project := model.Project{
		ID:   "proj-1",
		Name: "demo",
		Pipelines: []model.ProjectPipeline{{
			ID:   "pp-1",
			Name: "demo",
			Roles: map[string]model.ProjectPipelineRole{
				"builder": {Hosts: []string{"h-builder"}},
			},
			Pipeline: model.Pipeline{
				Build: []model.Step{{
					Name: "Build",
					Type: "remote_command",
					With: map[string]interface{}{"cmd": "go build"},
				}},
				Deploy: []model.Step{{
					Name: "Deploy",
					Type: "remote_command",
					With: map[string]interface{}{"cmd": "systemctl restart api"},
				}},
			},
		}},
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "pp-1",
		EnvName:    "dev",
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"h-builder"}, resolved.Pipeline.Roles["builder"])
	require.Len(t, resolved.Pipeline.Build, 1)
	assert.Equal(t, []string{"builder"}, resolved.Pipeline.Build[0].Roles)
	require.Len(t, resolved.Pipeline.Deploy, 1)
	assert.Empty(t, resolved.Pipeline.Deploy[0].Roles)
}

func TestResolveProjectPipelineDoesNotApplyBuilderRoleToLocalOnlyBuildSteps(t *testing.T) {
	project := model.Project{
		ID:   "proj-1",
		Name: "demo",
		Pipelines: []model.ProjectPipeline{{
			ID:   "pp-1",
			Name: "demo",
			Roles: map[string]model.ProjectPipelineRole{
				"builder": {Hosts: []string{"h-builder"}},
			},
			Pipeline: model.Pipeline{
				Build: []model.Step{
					{Name: "Build", Type: "local_command", With: map[string]interface{}{"cmd": "go build"}},
					{Name: "Package", Type: "archive_package", With: map[string]interface{}{"artifact": "api.tar.gz"}},
					{Name: "Include", Type: "include", With: map[string]interface{}{"template": "builtin://go-binary-build"}},
				},
			},
		}},
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "pp-1",
		EnvName:    "dev",
	})

	require.NoError(t, err)
	require.Len(t, resolved.Pipeline.Build, 3)
	assert.Empty(t, resolved.Pipeline.Build[0].Roles)
	assert.Empty(t, resolved.Pipeline.Build[1].Roles)
	assert.Empty(t, resolved.Pipeline.Build[2].Roles)
}

func TestResolveProjectPipelineDoesNotApplyBuilderRoleToUnknownBuildStep(t *testing.T) {
	project := model.Project{
		ID:   "proj-1",
		Name: "demo",
		Pipelines: []model.ProjectPipeline{{
			ID:   "pp-1",
			Name: "demo",
			Roles: map[string]model.ProjectPipelineRole{
				"builder": {Hosts: []string{"h-builder"}},
			},
			Pipeline: model.Pipeline{
				Build: []model.Step{{
					Name: "Custom",
					Type: "custom_build_plugin",
					With: map[string]interface{}{"cmd": "build"},
				}},
			},
		}},
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "pp-1",
		EnvName:    "dev",
	})

	require.NoError(t, err)
	require.Len(t, resolved.Pipeline.Build, 1)
	assert.Empty(t, resolved.Pipeline.Build[0].Roles)
}

func TestResolveProjectPipelineInjectsWorkspaceForRuntimeWithoutPreview(t *testing.T) {
	project := model.Project{
		ID:       "p1",
		RootPath: "/repo/demo",
		Pipelines: []model.ProjectPipeline{{
			ID:        "deploy-prod",
			Name:      "Deploy Prod",
			Variables: map[string]string{"artifact": "${artifacts}/api-${version}.tar.gz"},
			Pipeline: model.Pipeline{
				Build: []model.Step{{
					Name: "Build",
					Type: "include",
					With: map[string]interface{}{
						"template": "builtin://fake-build",
						"vars": map[string]interface{}{
							"frontend_dir":  "${workspace}/admin",
							"binary_output": "${output}/api",
						},
					},
				}},
			},
		}},
	}

	resolved, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:      project,
		PipelineID:   "deploy-prod",
		EnvName:      "prod",
		RunVariables: map[string]string{"version": "v1"},
	})

	require.NoError(t, err)
	assert.Equal(t, "/repo/demo", resolved.Pipeline.Variables["workspace"])
	assert.NotContains(t, resolved.Pipeline.Variables, "output")
	assert.NotContains(t, resolved.Pipeline.Variables, "artifacts")
	assert.Equal(t, "${artifacts}/api-v1.tar.gz", resolved.Pipeline.Variables["artifact"])
	require.Len(t, resolved.Pipeline.Build, 1)
	vars, ok := resolved.Pipeline.Build[0].With["vars"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "/repo/demo/admin", vars["frontend_dir"])
	assert.Equal(t, "${output}/api", vars["binary_output"])
}

func TestResolveProjectPipelineRejectsReservedVariables(t *testing.T) {
	project := projectForResolve()
	project.Variables = map[string]string{"workspace": "/bad"}

	_, err := pipeline.ResolveProjectPipeline(pipeline.ProjectPipelineRequest{
		Project:    project,
		PipelineID: "deploy-dev",
		EnvName:    "dev",
		Preview:    true,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace")
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
