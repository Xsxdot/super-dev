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
	"os"
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

func TestRepositoryRemotePipelineUsesPreparedArtifactPaths(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	want := map[string]string{
		"pipeline-config-validate":  "a",
		"pipeline-deploy-a":         "a",
		"pipeline-deploy-b":         "b",
		"pipeline-rollback-a":       "a",
		"pipeline-cleanup":          "a",
		"pipeline-cleanup-on-abort": "a",
	}
	for _, scenario := range scenarios {
		if scenario.ID != "remote-pipeline" {
			continue
		}
		for _, step := range append(scenario.Steps, scenario.Cleanup...) {
			version, ok := want[step.ID]
			if !ok {
				continue
			}
			variables, ok := step.Arguments["variables"].(map[string]any)
			require.True(t, ok, step.ID)
			require.Equal(t, "{{pipeline_artifact_path_"+version+"}}", variables["artifact_path"], step.ID)
			require.Equal(t, "{{pipeline_artifact_checksum_path_"+version+"}}", variables["artifact_checksum_path"], step.ID)
			require.NotContains(t, variables, "artifact_source", step.ID)
			require.NotContains(t, variables, "remote_release_script", step.ID)
			delete(want, step.ID)
		}
	}
	require.Empty(t, want)
}

func TestRepositoryRemotePipelineTemplateEmitsStrictOracleLogs(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("..", "..", "validation", "runtime", "pipeline", "templates", "remote-validation-deploy.yaml"))
	require.NoError(t, err)
	text := string(raw)
	require.Contains(t, text, `"stage":"preflight_root","root_mode":"%s","root_state":"%s"`)
	require.Contains(t, text, `"stage":"verify_transfer_digest","outcome":"success"`)
	require.Contains(t, text, `test "$(wc -l <"$root/.campaign-owner")" -eq 1`)
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

func TestRepositoryCodeDebugObservesExactGoDeploymentIdentity(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	wantSteps := map[string]string{
		"code-runtime-before":        "running",
		"code-runtime-after":         "running",
		"wait-go-stopped-after-code": "stopped",
	}
	for _, scenario := range scenarios {
		if scenario.ID != "code-debug" {
			continue
		}
		for _, step := range append(scenario.Steps, scenario.Cleanup...) {
			wantStatus, wanted := wantSteps[step.ID]
			if !wanted {
				continue
			}
			require.Equal(t, "diagnose_service", step.Tool, step.ID)
			require.Equal(t, "{{go_deployment_id}}", step.Arguments["deployment_id"], step.ID)
			require.Equal(t, "structuredContent.data.target.deployment.id", step.Expect.Assertions[0].Path, step.ID)
			require.Equal(t, "structuredContent.data.status", step.Expect.Assertions[1].Path, step.ID)
			require.Equal(t, wantStatus, step.Expect.Assertions[1].Value, step.ID)
			delete(wantSteps, step.ID)
		}
	}
	require.Empty(t, wantSteps)
}

func TestRepositoryCodeDebugMatchesVariableObjectsByStableName(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	wantSteps := map[string]bool{
		"code-capture-at":     true,
		"code-inspect-paused": true,
		"code-variables":      true,
	}
	for _, scenario := range scenarios {
		if scenario.ID != "code-debug" {
			continue
		}
		for _, step := range scenario.Steps {
			if !wantSteps[step.ID] {
				continue
			}
			variablesAssertion := step.Expect.Assertions[len(step.Expect.Assertions)-1]
			require.Equal(t, "contains_item", variablesAssertion.Operator, step.ID)
			require.Equal(t, map[string]any{"name": "fixtureMarker"}, variablesAssertion.Value, step.ID)
			delete(wantSteps, step.ID)
		}
	}
	require.Empty(t, wantSteps)
}

