// manifest_test.go 验证固定场景的工具归属门禁。
//
// 职责：
//   - 拒绝缺失、重复和额外的 primary MCP 工具归属
//
// 边界：
//   - supporting/cleanup 调用不改变唯一归属
package windowsvalidation

import "testing"

func TestValidateCoverageRequiresExactPrimaryAssignment(t *testing.T) {
	t.Parallel()
	frozen := []string{"alpha", "beta"}
	scenarios := []Scenario{{
		ID: "one",
		Steps: []ScenarioStep{
			{ID: "a", Tool: "alpha", Coverage: CoveragePrimary},
			{ID: "b", Tool: "beta", Coverage: CoveragePrimary},
			{ID: "b-again", Tool: "beta", Coverage: CoverageSupporting},
		},
	}}
	if _, err := ValidateCoverage(frozen, scenarios); err != nil {
		t.Fatalf("valid coverage rejected: %v", err)
	}

	scenarios[0].Steps[2].Coverage = CoveragePrimary
	if _, err := ValidateCoverage(frozen, scenarios); err == nil {
		t.Fatal("duplicate primary assignment should fail")
	}
}

func TestValidateScenarioRejectsGenericCommandExecution(t *testing.T) {
	t.Parallel()
	scenario := Scenario{
		SchemaVersion: 1,
		Kind:          ScenarioKind,
		ID:            "fixed",
		Steps:         []ScenarioStep{{ID: "bad", Tool: "", Coverage: CoveragePrimary}},
	}
	if err := ValidateScenario(scenario); err == nil {
		t.Fatal("step without MCP tool should fail")
	}
}
