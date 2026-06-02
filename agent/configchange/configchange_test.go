// Package configchange 验证 MCP 配置变更的纯合并、校验、diff 和预检计划。
//
// 职责：
//   - 验证 upsert 不删除未提及配置
//   - 验证删除和 deployment-level pipeline 会被拒绝
//   - 验证 diff 脱敏和 plan fingerprint 稳定
//
// 边界：
//   - 不读写 .superdev/config.yaml
//   - 不调用 agent HTTP API
package configchange

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestApplyChangeUpsertsServiceAndPreservesUnmentionedItems(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind:      KindServiceUpsert,
		ProjectID: "p1",
		Service: &ServicePatch{
			Name:     "api",
			Required: ptrBool(true),
			Deployments: []DeploymentPatch{{
				EnvName:  "dev",
				Location: model.LocationLocal,
				Runtime:  &model.RuntimeConfig{Type: model.RuntimeTypeCommand, Command: "go run ./cmd/api"},
				Logs:     &model.LogConfig{Type: model.LogKindProcess},
			}},
		},
	}

	updated, err := Apply(project, change)

	require.NoError(t, err)
	require.Len(t, updated.Services, 2)
	api := findServiceForTest(updated, "api")
	require.NotNil(t, api)
	assert.True(t, api.Required)
	require.Len(t, api.Deployments, 1)
	assert.Equal(t, "go run ./cmd/api", api.Deployments[0].Runtime.Command)
	assert.NotEmpty(t, api.Deployments[0].ID)
	assert.NotNil(t, findServiceForTest(updated, "worker"))
}

func TestApplyChangeUpsertsProjectPipelineAndPreservesOthers(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind:      KindPipelineUpsert,
		ProjectID: "p1",
		Pipeline: &ProjectPipelinePatch{
			ID:       "deploy-dev",
			Name:     "Deploy Dev",
			Services: []string{"api"},
			Pipeline: model.Pipeline{Build: []model.Step{{
				Name: "Build", Type: "local_command", With: map[string]interface{}{"command": "go build ./..."},
			}}},
		},
	}

	updated, err := Apply(project, change)

	require.NoError(t, err)
	require.Len(t, updated.Pipelines, 2)
	assert.Equal(t, "Deploy Dev", findPipelineForTest(updated, "deploy-dev").Name)
	assert.Equal(t, "Existing", findPipelineForTest(updated, "existing").Name)
}

func TestValidateRejectsDeleteAndDeploymentPipeline(t *testing.T) {
	project := sampleProject()
	deleteChange := ChangeRequest{Kind: KindServiceUpsert, Delete: true, Service: &ServicePatch{Name: "api"}}
	deleteResult := Validate(project, deleteChange)
	assert.False(t, deleteResult.OK)
	assert.Contains(t, deleteResult.Errors, "delete is not supported by MCP config upsert")

	pipelineChange := ChangeRequest{
		Kind: KindServiceUpsert,
		Service: &ServicePatch{Name: "api", Deployments: []DeploymentPatch{{
			EnvName:  "dev",
			Pipeline: &model.Pipeline{Build: []model.Step{{Name: "Build", Type: "local_command"}}},
		}}},
	}
	pipelineResult := Validate(project, pipelineChange)
	assert.False(t, pipelineResult.OK)
	assert.Contains(t, pipelineResult.Errors, "deployment-level pipeline is not supported; use project.pipelines")
}

func TestValidateRejectsUnknownProjectPipelineService(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind: KindPipelineUpsert,
		Pipeline: &ProjectPipelinePatch{
			ID: "deploy-missing", Name: "Deploy Missing", Services: []string{"missing"}, Pipeline: model.Pipeline{},
		},
	}

	updated, err := Apply(project, change)
	require.NoError(t, err)
	result := Validate(updated, change)

	assert.False(t, result.OK)
	assert.Contains(t, result.Errors, "pipeline deploy-missing references unknown service missing")
}

func TestDiffRedactsSecretValues(t *testing.T) {
	before := sampleProject()
	after := sampleProject()
	after.Variables["API_TOKEN"] = "new-secret"
	after.Variables["PUBLIC"] = "new-public"

	diff := Diff(before, after)

	assert.Contains(t, diff, DiffEntry{Path: "variables.API_TOKEN", Before: "[redacted]", After: "[redacted]"})
	assert.Contains(t, diff, DiffEntry{Path: "variables.PUBLIC", Before: "old", After: "new-public"})
}

func sampleProject() model.Project {
	return model.Project{
		ID:       "p1",
		Name:     "demo",
		RootPath: "/tmp/demo",
		Variables: map[string]string{
			"PUBLIC": "old",
		},
		Environments: []model.Environment{{ID: "env-dev", Name: "dev", IsDev: true, Order: 1}},
		Services: []model.Service{{
			ID: "svc-worker", Name: "worker", Order: 2,
			Deployments: []model.Deployment{{ID: "dep-worker-dev", EnvName: "dev", Location: model.LocationLocal, Command: "go run ./worker"}},
		}},
		Pipelines: []model.ProjectPipeline{{
			ID: "existing", Name: "Existing", Services: []string{"worker"}, Pipeline: model.Pipeline{},
		}},
	}
}

func findServiceForTest(project model.Project, name string) *model.Service {
	for i := range project.Services {
		if project.Services[i].Name == name {
			return &project.Services[i]
		}
	}
	return nil
}

func findPipelineForTest(project model.Project, id string) model.ProjectPipeline {
	for _, item := range project.Pipelines {
		if item.ID == id {
			return item
		}
	}
	return model.ProjectPipeline{}
}

func ptrBool(v bool) *bool {
	return &v
}
