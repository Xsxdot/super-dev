// Package operation 验证 MCP 写操作的本机安全策略。
//
// 职责：
//   - 验证运行态和模板导入操作的风险判定
//   - 验证 fingerprint 与稳定目标绑定
//
// 边界：
//   - 不调用 HTTP API
//   - 不执行进程或导入模板
package operation

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestPlanTunnelInvalidationDoesNotClaimMutationAlreadyPersisted(t *testing.T) {
	plan, err := PlanTunnelInvalidation(TunnelInvalidationRequest{
		HostID:        "host-1",
		Trigger:       "host_connection_config_changed",
		ChangedFields: []string{"ssh_host"},
	})

	require.NoError(t, err)
	require.Len(t, plan.Checks, 1)
	assert.Equal(t, "prepared_audit_required", plan.Checks[0].Name)
	assert.Equal(t, "passed", plan.Checks[0].Status)
	assert.NotContains(t, plan.Checks[0].Message, "persisted before invalidation")
}

func TestPlanRuntimeAllowsDevLocalDeployment(t *testing.T) {
	project := operationProject(true, model.LocationLocal, false)
	plan, err := PlanRuntime(OperationRuntimeStart, project, project.Services[0], project.Services[0].Deployments[0])

	require.NoError(t, err)
	assert.False(t, plan.Denied)
	assert.False(t, plan.RequiresApproval)
	assert.Equal(t, RiskLow, plan.RiskLevel)
	assert.Contains(t, plan.ExpectedEffects, "start local deployment api-dev")
	assert.NotEmpty(t, plan.Fingerprint)
}

func TestPlanRuntimeRequiresApprovalForNonDevLocalDeployment(t *testing.T) {
	project := operationProject(false, model.LocationLocal, false)
	plan, err := PlanRuntime(OperationRuntimeRestart, project, project.Services[0], project.Services[0].Deployments[0])

	require.NoError(t, err)
	assert.False(t, plan.Denied)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, RiskHigh, plan.RiskLevel)
	assert.Contains(t, plan.Reasons, "environment is not marked as dev")
}

func TestPlanRuntimeTreatsEmptyLocationAsLocalForSafety(t *testing.T) {
	project := operationProject(false, "", false)
	plan, err := PlanRuntime(OperationRuntimeRestart, project, project.Services[0], project.Services[0].Deployments[0])

	require.NoError(t, err)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, RiskHigh, plan.RiskLevel)
	assert.Contains(t, plan.Reasons, "environment is not marked as dev")
}

func TestPlanRuntimeDeniesReadOnlyDeployment(t *testing.T) {
	readOnlyProject := operationProject(true, model.LocationLocal, true)
	readOnlyPlan, err := PlanRuntime(OperationRuntimeStop, readOnlyProject, readOnlyProject.Services[0], readOnlyProject.Services[0].Deployments[0])
	require.NoError(t, err)
	assert.True(t, readOnlyPlan.Denied)
	assert.Equal(t, RiskCritical, readOnlyPlan.RiskLevel)
	assert.Contains(t, readOnlyPlan.Reasons, "deployment is read-only")
}

func TestPlanRuntimeRequiresApprovalForRemoteManagedDeployment(t *testing.T) {
	remoteProject := operationProject(true, model.LocationRemote, false)
	remotePlan, err := PlanRuntime(OperationRuntimeStop, remoteProject, remoteProject.Services[0], remoteProject.Services[0].Deployments[0])
	require.NoError(t, err)
	assert.False(t, remotePlan.Denied)
	assert.True(t, remotePlan.RequiresApproval)
	assert.Equal(t, RiskHigh, remotePlan.RiskLevel)
	assert.Contains(t, remotePlan.Reasons, "remote deployment control requires approval")
	assert.Contains(t, remotePlan.ExpectedEffects, "stop remote deployment api-dev")
	assert.NotEmpty(t, remotePlan.Fingerprint)
}

func TestPlanRuntimeStartSelectedRequiresApprovalForRemoteManagedDeployment(t *testing.T) {
	project := operationProject(true, model.LocationRemote, false)
	targets := []RuntimeDeploymentTarget{{
		Service:    project.Services[0],
		Deployment: project.Services[0].Deployments[0],
	}}

	plan, err := PlanRuntimeStartSelected(project, "prod", targets)

	require.NoError(t, err)
	assert.False(t, plan.Denied)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, RiskHigh, plan.RiskLevel)
	assert.Contains(t, plan.Reasons, "remote deployment control requires approval")
	assert.Contains(t, plan.ExpectedEffects, "start remote deployment api-dev")
}

func TestPlanTemplateImportRequiresApprovalAndBindsDigest(t *testing.T) {
	plan, err := PlanTemplateImport(TemplateImportRequest{
		Path:   "/tmp/custom.yaml",
		Digest: "sha256:abc",
		Summary: TemplateSummary{
			Source:  "user",
			ID:      "custom",
			Name:    "Custom",
			Version: "1.0.0",
		},
	})

	require.NoError(t, err)
	assert.True(t, plan.RequiresApproval)
	assert.False(t, plan.Denied)
	assert.Equal(t, RiskMedium, plan.RiskLevel)
	assert.Equal(t, "sha256:abc", plan.Target.TemplateDigest)
	assert.Contains(t, plan.ExpectedEffects, "import user pipeline template custom@1.0.0")
	assert.NotEmpty(t, plan.Fingerprint)
}

