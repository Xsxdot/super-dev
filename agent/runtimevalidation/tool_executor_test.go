// tool_executor_test.go 验证 live tools/list 硬门、manifest bootstrap 与全量 primary 结果。
//
// 职责：
//   - 锁定 coverage drift 时业务 mutation 零调用
//   - 锁定 project_id 来自 manifest bootstrap capture
//   - 锁定中途失败后仍为所有 primary 产生结果行
//
// 边界：
//   - fake 只验证编排合同，不能生成 strict target PASS
package runtimevalidation

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolExecutorCoverageDriftStopsBeforeBusinessMutation(t *testing.T) {
	t.Parallel()

	scenarios := []Scenario{executorScenario("identity-observation", []ScenarioStep{
		executorStep("list-projects", "list_projects", CoveragePrimary, "structuredContent.data.count", 1),
	})}
	transport := &fakeLiveTools{tools: []string{"list_projects", "unexpected_live_tool"}}
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{
		CampaignID: "campaign-1", Scenarios: scenarios,
	})

	require.Equal(t, StatusFail, result.Status)
	require.False(t, result.Coverage.Complete)
	require.Empty(t, transport.calls, "coverage drift must stop before tools/call")
	require.Len(t, result.PrimaryRows, 1)
	require.Equal(t, StatusNotRun, result.PrimaryRows[0].Status)
}

func TestToolExecutorBootstrapsProjectBeforeCallbackAndRemainingTopology(t *testing.T) {
	t.Parallel()

	bootstrap := executorScenario("config-security-lifecycle", []ScenarioStep{
		executorStep("probe-project", "probe_project_config", CoveragePrimary, "structuredContent.data.root", "/tmp/project"),
		executorStep("upsert-project", "upsert_project_config", CoveragePrimary, "structuredContent.data.name", "campaign-project"),
		{
			ID: "resolve-project", Tool: "get_project", Coverage: CoverageSupporting,
			Arguments: map[string]any{"project_name": "{{project_name}}"},
			Expect:    StepExpectation{Outcome: ExpectedOutcomeSuccess, Assertions: []Assertion{{Path: "structuredContent.data.id", Operator: "not_empty"}}},
			Capture:   map[string]string{"project_id": "structuredContent.data.id"},
		},
		executorStep("read-project", "get_project_config", CoveragePrimary, "structuredContent.data.project_id", "project-42"),
	})
	identity := executorScenario("identity-observation", []ScenarioStep{
		executorStep("list-projects", "list_projects", CoveragePrimary, "structuredContent.data.count", 1),
	})
	transport := &fakeLiveTools{
		tools: []string{"probe_project_config", "upsert_project_config", "get_project_config", "list_projects"},
		responses: map[string]ToolCallResult{
			"probe_project_config":  successToolResult(map[string]any{"root": "/tmp/project"}),
			"upsert_project_config": successToolResult(map[string]any{"name": "campaign-project"}),
			"get_project":           successToolResult(map[string]any{"id": "project-42"}),
			"get_project_config":    successToolResult(map[string]any{"project_id": "project-42"}),
			"list_projects":         successToolResult(map[string]any{"count": 1}),
		},
	}
	callbackCalled := false
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{
		CampaignID: "campaign-1", Scenarios: []Scenario{identity, bootstrap},
		Variables: map[string]any{"project_name": "campaign-project"},
		AfterBootstrap: func(_ context.Context, variables map[string]any) error {
			callbackCalled = true
			require.Equal(t, "project-42", variables["project_id"])
			require.Equal(t, []string{"probe_project_config", "upsert_project_config", "get_project"}, transport.calls)
			return nil
		},
	})

	require.True(t, callbackCalled)
	require.Equal(t, StatusPass, result.Status)
	require.Equal(t, "project-42", result.Variables["project_id"])
	require.Equal(t, []string{"probe_project_config", "upsert_project_config", "get_project", "list_projects", "get_project_config"}, transport.calls)
	require.Len(t, result.PrimaryRows, 4)
	for _, row := range result.PrimaryRows {
		require.Equal(t, StatusPass, row.Status, row.Tool)
	}
}

func TestToolExecutorFailureStillProducesEveryPrimaryRow(t *testing.T) {
	t.Parallel()

	scenario := executorScenario("identity-observation", []ScenarioStep{
		executorStep("first", "first_tool", CoveragePrimary, "structuredContent.data.value", "wanted"),
		executorStep("second", "second_tool", CoveragePrimary, "structuredContent.data.value", "wanted"),
	})
	transport := &fakeLiveTools{
		tools:     []string{"first_tool", "second_tool"},
		responses: map[string]ToolCallResult{"first_tool": successToolResult(map[string]any{"value": "wrong"})},
	}
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{CampaignID: "campaign-1", Scenarios: []Scenario{scenario}})

	require.Equal(t, StatusFail, result.Status)
	require.Len(t, result.PrimaryRows, 2)
	require.Equal(t, StatusFail, result.PrimaryRows[0].Status)
	require.Equal(t, StatusNotRun, result.PrimaryRows[1].Status)
	require.Equal(t, []string{"first_tool"}, transport.calls)
}

