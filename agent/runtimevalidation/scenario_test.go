// scenario_test.go 验证 strict 场景只能声明真实成功且具有业务语义的 MCP primary。
//
// 职责：
//   - 锁定 scenario manifest 对 primary 归属的唯一控制权
//   - 拒绝 policy denial、空断言和仅验证协议外壳的断言
//
// 边界：
//   - 不启动 MCP 进程，也不执行场景步骤
package runtimevalidation

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateScenarioRejectsPrimaryWithoutStrictSuccessEvidence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ScenarioStep)
	}{
		{
			name: "policy denial outcome",
			mutate: func(step *ScenarioStep) {
				step.Expect.Outcome = "success_or_policy_denied"
			},
		},
		{
			name: "empty assertions",
			mutate: func(step *ScenarioStep) {
				step.Expect.Assertions = nil
			},
		},
		{
			name: "protocol shell assertion",
			mutate: func(step *ScenarioStep) {
				step.Expect.Assertions = []Assertion{{Path: "content", Operator: "not_empty"}}
			},
		},
		{
			name: "error code allowlist",
			mutate: func(step *ScenarioStep) {
				step.Expect.AllowedErrorCodes = []string{"operation_denied"}
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			scenario := validScenario("list_projects")
			test.mutate(&scenario.Steps[0])
			require.Error(t, ValidateScenario(scenario))
		})
	}
}

func TestValidateScenarioAcceptsSupportingPreparationWithoutPrimaryPromotion(t *testing.T) {
	t.Parallel()

	scenario := validScenario("list_projects")
	scenario.Steps = append([]ScenarioStep{{
		ID:       "prepare",
		Tool:     "probe_project_config",
		Coverage: CoverageSupporting,
		Expect: StepExpectation{
			Outcome:    ExpectedOutcomeSuccess,
			Assertions: []Assertion{{Path: "structuredContent.plan_id", Operator: "not_empty"}},
		},
	}}, scenario.Steps...)

	require.NoError(t, ValidateScenario(scenario))
	assignments, err := PrimaryAssignments([]Scenario{scenario})
	require.NoError(t, err)
	require.Equal(t, []CoverageAssignment{{
		Tool: "list_projects", ScenarioID: "identity", StepID: "primary",
	}}, assignments)
}

func TestPrimaryAssignmentsRejectsDuplicatePrimary(t *testing.T) {
	t.Parallel()

	first := validScenario("list_projects")
	second := validScenario("list_projects")
	second.ID = "identity-copy"

	_, err := PrimaryAssignments([]Scenario{first, second})
	require.ErrorContains(t, err, "duplicate primary")
}

func TestRemotePipelineBindsGovernanceIdentityToLiveHost(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	var assertionValue map[string]any
	for _, scenario := range scenarios {
		if scenario.ID != "remote-pipeline" {
			continue
		}
		for _, step := range scenario.Steps {
			if step.ID != "pipeline-host-id-preflight" {
				continue
			}
			for _, assertion := range step.Expect.Assertions {
				if assertion.Path == "structuredContent.data.remote_hosts" {
					assertionValue, _ = assertion.Value.(map[string]any)
				}
			}
		}
	}
	require.Equal(t, "{{expected_remote_identity}}", assertionValue["node_id"])
}

func TestRepositoryPreviewExecutionRecordsReturnedPreviewPath(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	var record []string
	for _, scenario := range scenarios {
		if scenario.ID != "config-security-lifecycle" {
			continue
		}
		for _, step := range scenario.Steps {
			if step.ID == "preview-go-execution" {
				record = step.Evidence.Record
			}
		}
	}
	require.Equal(t, []string{"structuredContent.data.preview"}, record)
}

func TestRepositoryLifecyclePollsExactDeploymentIdentity(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	wantSteps := map[string]bool{
		"wait-go-running":   true,
		"wait-go-restarted": true,
		"wait-go-stopped":   true,
	}
	for _, scenario := range scenarios {
		if scenario.ID != "config-security-lifecycle" {
			continue
		}
		for _, step := range scenario.Steps {
			if !wantSteps[step.ID] {
				continue
			}
			require.Equal(t, "diagnose_service", step.Tool, step.ID)
			require.Equal(t, "{{go_deployment_id}}", step.Arguments["deployment_id"], step.ID)
			require.Equal(t, "structuredContent.data.status", step.Expect.Assertions[0].Path, step.ID)
			require.Equal(t, []string{
				"structuredContent.data.status",
				"structuredContent.data.target.deployment.id",
			}, step.Evidence.Record, step.ID)
			delete(wantSteps, step.ID)
		}
	}
	require.Empty(t, wantSteps)
}

func TestRepositoryApprovalReadUsesUnapprovedPendingProbe(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	var listStep, getStep, auditStep ScenarioStep
	for _, scenario := range scenarios {
		if scenario.ID != "config-security-lifecycle" {
			continue
		}
		for _, step := range scenario.Steps {
			switch step.ID {
			case "list-operation-approvals":
				listStep = step
			case "get-operation-approval":
				getStep = step
			case "list-operation-audit":
				auditStep = step
			}
		}
	}
	require.Equal(t, "pending", listStep.Arguments["status"])
	require.NotContains(t, listStep.Arguments, "project_id")
	require.Equal(t, "{{approval_probe_id}}", getStep.Arguments["approval_id"])
	require.Len(t, getStep.Expect.Assertions, 2)
	require.Equal(t, "structuredContent.data.approval.status", getStep.Expect.Assertions[1].Path)
	require.Equal(t, "pending", getStep.Expect.Assertions[1].Value)
	require.Contains(t, auditStep.Evidence.Forbid, "approval_token")
	require.NotContains(t, auditStep.Evidence.Forbid, "token")
}

func validScenario(tool string) Scenario {
	return Scenario{
		SchemaVersion: ScenarioSchemaVersion,
		Kind:          ScenarioKind,
		ID:            "identity",
		Title:         "Identity",
		Steps: []ScenarioStep{{
			ID:       "primary",
			Tool:     tool,
			Coverage: CoveragePrimary,
			Expect: StepExpectation{
				Outcome:    ExpectedOutcomeSuccess,
				Assertions: []Assertion{{Path: "structuredContent.projects", Operator: "array_not_empty"}},
			},
			Evidence: EvidenceContract{Record: []string{"structuredContent.projects"}},
		}},
	}
}
