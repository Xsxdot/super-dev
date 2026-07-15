// scenario.go 加载并校验 runtime validation 的固定 MCP 场景。
//
// 职责：
//   - 强制 manifest 成为 primary 归属的唯一来源
//   - 拒绝非精确 success、空断言、协议外壳断言和错误码白名单
//   - 稳定生成 tool 到 scenario step 的唯一 primary 映射
//
// 边界：
//   - 不执行 MCP 调用，不解析业务响应，也不产生 verdict
//   - supporting/bootstrap/provider/cleanup 调用不会被自动提升为 primary
package runtimevalidation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

var supportedAssertionOperators = map[string]struct{}{
	"equals": {}, "eq": {}, "not_equals": {}, "not_empty": {}, "array_not_empty": {},
	"contains": {}, "contains_item": {}, "not_contains": {}, "greater_than": {}, "gt": {}, "matches": {},
}

// LoadScenarios 从目录中加载、校验并按 ID 排序全部 JSON scenario。
//
// 参数：
//   - root: scenario manifest 所在目录
//
// 返回：
//   - 已通过 strict loader 合同的场景
//   - 目录读取、JSON 解析、场景合同或 primary 重复错误
//
// 注意：非 JSON 文件会被忽略；空目录不能形成 strict coverage。
func LoadScenarios(root string) ([]Scenario, error) {
	log := logger.GetLogger().WithEntryName("RuntimeValidationScenario").WithField("root", root)
	log.Info("开始加载 runtime validation 场景")
	entries, err := os.ReadDir(root)
	if err != nil {
		log.WithErr(err).Error("读取 runtime validation 场景目录失败")
		return nil, fmt.Errorf("read scenario directory: %w", err)
	}
	scenarios := make([]Scenario, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			log.WithErr(err).WithField("file", entry.Name()).Error("读取 runtime validation 场景失败")
			return nil, fmt.Errorf("read scenario %s: %w", entry.Name(), err)
		}
		var scenario Scenario
		if err := json.Unmarshal(raw, &scenario); err != nil {
			log.WithErr(err).WithField("file", entry.Name()).Error("解析 runtime validation 场景失败")
			return nil, fmt.Errorf("decode scenario %s: %w", entry.Name(), err)
		}
		if err := ValidateScenario(scenario); err != nil {
			log.WithErr(err).WithField("file", entry.Name()).Error("runtime validation 场景合同无效")
			return nil, err
		}
		scenarios = append(scenarios, scenario)
	}
	if len(scenarios) == 0 {
		return nil, fmt.Errorf("scenario directory %s contains no JSON scenarios", root)
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].ID < scenarios[j].ID })
	assignments, err := PrimaryAssignments(scenarios)
	if err != nil {
		log.WithErr(err).Error("runtime validation primary 归属无效")
		return nil, err
	}
	log.WithFields(map[string]any{"scenario_count": len(scenarios), "primary_count": len(assignments)}).Info("runtime validation 场景加载完成")
	return scenarios, nil
}

