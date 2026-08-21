// manifest.go 校验冻结场景、工具归属与包源资产。
//
// 职责：
//   - 保证场景仅表达固定 MCP 调用
//   - 保证冻结 MCP primary 工具归属无缺失、重复和额外项

// 边界：
//   - supporting 与 cleanup 调用不改变主覆盖归属
package windowsvalidation

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateScenario 校验一个固定 MCP 场景的结构和安全边界。
func ValidateScenario(scenario Scenario) error {
	if scenario.SchemaVersion != 1 {
		return fmt.Errorf("scenario %s schema_version must be 1", scenario.ID)
	}
	if scenario.Kind != ScenarioKind {
		return fmt.Errorf("scenario %s kind %q is not accepted", scenario.ID, scenario.Kind)
	}
	if strings.TrimSpace(scenario.ID) == "" {
		return fmt.Errorf("scenario id is required")
	}
	seen := map[string]struct{}{}
	steps := append(append([]ScenarioStep{}, scenario.Steps...), scenario.Cleanup...)
	for _, step := range steps {
		if strings.TrimSpace(step.ID) == "" {
			return fmt.Errorf("scenario %s step id is required", scenario.ID)
		}
		if _, ok := seen[step.ID]; ok {
			return fmt.Errorf("scenario %s duplicates step %s", scenario.ID, step.ID)
		}
		seen[step.ID] = struct{}{}
		// 固定场景只允许 MCP tool；禁止把任意 command/shell 重新塞回一次性驱动。
		if strings.TrimSpace(step.Tool) == "" {
			return fmt.Errorf("scenario %s step %s must call an MCP tool", scenario.ID, step.ID)
		}
		if step.Coverage != CoveragePrimary && step.Coverage != CoverageSupporting {
			return fmt.Errorf("scenario %s step %s has invalid coverage %q", scenario.ID, step.ID, step.Coverage)
		}
		switch step.Expect.Outcome {
		case "success", "success_or_policy_denied":
		default:
			return fmt.Errorf("scenario %s step %s has invalid outcome %q", scenario.ID, step.ID, step.Expect.Outcome)
		}
		if step.Poll != nil && (step.Poll.IntervalSeconds <= 0 || step.Poll.TimeoutSeconds <= 0) {
			return fmt.Errorf("scenario %s step %s has invalid poll contract", scenario.ID, step.ID)
		}
		for _, assertion := range step.Expect.Assertions {
			if strings.TrimSpace(assertion.Path) == "" {
				return fmt.Errorf("scenario %s step %s has an assertion without path", scenario.ID, step.ID)
			}
			switch assertion.Operator {
			case "equals", "eq", "not_empty", "contains", "contains_item", "not_contains", "greater_than", "gt":
			default:
				return fmt.Errorf("scenario %s step %s has unsupported assertion operator %q", scenario.ID, step.ID, assertion.Operator)
			}
		}
		for _, path := range step.Evidence.Redact {
			if !strings.HasPrefix(path, "request.") && !strings.HasPrefix(path, "structuredContent.") {
				return fmt.Errorf("scenario %s step %s has ineffective evidence redaction path %q", scenario.ID, step.ID, path)
			}
		}
	}
	return nil
}

// ValidateCoverage 验证冻结工具集合在场景 primary steps 中恰好出现一次。
func ValidateCoverage(frozen []string, scenarios []Scenario) ([]CoverageAssignment, error) {
	wanted := map[string]struct{}{}
	for _, tool := range frozen {
		if _, exists := wanted[tool]; exists {
			return nil, fmt.Errorf("frozen tool %s is duplicated", tool)
		}
		wanted[tool] = struct{}{}
	}
	assignments := make([]CoverageAssignment, 0, len(frozen))
	counts := map[string]int{}
	for _, scenario := range scenarios {
		for _, step := range scenario.Steps {
			if step.Coverage != CoveragePrimary {
				continue
			}
			counts[step.Tool]++
			assignments = append(assignments, CoverageAssignment{Tool: step.Tool, ScenarioID: scenario.ID, StepID: step.ID})
		}
	}
	missing := []string{}
	extra := []string{}
	duplicate := []string{}
	for tool := range wanted {
		if counts[tool] == 0 {
			missing = append(missing, tool)
		}
	}
	for tool, count := range counts {
		if _, ok := wanted[tool]; !ok {
			extra = append(extra, tool)
		}
		if count > 1 {
			duplicate = append(duplicate, tool)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	sort.Strings(duplicate)
	if len(missing) > 0 || len(extra) > 0 || len(duplicate) > 0 || len(assignments) != len(frozen) {
		return nil, fmt.Errorf("MCP primary coverage mismatch: frozen=%d assigned=%d missing=%v extra=%v duplicate=%v", len(frozen), len(assignments), missing, extra, duplicate)
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].Tool < assignments[j].Tool })
	return assignments, nil
}

func buildValidationCatalog(scenarios []Scenario, coverage []CoverageAssignment) ValidationCatalog {
	catalog := ValidationCatalog{Coverage: append([]CoverageAssignment{}, coverage...)}
	for _, scenario := range orderedScenarios(scenarios) {
		entry := ScenarioCatalogEntry{ID: scenario.ID, Title: scenario.Title}
		for _, step := range scenario.Steps {
			entry.Steps = append(entry.Steps, StepCatalogEntry{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage})
		}
		for _, step := range scenario.Cleanup {
			entry.Cleanup = append(entry.Cleanup, StepCatalogEntry{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage})
		}
		catalog.Scenarios = append(catalog.Scenarios, entry)
	}
	return catalog
}
