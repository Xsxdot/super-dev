// Package configchange 验证 MCP 配置变更的纯合并、校验、diff 和预检计划。
//
// 职责：
//   - 验证 upsert 不删除未提及配置
//   - 验证删除会被拒绝
//   - 验证 diff 脱敏和 plan fingerprint 稳定
//
// 边界：
//   - 不读写 .superdev/config.yaml
//   - 不调用 agent HTTP API
package configchange

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
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

func TestApplyChangeUpsertsServiceLanguageForExistingAndNewServices(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo

	updateExisting := ChangeRequest{
		Kind: KindServiceUpsert,
		Service: &ServicePatch{
			ID:       "svc-worker",
			Name:     "worker",
			Language: ptrLanguage(model.LanguageNode),
		},
	}
	updated, err := Apply(project, updateExisting)
	require.NoError(t, err)
	assert.Equal(t, model.LanguageNode, findServiceForTest(updated, "worker").Language)

	addNew := ChangeRequest{
		Kind: KindServiceUpsert,
		Service: &ServicePatch{
			Name:     "api",
			Language: ptrLanguage(model.LanguagePython),
		},
	}
	updated, err = Apply(updated, addNew)
	require.NoError(t, err)
	assert.Equal(t, model.LanguagePython, findServiceForTest(updated, "api").Language)
}

func TestApplyChangeClearsServiceLanguageWhenExplicitlyEmpty(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo
	var change ChangeRequest
	require.NoError(t, json.Unmarshal([]byte(`{
		"kind": "config.service.upsert",
		"service": {
			"id": "svc-worker",
			"name": "worker",
			"language": ""
		}
	}`), &change))

	updated, err := Apply(project, change)

	require.NoError(t, err)
	assert.Empty(t, findServiceForTest(updated, "worker").Language)
}

func TestServiceUpsertPreservesDeploymentWebConfig(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind:      KindServiceUpsert,
		ProjectID: "p1",
		Service: &ServicePatch{
			ID:   "svc-api",
			Name: "api",
			Deployments: []DeploymentPatch{{
				ID:       "dep-api-dev",
				EnvName:  "dev",
				Location: model.LocationLocal,
				Runtime:  &model.RuntimeConfig{Type: model.RuntimeTypeCommand, Command: "pnpm dev"},
				Logs:     &model.LogConfig{Type: model.LogKindProcess},
				Web: &model.WebEntrypointConfig{
					Enabled: true,
					URL:     "http://127.0.0.1:3000",
					AIDebug: model.WebAIDebugConfig{Enabled: true},
				},
			}},
		},
	}

	updated, err := Apply(project, change)
	require.NoError(t, err)
	result := Validate(updated, change)

	require.True(t, result.OK, result.Errors)
	got := findServiceForTest(updated, "api").Deployments[0].Web
	require.NotNil(t, got)
	assert.Equal(t, "http://127.0.0.1:3000", got.URL)
	assert.True(t, got.AIDebug.Enabled)
}

func TestServiceUpsertPreservesDeploymentCodeDebugConfig(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind:      KindServiceUpsert,
		ProjectID: "p1",
		Service: &ServicePatch{
			ID:       "svc-api",
			Name:     "api",
			Language: ptrLanguage(model.LanguageGo),
			Deployments: []DeploymentPatch{{
				ID:          "dep-api-dev",
				EnvName:     "dev",
				Location:    model.LocationLocal,
				ControlMode: model.ControlModeManaged,
				Runtime: &model.RuntimeConfig{
					Type:   model.RuntimeTypeLanguage,
					CWD:    ".",
					Config: map[string]any{"program": "./cmd/api"},
				},
				Logs: &model.LogConfig{Type: model.LogKindProcess},
				CodeDebug: &model.CodeDebugConfig{
					Policy:      model.CodeDebugPolicyEnabled,
					Mode:        model.CodeDebugModeLaunch,
					StopOnEntry: true,
				},
			}},
		},
	}

	updated, err := Apply(project, change)
	require.NoError(t, err)
	result := Validate(updated, change)

	require.True(t, result.OK, result.Errors)
	got := findServiceForTest(updated, "api").Deployments[0].CodeDebug
	require.NotNil(t, got)
	assert.Equal(t, model.CodeDebugPolicyEnabled, got.Policy)
	assert.Equal(t, model.CodeDebugModeLaunch, got.Mode)
	assert.True(t, got.StopOnEntry)
}