func TestOperationFingerprintChangesWhenTargetChanges(t *testing.T) {
	project := operationProject(false, model.LocationLocal, false)
	dep := project.Services[0].Deployments[0]

	planA, err := PlanRuntime(OperationRuntimeRestart, project, project.Services[0], dep)
	require.NoError(t, err)
	dep.ID = "api-prod-2"
	planB, err := PlanRuntime(OperationRuntimeRestart, project, project.Services[0], dep)
	require.NoError(t, err)

	assert.NotEqual(t, planA.Fingerprint, planB.Fingerprint)
}

func TestPlanPipelineRunBaseline(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo"}
	plan, err := PlanPipelineRun(project, "pl1", "prod", false, "")
	require.NoError(t, err)
	assert.Equal(t, OperationPipelineRun, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, "p1", plan.Target.ProjectID)
	assert.Equal(t, "pl1", plan.Target.PipelineID)
	assert.Equal(t, "prod", plan.Target.EnvName)
	assert.NotEmpty(t, plan.Fingerprint)
}

func TestPlanPipelineRunRollbackFingerprintDiffers(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo"}
	deploy, err := PlanPipelineRun(project, "pl1", "prod", false, "")
	require.NoError(t, err)
	rollback, err := PlanPipelineRun(project, "pl1", "prod", true, "v1")
	require.NoError(t, err)
	assert.NotEqual(t, deploy.Fingerprint, rollback.Fingerprint)
}

func TestPlanPipelineRunTargetCarriesArtifactVersion(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo"}
	plan, err := PlanPipelineRun(project, "pl1", "prod", true, "v1")
	require.NoError(t, err)

	targetJSON, err := json.Marshal(plan.Target)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"project_id": "p1",
		"project_name": "demo",
		"env_name": "prod",
		"pipeline_id": "pl1",
		"artifact_version": "v1"
	}`, string(targetJSON))
}

func TestPlanPipelineRunRequiresPipelineID(t *testing.T) {
	_, err := PlanPipelineRun(model.Project{ID: "p1"}, "", "prod", false, "")
	require.Error(t, err)
}

func TestPlanBrowserDebugOpenRequiresApproval(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo"}
	service := model.Service{ID: "svc-admin", Name: "admin"}
	dep := model.Deployment{ID: "dep-admin-dev", EnvName: "dev", Location: model.LocationLocal}

	plan, err := PlanBrowserDebugOpen(project, service, dep, "http://127.0.0.1:3000/")
	require.NoError(t, err)
	assert.Equal(t, OperationBrowserDebugOpen, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, RiskMedium, plan.RiskLevel)
	assert.Equal(t, "dep-admin-dev", plan.Target.DeploymentID)
	assert.Contains(t, plan.ExpectedEffects[0], "open debug browser")
}

func TestPlanCodeDebugOpenDisabledPolicy(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}}}
	svc := model.Service{ID: "s1", Name: "api", Language: model.LanguageGo}
	dep := model.Deployment{ID: "d1", EnvName: "dev", Location: model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "./cmd/api"},
		},
		CodeDebug: &model.CodeDebugConfig{Policy: model.CodeDebugPolicyDisabled}}
	plan, err := PlanCodeDebugOpen(project, svc, dep, model.LanguageGo)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Denied {
		t.Fatal("disabled policy must deny")
	}
}

func TestPlanCodeDebugOpenDevAllowed(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}}}
	svc := model.Service{ID: "s1", Name: "api", Language: model.LanguageGo}
	dep := model.Deployment{ID: "d1", EnvName: "dev", Location: model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "./cmd/api"},
		}}
	plan, err := PlanCodeDebugOpen(project, svc, dep, model.LanguageGo)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Denied {
		t.Fatalf("dev go deployment must not be denied: %v", plan.Reasons)
	}

	require.NoError(t, err)
	assert.Equal(t, OperationCodeDebugOpen, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, RiskMedium, plan.RiskLevel)
	assert.False(t, plan.Denied)
	assert.Equal(t, dep.ID, plan.Target.DeploymentID)
}

func TestPlanCodeDebugOpenLanguageRuntimeAllowed(t *testing.T) {
	project := model.Project{ID: "p1", Name: "demo",
		Environments: []model.Environment{{Name: "dev", IsDev: true}}}
	svc := model.Service{ID: "s1", Name: "api", Language: model.LanguageGo}
	dep := model.Deployment{
		ID:          "d1",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{"program": "./cmd/api"},
		},
	}

	plan, err := PlanCodeDebugOpen(project, svc, dep, model.LanguageGo)

	require.NoError(t, err)
	assert.False(t, plan.Denied)
	assert.Equal(t, OperationCodeDebugOpen, plan.Kind)
}

func TestPlanCodeDebugEvaluateDoesNotStoreExpression(t *testing.T) {
	plan, err := PlanCodeDebugEvaluate(CodeDebugEvaluateRequest{
		ProjectID:      "p1",
		ProjectName:    "demo",
		DeploymentID:   "dep-api",
		DebugSessionID: "cds_123",
		ExpressionHash: "sha256:abc",
	})

	require.NoError(t, err)
	assert.Equal(t, OperationCodeDebugEvaluate, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.NotContains(t, plan.Fingerprint, "password")
	assert.Equal(t, "cds_123", plan.Target.DebugSessionID)
}

func operationProject(isDev bool, location model.DeployLocation, readOnly bool) model.Project {
	return model.Project{
		ID:   "proj-1",
		Name: "demo",
		Environments: []model.Environment{{
			ID: "env-1", Name: "prod", IsDev: isDev, Order: 0,
		}},
		Services: []model.Service{{
			ID:        "svc-api",
			ProjectID: "proj-1",
			Name:      "api",
			Deployments: []model.Deployment{{
				ID:       "api-dev",
				EnvName:  "prod",
				Location: location,
				ReadOnly: readOnly,
			}},
		}},
	}
}