// ValidateScenario 校验单个 scenario 的结构、成功合同和断言语义。
//
// 参数：
//   - scenario: 待校验的 manifest 模型
//
// 返回：
//   - 违反 strict schema、primary、poll 或 evidence 合同时的错误
//
// 注意：primary 只能要求精确 success；策略拒绝只能作为不计 coverage 的外部诊断事实。
func ValidateScenario(scenario Scenario) error {
	if scenario.SchemaVersion != ScenarioSchemaVersion {
		return fmt.Errorf("scenario %s schema_version must be %d", scenario.ID, ScenarioSchemaVersion)
	}
	if scenario.Kind != ScenarioKind {
		return fmt.Errorf("scenario %s kind %q is not accepted", scenario.ID, scenario.Kind)
	}
	if strings.TrimSpace(scenario.ID) == "" || strings.TrimSpace(scenario.Title) == "" {
		return fmt.Errorf("scenario id and title are required")
	}
	if len(scenario.Steps) == 0 {
		return fmt.Errorf("scenario %s has no steps", scenario.ID)
	}
	seen := map[string]struct{}{}
	for _, group := range []struct {
		name  string
		steps []ScenarioStep
	}{{name: "steps", steps: scenario.Steps}, {name: "cleanup", steps: scenario.Cleanup}} {
		for _, step := range group.steps {
			if err := validateScenarioStep(scenario.ID, group.name, step, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// PrimaryAssignments 返回 scenario manifests 声明的唯一 primary 映射。
//
// 参数：
//   - scenarios: 全部已加载场景
//
// 返回：
//   - 按 tool 名排序的唯一 primary 归属
//   - 场景无效或同一工具被多个 step 声明为 primary 时的错误
//
// 注意：supporting 和 cleanup 调用不会进入返回集合。
func PrimaryAssignments(scenarios []Scenario) ([]CoverageAssignment, error) {
	assignments := make([]CoverageAssignment, 0)
	seen := map[string]CoverageAssignment{}
	for _, scenario := range scenarios {
		if err := ValidateScenario(scenario); err != nil {
			return nil, err
		}
		for _, step := range scenario.Steps {
			if step.Coverage != CoveragePrimary {
				continue
			}
			assignment := CoverageAssignment{Tool: step.Tool, ScenarioID: scenario.ID, StepID: step.ID}
			if previous, ok := seen[step.Tool]; ok {
				return nil, fmt.Errorf("duplicate primary for tool %s: %s/%s and %s/%s", step.Tool, previous.ScenarioID, previous.StepID, scenario.ID, step.ID)
			}
			seen[step.Tool] = assignment
			assignments = append(assignments, assignment)
		}
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].Tool < assignments[j].Tool })
	return assignments, nil
}

func validateScenarioStep(scenarioID, group string, step ScenarioStep, seen map[string]struct{}) error {
	if strings.TrimSpace(step.ID) == "" || strings.TrimSpace(step.Tool) == "" {
		return fmt.Errorf("scenario %s %s step id and tool are required", scenarioID, group)
	}
	if _, ok := seen[step.ID]; ok {
		return fmt.Errorf("scenario %s duplicates step %s", scenarioID, step.ID)
	}
	seen[step.ID] = struct{}{}
	if step.Coverage != CoveragePrimary && step.Coverage != CoverageSupporting {
		return fmt.Errorf("scenario %s step %s has invalid coverage %q", scenarioID, step.ID, step.Coverage)
	}
	// Cleanup 是安全回收，不是功能成功的唯一归属；禁止借清理调用补 primary coverage。
	if group == "cleanup" && step.Coverage == CoveragePrimary {
		return fmt.Errorf("scenario %s cleanup step %s cannot own primary coverage", scenarioID, step.ID)
	}
	if step.Expect.Outcome != ExpectedOutcomeSuccess {
		return fmt.Errorf("scenario %s step %s must expect exact success, got %q", scenarioID, step.ID, step.Expect.Outcome)
	}
	if len(step.Expect.AllowedErrorCodes) > 0 {
		return fmt.Errorf("scenario %s step %s cannot allow error codes", scenarioID, step.ID)
	}
	if len(step.Expect.Assertions) == 0 {
		return fmt.Errorf("scenario %s step %s has no executable assertions", scenarioID, step.ID)
	}
	for index, assertion := range step.Expect.Assertions {
		path := strings.TrimSpace(assertion.Path)
		operator := strings.TrimSpace(assertion.Operator)
		if path == "" || operator == "" {
			return fmt.Errorf("scenario %s step %s assertion %d needs path and operator", scenarioID, step.ID, index)
		}
		if _, ok := supportedAssertionOperators[operator]; !ok {
			return fmt.Errorf("scenario %s step %s assertion %d has unsupported operator %q", scenarioID, step.ID, index, operator)
		}
		if protocolShellAssertion(path) {
			return fmt.Errorf("scenario %s step %s assertion %d only checks the protocol shell", scenarioID, step.ID, index)
		}
	}
	if step.Poll != nil && (step.Poll.IntervalMilliseconds <= 0 || step.Poll.TimeoutMilliseconds <= 0) {
		return fmt.Errorf("scenario %s step %s has invalid poll contract", scenarioID, step.ID)
	}
	for _, path := range step.Evidence.Redact {
		if !strings.HasPrefix(path, "request.") && !strings.HasPrefix(path, "structuredContent.") {
			return fmt.Errorf("scenario %s step %s has ineffective redaction path %q", scenarioID, step.ID, path)
		}
	}
	return nil
}

func protocolShellAssertion(path string) bool {
	normalized := strings.ToLower(strings.TrimSpace(path))
	return normalized == "content" || normalized == "structuredcontent" || normalized == "iserror" ||
		strings.HasPrefix(normalized, "content.") || strings.HasSuffix(normalized, ".iserror")
}