func TestServiceUpsertRejectsRemoteWebDebugV1(t *testing.T) {
	project := sampleProject()
	change := ChangeRequest{
		Kind:      KindServiceUpsert,
		ProjectID: "p1",
		Service: &ServicePatch{
			ID:   "svc-api",
			Name: "api",
			Deployments: []DeploymentPatch{{
				ID:          "dep-api-dev",
				EnvName:     "dev",
				Location:    model.LocationRemote,
				ControlMode: model.ControlModeMonitor,
				Web: &model.WebEntrypointConfig{
					Enabled: true,
					URL:     "http://127.0.0.1:3000",
					AIDebug: model.WebAIDebugConfig{Enabled: true},
				},
			}},
		},
	}

	updated, err := Apply(project, change)
	require.NoError(t, err)
	result := Validate(updated, change)

	assert.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "; "), "local deployments only")
}

func TestValidateCodeDebugRequiresLocalManagedLanguageRuntime(t *testing.T) {
	project := sampleProject()
	project.Services[0].Deployments[0] = model.Deployment{
		ID:           "dep-api-dev",
		EnvName:      "dev",
		Location:     model.LocationRemote,
		ControlMode:  model.ControlModeManaged,
		HostIDs:      []string{"h1"},
		StartCommand: "systemctl start api",
		StopCommand:  "systemctl stop api",
		CodeDebug: &model.CodeDebugConfig{
			Policy: model.CodeDebugPolicyEnabled,
			Mode:   model.CodeDebugModeLaunch,
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "code_debug supports local managed language runtime deployments only")
}

func TestValidateCodeDebugAllowsLanguageRuntime(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-api-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{"program": "./cmd/api"},
		},
		CodeDebug: &model.CodeDebugConfig{
			Policy: model.CodeDebugPolicyEnabled,
			Mode:   model.CodeDebugModeLaunch,
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.True(t, result.OK, result.Errors)
}

func TestValidateLanguageRuntimeRequiresServiceLanguage(t *testing.T) {
	project := sampleProject()
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{"program": "./cmd/worker"},
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "service worker language is required for local managed language runtime")
}

func TestValidateLanguageRuntimeRejectsUnsupportedServiceLanguage(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.ServiceLanguage("ruby")
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{"program": "./cmd/worker"},
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "service worker language ruby is unsupported for local managed language runtime")
}

func TestValidateLanguageRuntimeRejectsCWDOutsideProjectRoot(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "../outside",
			Config: map[string]any{"program": "./cmd/worker"},
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "runtime.cwd must be inside project root")
}

func TestValidateLanguageRuntimeRejectsProgramOutsideProjectRoot(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageNode
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./web",
			Config: map[string]any{"program": "../../outside/server.js"},
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "runtime.config.program must be inside project root")
}

func TestValidateLanguageRuntimeRejectsPythonWithoutEntry(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguagePython
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{},
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "service worker python runtime requires program, module, or runtime_executable")
}

func TestValidateCodeDebugPolicy(t *testing.T) {
	project := sampleProject()
	project.Services[0].Deployments[0].CodeDebug = &model.CodeDebugConfig{
		Policy: model.CodeDebugPolicy("bogus"),
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "code_debug.policy must be auto, enabled, or disabled")
}

func TestValidateCodeDebugValidPolicy(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "./worker"},
		},
		CodeDebug: &model.CodeDebugConfig{
			Policy: model.CodeDebugPolicyDisabled,
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.True(t, result.OK, result.Errors)
}