func TestToolExecutorKeepsResolvablePrimaryEvidenceWhenBootstrapCallbackFails(t *testing.T) {
	t.Parallel()

	bootstrap := executorScenario("config-security-lifecycle", []ScenarioStep{
		executorStep("probe-project", "probe_project_config", CoveragePrimary, "structuredContent.data.root", "/tmp/project"),
		{
			ID: "resolve-project", Tool: "get_project", Coverage: CoverageSupporting,
			Expect:  StepExpectation{Outcome: ExpectedOutcomeSuccess, Assertions: []Assertion{{Path: "structuredContent.data.id", Operator: "not_empty"}}},
			Capture: map[string]string{"project_id": "structuredContent.data.id"},
		},
	})
	transport := &fakeLiveTools{
		tools: []string{"probe_project_config"}, responses: map[string]ToolCallResult{
			"probe_project_config": successToolResult(map[string]any{"root": "/tmp/project"}),
			"get_project":          successToolResult(map[string]any{"id": "project-42"}),
		},
	}
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{
		CampaignID: "campaign-1", Scenarios: []Scenario{bootstrap},
		AfterBootstrap: func(context.Context, map[string]any) error { return fmt.Errorf("provider failed") },
	})

	require.Equal(t, StatusFail, result.Status)
	require.Len(t, result.PrimaryEvidence, 1)
	require.Equal(t, "evidence/tool-campaign.json#/scenarios/0/steps/0/recorded_evidence", result.PrimaryEvidence[0].EvidenceRef)
}

func TestToolExecutorPersistsOnlySelectedRedactedCorrelatedEvidence(t *testing.T) {
	t.Parallel()

	step := executorStep("inspect", "inspect_tool", CoveragePrimary, "structuredContent.data.resource_id", "resource-7")
	step.Arguments = map[string]any{"project_id": "project-42", "authorization": "one-time-secret"}
	step.Evidence = EvidenceContract{
		Record: []string{"structuredContent.data.resource_id", "request.arguments"},
		Redact: []string{"request.arguments.authorization"},
		Forbid: []string{"one-time-secret", "authorization"},
	}
	transport := &fakeLiveTools{
		tools: []string{"inspect_tool"},
		responses: map[string]ToolCallResult{"inspect_tool": successToolResult(map[string]any{
			"resource_id": "resource-7", "ignored": "must-not-persist",
		})},
	}
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{
		CampaignID: "campaign-1", Scenarios: []Scenario{executorScenario("identity-observation", []ScenarioStep{step})},
	})

	require.Equal(t, StatusPass, result.Status)
	require.Len(t, result.Scenarios, 1)
	execution := result.Scenarios[0].Steps[0]
	require.Equal(t, map[string]any{"paths": map[string]any{
		"structuredContent.data.resource_id": "resource-7",
		"request.arguments":                  map[string]any{"project_id": "project-42"},
	}}, execution.RecordedEvidence)
	require.NotContains(t, fmt.Sprint(execution.RecordedEvidence), "must-not-persist")
	require.Equal(t, "project-42", result.PrimaryEvidence[0].ResourceID)
	require.Equal(t, "evidence/tool-campaign.json#/scenarios/0/steps/0/recorded_evidence", result.PrimaryEvidence[0].EvidenceRef)
}

func TestToolExecutorReportsCommittedMutationBeforeEvidenceFailure(t *testing.T) {
	t.Parallel()

	step := executorStep("deploy", "deploy_project_pipeline", CoveragePrimary, "structuredContent.data.run.id", "run-1")
	step.Evidence.Record = []string{"structuredContent.data.missing"}
	transport := &fakeLiveTools{
		tools: []string{"deploy_project_pipeline"},
		responses: map[string]ToolCallResult{"deploy_project_pipeline": successToolResult(map[string]any{
			"run": map[string]any{"id": "run-1"},
		})},
	}
	committed := false
	result := NewToolExecutor(transport, transport).Run(context.Background(), ToolCampaignRequest{
		CampaignID: "campaign-1", Scenarios: []Scenario{executorScenario("remote-pipeline", []ScenarioStep{step})},
		OnMutationCommitted: func(tool string, _ map[string]any, _ ToolCallResult, _ map[string]any) {
			committed = tool == "deploy_project_pipeline"
		},
	})

	require.True(t, committed)
	require.Equal(t, StatusFail, result.Status)
}

type fakeLiveTools struct {
	tools     []string
	responses map[string]ToolCallResult
	calls     []string
}

func (f *fakeLiveTools) ListTools(context.Context) ([]string, error) {
	return append([]string{}, f.tools...), nil
}

func (f *fakeLiveTools) CallTool(_ context.Context, name string, _ map[string]any) (ToolCallResult, error) {
	f.calls = append(f.calls, name)
	result, ok := f.responses[name]
	if !ok {
		return ToolCallResult{}, fmt.Errorf("unexpected tool %s", name)
	}
	return result, nil
}

func executorScenario(id string, steps []ScenarioStep) Scenario {
	return Scenario{SchemaVersion: ScenarioSchemaVersion, Kind: ScenarioKind, ID: id, Title: id, Steps: steps}
}

func executorStep(id, tool, coverage, path string, value any) ScenarioStep {
	return ScenarioStep{
		ID: id, Tool: tool, Coverage: coverage,
		Expect:   StepExpectation{Outcome: ExpectedOutcomeSuccess, Assertions: []Assertion{{Path: path, Operator: "eq", Value: value}}},
		Evidence: EvidenceContract{Record: []string{path}},
	}
}

func successToolResult(data map[string]any) ToolCallResult {
	return ToolCallResult{StructuredContent: map[string]any{"ok": true, "data": data}}
}