func TestRepositoryCodeDebugEvidenceOnlyRequiresStableSuccessFields(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	for _, scenario := range scenarios {
		if scenario.ID != "code-debug" {
			continue
		}
		breakpointSteps := 0
		readSteps := 0
		evaluateSteps := 0
		for _, step := range append(scenario.Steps, scenario.Cleanup...) {
			switch step.Tool {
			case "set_debug_breakpoints":
				breakpointSteps++
				require.NotContains(t, step.Evidence.Record, "structuredContent.data.result.session_id", step.ID)
				require.Contains(t, step.Evidence.Record, "structuredContent.data.result.lease_created", step.ID)
			case "debug_stack_trace", "debug_scopes", "debug_variables":
				readSteps++
				require.NotContains(t, step.Evidence.Record, "structuredContent.data.result.session_id", step.ID)
				require.Contains(t, step.Evidence.Record, "structuredContent.data.result.lease_created", step.ID)
			case "debug_evaluate":
				evaluateSteps++
				require.NotContains(t, step.Evidence.Record, "structuredContent.code", step.ID)
				require.NotContains(t, step.Evidence.Record, "structuredContent.message", step.ID)
				require.NotContains(t, step.Evidence.Record, "structuredContent.data.result.session_id", step.ID)
			}
		}
		require.Equal(t, 3, breakpointSteps)
		require.Equal(t, 3, readSteps)
		require.Equal(t, 1, evaluateSteps)
		return
	}
	t.Fatal("code-debug scenario is absent")
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

func TestRepositoryBrowserScenarioStartsFixtureBeforeOpeningSession(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	for _, scenario := range scenarios {
		if scenario.ID != "browser-debug" {
			continue
		}
		stepIndex := map[string]int{}
		for index, step := range scenario.Steps {
			stepIndex[step.ID] = index
		}
		require.Contains(t, stepIndex, "browser-start-go")
		require.Contains(t, stepIndex, "browser-wait-go-running")
		require.Contains(t, stepIndex, "browser-open-session")
		require.Less(t, stepIndex["browser-start-go"], stepIndex["browser-open-session"])
		require.Less(t, stepIndex["browser-wait-go-running"], stepIndex["browser-open-session"])
		require.Equal(t, "start_service", scenario.Steps[stepIndex["browser-start-go"]].Tool)
		require.Equal(t, CoverageSupporting, scenario.Steps[stepIndex["browser-start-go"]].Coverage)
		require.Equal(t, "diagnose_service", scenario.Steps[stepIndex["browser-wait-go-running"]].Tool)
		require.Equal(t, "{{go_deployment_id}}", scenario.Steps[stepIndex["browser-wait-go-running"]].Arguments["deployment_id"])

		cleanupByID := map[string]ScenarioStep{}
		for _, step := range scenario.Cleanup {
			cleanupByID[step.ID] = step
		}
		stop := cleanupByID["browser-stop-go-on-abort"]
		require.Equal(t, "stop_service", stop.Tool)
		require.Equal(t, "variable_set:project_id&&primary_step_not_passed:browser-close-session", stop.RunIf)
		return
	}
	t.Fatal("browser-debug scenario is absent")
}

func TestRepositoryBrowserEvidenceOnlyRequiresSuccessPayloadFields(t *testing.T) {
	t.Parallel()

	scenarios, err := LoadScenarios(filepath.Join("..", "..", "validation", "runtime", "scenarios"))
	require.NoError(t, err)
	for _, scenario := range scenarios {
		if scenario.ID != "browser-debug" {
			continue
		}
		foundSnapshot := false
		foundEvaluate := false
		foundScreenshot := false
		for _, step := range scenario.Steps {
			switch step.ID {
			case "browser-snapshot-result":
				foundSnapshot = true
				require.Contains(t, step.Evidence.Record, "structuredContent.data.snapshot.text")
				require.NotContains(t, step.Evidence.Record, "structuredContent.data.snapshot.elements")
			case "browser-evaluate-policy":
				foundEvaluate = true
				require.Contains(t, step.Evidence.Record, "structuredContent.data.result.session_id")
				require.NotContains(t, step.Evidence.Record, "structuredContent.code")
				require.NotContains(t, step.Evidence.Record, "structuredContent.message")
			case "browser-screenshot-safe-page":
				foundScreenshot = true
				require.Contains(t, step.Evidence.Record, "sha256:structuredContent.data.screenshot.data_base64")
				require.Contains(t, step.Evidence.Redact, "structuredContent.data.screenshot.data_base64")
				require.NotContains(t, step.Evidence.Record, "structuredContent.data.screenshot.data_base64")
			}
		}
		require.True(t, foundSnapshot, "browser-snapshot-result step is absent")
		require.True(t, foundEvaluate, "browser-evaluate-policy step is absent")
		require.True(t, foundScreenshot, "browser-screenshot-safe-page step is absent")
		return
	}
	t.Fatal("browser-debug scenario is absent")
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