func TestValidateCodeDebugRejectsCommandRuntime(t *testing.T) {
	project := sampleProject()
	project.Services[0].Deployments[0].CodeDebug = &model.CodeDebugConfig{
		Policy: model.CodeDebugPolicyDisabled,
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.False(t, result.OK)
	assert.Contains(t, strings.Join(result.Errors, "\n"), "code_debug supports local managed language runtime deployments only")
}

func TestValidateCodeDebugAllowsLanguageRuntimeWithoutCodeDebugProgramOverride(t *testing.T) {
	project := sampleProject()
	project.Services[0].Language = model.LanguageGo
	project.Services[0].Deployments[0] = model.Deployment{
		ID:          "dep-worker-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "./worker"},
		},
		CodeDebug: &model.CodeDebugConfig{
			Mode: model.CodeDebugModeLaunch,
		},
	}

	result := Validate(project, ChangeRequest{Kind: KindServiceUpsert})

	require.True(t, result.OK, result.Errors)
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

func TestValidateRejectsDelete(t *testing.T) {
	project := sampleProject()
	deleteChange := ChangeRequest{Kind: KindServiceUpsert, Delete: true, Service: &ServicePatch{Name: "api"}}
	deleteResult := Validate(project, deleteChange)
	assert.False(t, deleteResult.OK)
	assert.Contains(t, deleteResult.Errors, "delete is not supported by MCP config upsert")
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

func TestDiffIncludesDeploymentCodeDebugChanges(t *testing.T) {
	before := sampleProject()
	after := sampleProject()
	after.Services[0].Deployments[0].CodeDebug = &model.CodeDebugConfig{
		Policy:      model.CodeDebugPolicyEnabled,
		Mode:        model.CodeDebugModeLaunch,
		StopOnEntry: true,
	}

	diff := Diff(before, after)

	assert.Contains(t, diff, DiffEntry{
		Path:   "services[worker].deployments[dev].code_debug",
		Before: map[string]any(nil),
		After: map[string]any{
			"policy":        model.CodeDebugPolicyEnabled,
			"mode":          model.CodeDebugModeLaunch,
			"stop_on_entry": true,
		},
	})
}

func TestCodeDebugSummaryPolicy(t *testing.T) {
	out := codeDebugSummary(&model.CodeDebugConfig{Policy: model.CodeDebugPolicyEnabled, AdapterCommand: "dlv"})
	if out["policy"] != model.CodeDebugPolicyEnabled {
		t.Fatalf("summary policy = %v", out["policy"])
	}
}

func TestPlanConfigChangeRequiresApprovalAndHasStableFingerprint(t *testing.T) {
	before := sampleProject()
	change := ChangeRequest{
		Kind: KindPipelineUpsert,
		Pipeline: &ProjectPipelinePatch{
			ID: "deploy-dev", Name: "Deploy Dev", Services: []string{"worker"}, Pipeline: model.Pipeline{},
		},
	}
	after, err := Apply(before, change)
	require.NoError(t, err)
	diff := Diff(before, after)
	validation := Validate(after, change)

	first := Plan(before, after, change, diff, validation)
	second := Plan(before, after, change, diff, validation)

	assert.Equal(t, KindPipelineUpsert, first.Kind)
	assert.True(t, first.RequiresApproval)
	assert.Equal(t, "high", first.RiskLevel)
	assert.Equal(t, first.Fingerprint, second.Fingerprint)
	assert.Contains(t, first.ExpectedEffects[0], "update project pipeline deploy-dev")
}

func TestPlanConfigChangeDeniesUnsupportedOperation(t *testing.T) {
	before := sampleProject()
	change := ChangeRequest{Kind: KindServiceUpsert, Delete: true, Service: &ServicePatch{Name: "api"}}
	validation := Validate(before, change)

	plan := Plan(before, before, change, nil, validation)

	assert.True(t, plan.Denied)
	assert.Equal(t, "critical", plan.RiskLevel)
	assert.Contains(t, plan.Reasons, "delete is not supported by MCP config upsert")
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

func ptrLanguage(v model.ServiceLanguage) *model.ServiceLanguage {
	return &v
}
