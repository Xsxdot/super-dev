// tool_executor.go 按 manifest 拓扑执行全部 live MCP 工具场景。
//
// 职责：
//   - 在任何业务 mutation 前执行 live tools/list 与 primary exact coverage 硬门
//   - 执行 project bootstrap、参数渲染、轮询、断言、capture 与 guarded cleanup
//   - 即使中途失败，也为 live tool 全集生成可审计的 primary 结果行
//
// 边界：
//   - 不经 HTTP 注册 project，project_id 只能来自 manifest bootstrap capture
//   - 不将 policy denial、isError=true、ok=false 或空断言当作 coverage PASS
//   - 不把 supporting/provider/cleanup 调用自动提升为 primary
package runtimevalidation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
)

// ToolCampaignRequest 提交一次 live MCP campaign 的 manifest、运行时变量和 bootstrap 边界。
type ToolCampaignRequest struct {
	CampaignID          string
	Scenarios           []Scenario
	Variables           map[string]any
	AfterBootstrap      func(context.Context, map[string]any) error
	OnMutationCommitted func(tool string, arguments map[string]any, response ToolCallResult, variables map[string]any)
	OnStepPassed        func(scenarioID, stepID string, variables map[string]any)
}

// ToolPrimaryRow 表示一个 live tool 唯一 primary step 的终态。
type ToolPrimaryRow struct {
	Tool       string        `json:"tool"`
	ScenarioID string        `json:"scenario_id"`
	StepID     string        `json:"step_id"`
	Status     Status        `json:"status"`
	Cause      Cause         `json:"cause,omitempty"`
	Evidence   *ToolEvidence `json:"evidence,omitempty"`
}

// ToolStepExecution 保存一次 manifest step 的调用、断言与重试结果。
type ToolStepExecution struct {
	StepID           string         `json:"step_id"`
	Tool             string         `json:"tool"`
	Coverage         string         `json:"coverage"`
	Status           Status         `json:"status"`
	Cause            Cause          `json:"cause,omitempty"`
	Attempts         int            `json:"attempts"`
	Evidence         ToolEvidence   `json:"evidence"`
	RecordedEvidence map[string]any `json:"recorded_evidence,omitempty"`
}

// ToolScenarioExecution 保存一个场景主步骤与 guarded cleanup 的聚合状态。
type ToolScenarioExecution struct {
	ID      string              `json:"id"`
	Status  Status              `json:"status"`
	Cause   Cause               `json:"cause,omitempty"`
	Steps   []ToolStepExecution `json:"steps"`
	Cleanup []ToolStepExecution `json:"cleanup"`
}

// ToolCampaignResult 保存动态 coverage、场景、primary 行和内存 capture。
type ToolCampaignResult struct {
	Status          Status                  `json:"status"`
	Cause           Cause                   `json:"cause,omitempty"`
	LiveTools       []string                `json:"live_tools"`
	Coverage        CoverageReport          `json:"coverage"`
	Scenarios       []ToolScenarioExecution `json:"scenarios"`
	PrimaryRows     []ToolPrimaryRow        `json:"primary_rows"`
	PrimaryEvidence []ToolEvidence          `json:"primary_evidence"`
	Variables       map[string]any          `json:"-"`
}

// ToolExecutor 在同一 packaged MCP session 上执行 strict scenario topology。
type ToolExecutor struct {
	lister ToolLister
	caller ToolCaller
}

// NewToolExecutor 创建共享一个 live MCP 会话的 executor。
func NewToolExecutor(lister ToolLister, caller ToolCaller) *ToolExecutor {
	return &ToolExecutor{lister: lister, caller: caller}
}

// Run 先执行 exact coverage，再按 bootstrap 和业务拓扑执行场景。
//
// 参数：
//   - ctx: campaign 总期限
//   - request: campaign、manifest、运行时变量、mutation hook 和 bootstrap callback
//
// 返回：
//   - 始终包含全量 primary 行的 campaign 结果
//
// 注意：coverage drift 是产品/资产 FAIL，并且保证 tools/call 零调用。
func (e *ToolExecutor) Run(ctx context.Context, request ToolCampaignRequest) ToolCampaignResult {
	result := ToolCampaignResult{Status: StatusNotRun, Variables: cloneVariables(request.Variables)}
	// executor 必须在同一张内存变量表上完成 bootstrap capture、provider callback 和后续场景渲染。
	request.Variables = result.Variables
	log := logger.GetLogger().WithEntryName("RuntimeValidationToolExecutor").WithField("campaign_id", request.CampaignID)
	if e == nil || e.lister == nil || e.caller == nil || strings.TrimSpace(request.CampaignID) == "" {
		result.Status = StatusFail
		result.Cause = Cause{Code: "tool_executor_input_invalid", Message: "campaign_id, lister and caller are required", Source: "tool-executor"}
		return result
	}
	log.Info("开始 live MCP tools/list coverage 硬门")
	liveTools, err := e.lister.ListTools(ctx)
	result.LiveTools = append([]string{}, liveTools...)
	if err != nil {
		result.Status = StatusFail
		result.Cause = Cause{Code: "tools_list_failed", Message: err.Error(), Source: "coverage"}
		result.PrimaryRows = primaryRowsNotRun(request.Scenarios, result.Cause)
		return result
	}
	coverage, err := CompareCoverage(liveTools, request.Scenarios)
	result.Coverage = coverage
	if err != nil || !coverage.Complete {
		message := "live tools/list does not exactly match scenario primary assignments"
		if err != nil {
			message = err.Error()
		}
		result.Status = StatusFail
		result.Cause = Cause{Code: "live_tool_coverage_drift", Message: message, Source: "coverage"}
		result.PrimaryRows = primaryRowsNotRun(request.Scenarios, result.Cause)
		log.WithFields(map[string]any{"live_tool_count": coverage.LiveToolCount, "primary_count": coverage.PrimaryCount}).Error("coverage drift 阻止业务 mutation")
		return result
	}

	ordered, bootstrapIndex := orderToolScenarios(request.Scenarios)
	passed := map[string]bool{}
	var bootstrapScenario *ToolScenarioExecution
	var bootstrapRemainder []ScenarioStep
	if bootstrapIndex >= 0 {
		scenario := ordered[bootstrapIndex]
		if err := mergeScenarioVariables(scenario, result.Variables); err != nil {
			result.Status = StatusBlocked
			result.Cause = Cause{Code: "scenario_variables_missing", Message: err.Error(), Source: scenario.ID}
			result.Scenarios = allScenariosNotRun(ordered, result.Cause)
			finalizeToolPrimaryEvidence(&result, coverage.Assignments)
			return result
		}
		boundary := projectBootstrapBoundary(scenario)
		if boundary >= 0 {
			prefix := scenario
			prefix.Steps = append([]ScenarioStep{}, scenario.Steps[:boundary+1]...)
			bootstrapRemainder = append([]ScenarioStep{}, scenario.Steps[boundary+1:]...)
			execution := e.executeScenario(ctx, request, prefix, passed)
			bootstrapScenario = &execution
			if execution.Status != StatusPass || strings.TrimSpace(fmt.Sprint(result.Variables["project_id"])) == "" {
				cause := execution.Cause
				if cause.Code == "" {
					cause = Cause{Code: "project_bootstrap_incomplete", Message: "manifest bootstrap did not capture project_id", Source: scenario.ID}
				}
				result.Status = execution.Status
				if result.Status == StatusPass {
					result.Status = StatusFail
				}
				result.Cause = cause
				result.Scenarios = append(result.Scenarios, execution)
				result.Scenarios = append(result.Scenarios, scenariosNotRunExcept(ordered, scenario.ID, cause)...)
				finalizeToolPrimaryEvidence(&result, coverage.Assignments)
				return result
			}
			if request.AfterBootstrap != nil {
				log.WithField("project_id", result.Variables["project_id"]).Info("manifest project bootstrap 完成，进入 provider 边界")
				if err := request.AfterBootstrap(ctx, result.Variables); err != nil {
					result.Status = StatusFail
					result.Cause = Cause{Code: "after_bootstrap_failed", Message: err.Error(), Source: "provider-matrix"}
					result.Scenarios = append(result.Scenarios, execution)
					result.Scenarios = append(result.Scenarios, scenariosNotRunExcept(ordered, scenario.ID, result.Cause)...)
					finalizeToolPrimaryEvidence(&result, coverage.Assignments)
					return result
				}
			}
		}
	}

	result.Status = StatusPass
	globalBlockingScenario := ""
	for _, scenario := range ordered {
		if globalBlockingScenario != "" {
			cause := Cause{Code: "upstream_scenario_not_passed", Message: "not run after scenario " + globalBlockingScenario, Source: globalBlockingScenario}
			execution := scenarioNotRun(scenario, cause, StatusNotRun)
			result.Scenarios = append(result.Scenarios, execution)
			continue
		}
		if err := mergeScenarioVariables(scenario, result.Variables); err != nil {
			execution := scenarioNotRun(scenario, Cause{Code: "scenario_variables_missing", Message: err.Error(), Source: scenario.ID}, StatusBlocked)
			result.Scenarios = append(result.Scenarios, execution)
			if result.Status == StatusPass {
				result.Status, result.Cause = StatusBlocked, execution.Cause
			}
			globalBlockingScenario = scenario.ID
			continue
		}
		if bootstrapScenario != nil && scenario.ID == bootstrapScenario.ID {
			if len(bootstrapRemainder) == 0 {
				result.Scenarios = append(result.Scenarios, *bootstrapScenario)
				continue
			}
			remainder := scenario
			remainder.Steps = bootstrapRemainder
			execution := e.executeScenario(ctx, request, remainder, passed)
			combined := combineScenarioExecutions(*bootstrapScenario, execution)
			result.Scenarios = append(result.Scenarios, combined)
			if combined.Status != StatusPass && result.Status == StatusPass {
				result.Status, result.Cause = combined.Status, combined.Cause
			}
			if combined.Status != StatusPass {
				globalBlockingScenario = scenario.ID
			}
			continue
		}
		execution := e.executeScenario(ctx, request, scenario, passed)
		result.Scenarios = append(result.Scenarios, execution)
		if execution.Status != StatusPass && result.Status == StatusPass {
			result.Status, result.Cause = execution.Status, execution.Cause
		}
		if execution.Status != StatusPass {
			globalBlockingScenario = scenario.ID
		}
	}
	finalizeToolPrimaryEvidence(&result, coverage.Assignments)
	for _, row := range result.PrimaryRows {
		if row.Status != StatusPass && result.Status == StatusPass {
			result.Status, result.Cause = StatusFail, row.Cause
		}
	}
	log.WithFields(map[string]any{"status": result.Status, "primary_count": len(result.PrimaryRows)}).Info("live MCP scenario campaign 执行完成")
	return result
}

func (e *ToolExecutor) executeScenario(ctx context.Context, request ToolCampaignRequest, scenario Scenario, passed map[string]bool) ToolScenarioExecution {
	execution := ToolScenarioExecution{ID: scenario.ID, Status: StatusPass}
	log := logger.GetLogger().WithEntryName("RuntimeValidationToolScenario").WithFields(map[string]any{"campaign_id": request.CampaignID, "scenario": scenario.ID})
	log.WithField("step_count", len(scenario.Steps)).Info("开始执行 live MCP 场景")
	blocking := ""
	for _, step := range scenario.Steps {
		if blocking != "" {
			cause := Cause{Code: "upstream_step_not_passed", Message: "not run after " + blocking, Source: blocking}
			execution.Steps = append(execution.Steps, notRunStep(request.CampaignID, scenario.ID, step, cause))
			continue
		}
		stepResult := e.executeStep(ctx, request, scenario.ID, step)
		execution.Steps = append(execution.Steps, stepResult)
		passed[step.ID] = stepResult.Status == StatusPass
		if stepResult.Status != StatusPass {
			blocking = step.ID
			execution.Status, execution.Cause = stepResult.Status, stepResult.Cause
		}
	}
	for _, step := range scenario.Cleanup {
		if !shouldRunCleanup(step.RunIf, request.Variables, passed) {
			execution.Cleanup = append(execution.Cleanup, notRunStep(request.CampaignID, scenario.ID+"-cleanup", step, Cause{Code: "cleanup_guard_false", Message: "cleanup guard did not match", Source: scenario.ID}))
			continue
		}
		cleanupTimeout := 5 * time.Minute
		if step.Poll != nil {
			cleanupTimeout = time.Duration(step.Poll.TimeoutMilliseconds+step.Poll.IntervalMilliseconds) * time.Millisecond
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		cleanupResult := e.executeStep(cleanupCtx, request, scenario.ID+"-cleanup", step)
		cancel()
		execution.Cleanup = append(execution.Cleanup, cleanupResult)
		if cleanupResult.Status != StatusPass && execution.Status == StatusPass {
			execution.Status, execution.Cause = cleanupResult.Status, cleanupResult.Cause
		}
	}
	if execution.Status == StatusPass {
		log.Info("live MCP 场景执行完成")
	} else {
		log.WithField("cause_code", execution.Cause.Code).Error("live MCP 场景执行失败")
	}
	return execution
}

func (e *ToolExecutor) executeStep(ctx context.Context, request ToolCampaignRequest, scenarioID string, step ScenarioStep) ToolStepExecution {
	started := time.Now()
	log := logger.GetLogger().WithEntryName("RuntimeValidationToolStep").WithFields(map[string]any{
		"campaign_id": request.CampaignID, "scenario": scenarioID, "step": step.ID, "tool": step.Tool,
	})
	log.Info("开始 live MCP tools/call")
	rendered, err := renderManifestValue(step.Arguments, request.Variables)
	if err != nil {
		return failedStep(request.CampaignID, scenarioID, step, StatusBlocked, "render_arguments_failed", err, 0, nil)
	}
	arguments, ok := rendered.(map[string]any)
	if !ok {
		return failedStep(request.CampaignID, scenarioID, step, StatusFail, "render_arguments_invalid", fmt.Errorf("rendered arguments are not an object"), 0, nil)
	}
	mutating := mutationTool(step.Tool)
	deadline := started
	if step.Poll != nil {
		deadline = deadline.Add(time.Duration(step.Poll.TimeoutMilliseconds) * time.Millisecond)
	}
	attempts := 0
	var response ToolCallResult
	var assertionResults []AssertionResult
	for {
		attempts++
		response, err = e.caller.CallTool(ctx, step.Tool, arguments)
		if err == nil {
			assertionResults, err = evaluateToolStep(step, response, request.Variables)
		}
		if err == nil || step.Poll == nil || response.IsError || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(time.Duration(step.Poll.IntervalMilliseconds) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			err = ctx.Err()
		case <-timer.C:
		}
		if ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		log.WithFields(map[string]any{"attempt_count": attempts, "duration_ms": time.Since(started).Milliseconds(), "cause_code": "tool_step_failed"}).Error("live MCP tools/call 或业务断言失败")
		return failedStep(request.CampaignID, scenarioID, step, StatusFail, "tool_step_failed", err, attempts, assertionResults)
	}
	root := RawMessageMap(response)
	if mutating && request.OnMutationCommitted != nil {
		// 回调位于 capture/evidence 前：外部副作用一旦成功，后续证据错误也不能让 campaign 忘记资源已存在。
		request.OnMutationCommitted(step.Tool, cloneMap(arguments), response, request.Variables)
	}
	for name, path := range step.Capture {
		value, found := lookupManifestPath(root, path)
		if !found {
			err = fmt.Errorf("capture %s path %s was not found", name, path)
			return failedStep(request.CampaignID, scenarioID, step, StatusFail, "capture_failed", err, attempts, assertionResults)
		}
		request.Variables[name] = value
	}
	evidenceRoot := cloneMap(root)
	evidenceRoot["request"] = map[string]any{"arguments": cloneMap(arguments)}
	recorded, err := buildRecordedEvidence(evidenceRoot, step.Evidence, request.Variables)
	if err != nil {
		return failedStep(request.CampaignID, scenarioID, step, StatusFail, "evidence_contract_failed", err, attempts, assertionResults)
	}
	applicationOK := applicationOK(response)
	evidence := ToolEvidence{
		CampaignID: request.CampaignID, ScenarioID: scenarioID, StepID: step.ID, Tool: step.Tool,
		ResourceID: correlatedResourceID(arguments, root, request.CampaignID), Outcome: ExpectedOutcomeSuccess,
		IsError: response.IsError, ApplicationOK: applicationOK, Assertions: assertionResults,
	}
	log.WithFields(map[string]any{"attempt_count": attempts, "assertion_count": len(assertionResults), "duration_ms": time.Since(started).Milliseconds()}).Info("live MCP tools/call 与业务断言完成")
	if request.OnStepPassed != nil {
		request.OnStepPassed(scenarioID, step.ID, request.Variables)
	}
	return ToolStepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Status: StatusPass, Attempts: attempts, Evidence: evidence, RecordedEvidence: recorded}
}

func buildRecordedEvidence(root map[string]any, contract EvidenceContract, variables map[string]any) (map[string]any, error) {
	redacted, err := cloneJSONMap(root)
	if err != nil {
		return nil, err
	}
	for _, path := range contract.Redact {
		redactEvidencePath(redacted, strings.Split(path, "."))
	}
	paths := make(map[string]any, len(contract.Record))
	for _, path := range contract.Record {
		value, found := lookupManifestPath(redacted, path)
		if !found {
			return nil, fmt.Errorf("record path %s was not found after redaction", path)
		}
		paths[path] = value
	}
	recorded := map[string]any{"paths": paths}
	raw, err := json.Marshal(recorded)
	if err != nil {
		return nil, fmt.Errorf("marshal selected evidence: %w", err)
	}
	lower := strings.ToLower(string(raw))
	for _, forbidden := range contract.Forbid {
		rendered, renderErr := renderManifestValue(forbidden, variables)
		if renderErr != nil {
			return nil, fmt.Errorf("render forbidden evidence value: %w", renderErr)
		}
		needle := strings.ToLower(strings.TrimSpace(fmt.Sprint(rendered)))
		if needle != "" && strings.Contains(lower, needle) {
			return nil, fmt.Errorf("selected evidence contains forbidden value")
		}
	}
	return recorded, nil
}

func cloneJSONMap(input map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal evidence root: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("clone evidence root: %w", err)
	}
	return result, nil
}

func redactEvidencePath(current any, parts []string) {
	if len(parts) == 0 {
		return
	}
	switch typed := current.(type) {
	case map[string]any:
		if parts[0] == "*" {
			for key, nested := range typed {
				if len(parts) == 1 {
					delete(typed, key)
				} else {
					redactEvidencePath(nested, parts[1:])
				}
			}
			return
		}
		nested, ok := typed[parts[0]]
		if !ok {
			return
		}
		if len(parts) == 1 {
			delete(typed, parts[0])
			return
		}
		redactEvidencePath(nested, parts[1:])
	case []any:
		if parts[0] == "*" {
			for _, nested := range typed {
				redactEvidencePath(nested, parts[1:])
			}
			return
		}
		index, err := strconv.Atoi(parts[0])
		if err == nil && index >= 0 && index < len(typed) {
			redactEvidencePath(typed[index], parts[1:])
		}
	}
}

func correlatedResourceID(arguments, response map[string]any, campaignID string) string {
	for _, key := range []string{"deployment_id", "project_id", "session_id", "run_id", "pipeline_id", "host_id", "browser_id", "service_id"} {
		if value := strings.TrimSpace(fmt.Sprint(arguments[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	for _, path := range []string{
		"structuredContent.data.id", "structuredContent.data.project_id", "structuredContent.data.session_id",
		"structuredContent.data.run.id", "structuredContent.data.session.id",
	} {
		if value, found := lookupManifestPath(response, path); found && !emptyManifestValue(value) {
			return strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return campaignID
}

func evaluateToolStep(step ScenarioStep, response ToolCallResult, variables map[string]any) ([]AssertionResult, error) {
	if response.IsError {
		return nil, fmt.Errorf("tool %s returned isError=true", step.Tool)
	}
	structured := RawMessageMap(response.StructuredContent)
	if ok, exists := structured["ok"]; exists && ok != true {
		return nil, fmt.Errorf("tool %s returned application ok=%v", step.Tool, ok)
	}
	root := RawMessageMap(response)
	results := make([]AssertionResult, 0, len(step.Expect.Assertions))
	for _, assertion := range step.Expect.Assertions {
		passed, failure := evaluateAssertion(root, assertion, variables)
		results = append(results, AssertionResult{Path: assertion.Path, Passed: passed, Failure: failure})
		if !passed {
			return results, fmt.Errorf("assertion %s failed: %s", assertion.Path, failure)
		}
	}
	return results, nil
}

func evaluateAssertion(root map[string]any, assertion Assertion, variables map[string]any) (bool, string) {
	actual, found := lookupManifestPath(root, assertion.Path)
	if !found {
		return false, "path not found"
	}
	expected := assertion.Value
	if assertion.Variable != "" {
		var ok bool
		expected, ok = variables[assertion.Variable]
		if !ok {
			return false, "expected variable is missing"
		}
	} else if rendered, err := renderManifestValue(expected, variables); err == nil {
		expected = rendered
	} else {
		return false, err.Error()
	}
	switch assertion.Operator {
	case "eq", "equals":
		if equivalentJSON(actual, expected) {
			return true, ""
		}
	case "not_equals":
		if !equivalentJSON(actual, expected) {
			return true, ""
		}
	case "not_empty", "array_not_empty":
		if !emptyManifestValue(actual) {
			return true, ""
		}
	case "contains":
		if containsManifestValue(actual, expected, false) {
			return true, ""
		}
	case "contains_item":
		if containsManifestValue(actual, expected, true) {
			return true, ""
		}
	case "not_contains":
		if !containsManifestValue(actual, expected, false) {
			return true, ""
		}
	case "gt", "greater_than":
		left, lok := numericValue(actual)
		right, rok := numericValue(expected)
		if lok && rok && left > right {
			return true, ""
		}
	case "matches":
		pattern, err := regexp.Compile(fmt.Sprint(expected))
		if err == nil && pattern.MatchString(fmt.Sprint(actual)) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("actual=%v operator=%s expected=%v", actual, assertion.Operator, expected)
}

func renderManifestValue(value any, variables map[string]any) (any, error) {
	switch typed := value.(type) {
	case string:
		if strings.HasPrefix(typed, "{{") && strings.HasSuffix(typed, "}}") && strings.Count(typed, "{{") == 1 {
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(typed, "{{"), "}}"))
			resolved, ok := variables[name]
			if !ok {
				return nil, fmt.Errorf("runtime variable %s is missing", name)
			}
			return resolved, nil
		}
		result := typed
		for {
			start := strings.Index(result, "{{")
			if start < 0 {
				break
			}
			end := strings.Index(result[start+2:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("unterminated runtime variable in %q", typed)
			}
			end += start + 2
			name := strings.TrimSpace(result[start+2 : end])
			resolved, ok := variables[name]
			if !ok {
				return nil, fmt.Errorf("runtime variable %s is missing", name)
			}
			result = result[:start] + fmt.Sprint(resolved) + result[end+2:]
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, nested := range typed {
			rendered, err := renderManifestValue(nested, variables)
			if err != nil {
				return nil, err
			}
			result[key] = rendered
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, nested := range typed {
			rendered, err := renderManifestValue(nested, variables)
			if err != nil {
				return nil, err
			}
			result[index] = rendered
		}
		return result, nil
	default:
		return value, nil
	}
}

func lookupManifestPath(root any, path string) (any, bool) {
	current := root
	for _, part := range strings.Split(path, ".") {
		switch typed := current.(type) {
		case map[string]any:
			var ok bool
			current, ok = typed[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(typed) {
				return nil, false
			}
			current = typed[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func containsManifestValue(actual, expected any, partial bool) bool {
	switch typed := actual.(type) {
	case string:
		return strings.Contains(typed, fmt.Sprint(expected))
	case []any:
		for _, item := range typed {
			if partial && partialJSONMatch(item, expected) || !partial && (equivalentJSON(item, expected) || strings.Contains(fmt.Sprint(item), fmt.Sprint(expected))) {
				return true
			}
		}
	case map[string]any:
		if partial {
			return partialJSONMatch(typed, expected)
		}
		return strings.Contains(fmt.Sprint(typed), fmt.Sprint(expected))
	default:
		return strings.Contains(fmt.Sprint(actual), fmt.Sprint(expected))
	}
	return false
}

func partialJSONMatch(actual, expected any) bool {
	expectedMap, ok := expected.(map[string]any)
	if !ok {
		return equivalentJSON(actual, expected)
	}
	actualMap, ok := actual.(map[string]any)
	if !ok {
		return false
	}
	for key, wanted := range expectedMap {
		got, exists := actualMap[key]
		if !exists || !partialJSONMatch(got, wanted) {
			return false
		}
	}
	return true
}

func equivalentJSON(left, right any) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftRaw) == string(rightRaw)
}

func numericValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		result, err := typed.Float64()
		return result, err == nil
	default:
		result, err := strconv.ParseFloat(fmt.Sprint(value), 64)
		return result, err == nil
	}
}

func emptyManifestValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return reflected.Len() == 0
	}
	return false
}

func mergeScenarioVariables(scenario Scenario, variables map[string]any) error {
	for name, raw := range scenario.Variables {
		metadata, ok := raw.(map[string]any)
		if !ok {
			if _, exists := variables[name]; !exists {
				variables[name] = raw
			}
			continue
		}
		if _, exists := variables[name]; !exists {
			if fallback, hasDefault := metadata["default"]; hasDefault {
				variables[name] = fallback
			}
		}
		required, _ := metadata["required"].(bool)
		if required && emptyManifestValue(variables[name]) {
			return fmt.Errorf("scenario %s requires runtime variable %s", scenario.ID, name)
		}
	}
	return nil
}

func projectBootstrapBoundary(scenario Scenario) int {
	if scenario.ID != "config-security-lifecycle" {
		return -1
	}
	for index, step := range scenario.Steps {
		if _, ok := step.Capture["project_id"]; ok {
			return index
		}
	}
	return -1
}

func orderToolScenarios(scenarios []Scenario) ([]Scenario, int) {
	order := map[string]int{
		"config-security-lifecycle": 0, "identity-observation": 1, "logs-diagnostics": 3,
		"browser-debug": 4, "code-debug": 5, "remote-pipeline": 6,
	}
	ordered := append([]Scenario{}, scenarios...)
	sort.SliceStable(ordered, func(i, j int) bool {
		left, lok := order[ordered[i].ID]
		right, rok := order[ordered[j].ID]
		if !lok {
			left = 100
		}
		if !rok {
			right = 100
		}
		if left == right {
			return ordered[i].ID < ordered[j].ID
		}
		return left < right
	})
	bootstrap := -1
	for index, scenario := range ordered {
		if projectBootstrapBoundary(scenario) >= 0 {
			bootstrap = index
			break
		}
	}
	// Bootstrap 前缀会单独先执行；完成后 identity 先观测，再继续 config/lifecycle 剩余步骤。
	if bootstrap >= 0 {
		candidate := ordered[bootstrap]
		ordered = append(ordered[:bootstrap], ordered[bootstrap+1:]...)
		insert := 0
		for insert < len(ordered) && ordered[insert].ID == "identity-observation" {
			insert++
		}
		ordered = append(ordered, Scenario{})
		copy(ordered[insert+1:], ordered[insert:])
		ordered[insert] = candidate
		bootstrap = insert
	}
	return ordered, bootstrap
}

func shouldRunCleanup(condition string, variables map[string]any, passed map[string]bool) bool {
	if strings.TrimSpace(condition) == "" {
		return true
	}
	for _, raw := range strings.Split(condition, "&&") {
		clause := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(clause, "variable_set:"):
			if emptyManifestValue(variables[strings.TrimPrefix(clause, "variable_set:")]) {
				return false
			}
		case strings.HasPrefix(clause, "variable_unset:"):
			if !emptyManifestValue(variables[strings.TrimPrefix(clause, "variable_unset:")]) {
				return false
			}
		case strings.HasPrefix(clause, "primary_step_not_passed:"):
			if passed[strings.TrimPrefix(clause, "primary_step_not_passed:")] {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func mutationTool(tool string) bool {
	_, ok := map[string]struct{}{
		"apply_config_change": {}, "upsert_project_config": {}, "upsert_service": {}, "upsert_project_pipeline": {},
		"start_service": {}, "restart_service": {}, "stop_service": {}, "import_pipeline_template": {}, "deploy_project_pipeline": {},
		"open_browser_debug_session": {}, "close_browser_debug_session": {}, "browser_navigate": {}, "browser_set_viewport": {},
		"browser_type": {}, "browser_select_option": {}, "browser_click": {}, "browser_press_key": {}, "browser_reload": {},
		"browser_evaluate": {},
		"debug_capture_at": {}, "set_debug_breakpoints": {}, "debug_continue": {}, "debug_pause": {}, "debug_step_over": {},
		"debug_step_in": {}, "debug_step_out": {}, "debug_evaluate": {}, "create_debug_session": {},
		"append_log_analysis_to_session": {}, "append_debug_session_note": {}, "close_debug_session": {},
	}[tool]
	return ok
}

func applicationOK(response ToolCallResult) *bool {
	value, exists := RawMessageMap(response.StructuredContent)["ok"]
	if !exists {
		return nil
	}
	result, ok := value.(bool)
	if !ok {
		return nil
	}
	return &result
}

func failedStep(campaignID, scenarioID string, step ScenarioStep, status Status, code string, err error, attempts int, assertions []AssertionResult) ToolStepExecution {
	cause := Cause{Code: code, Message: err.Error(), Source: step.ID}
	evidence := ToolEvidence{CampaignID: campaignID, ScenarioID: scenarioID, StepID: step.ID, Tool: step.Tool, Outcome: string(status), Assertions: assertions}
	return ToolStepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Status: status, Cause: cause, Attempts: attempts, Evidence: evidence}
}

func notRunStep(campaignID, scenarioID string, step ScenarioStep, cause Cause) ToolStepExecution {
	evidence := ToolEvidence{CampaignID: campaignID, ScenarioID: scenarioID, StepID: step.ID, Tool: step.Tool, Outcome: string(StatusNotRun), Assertions: []AssertionResult{}}
	return ToolStepExecution{StepID: step.ID, Tool: step.Tool, Coverage: step.Coverage, Status: StatusNotRun, Cause: cause, Evidence: evidence}
}

func collectPrimaryRows(assignments []CoverageAssignment, scenarios []ToolScenarioExecution) []ToolPrimaryRow {
	steps := map[string]ToolStepExecution{}
	for _, scenario := range scenarios {
		for _, step := range scenario.Steps {
			steps[scenario.ID+"\x00"+step.StepID] = step
		}
	}
	rows := make([]ToolPrimaryRow, 0, len(assignments))
	for _, assignment := range assignments {
		step, ok := steps[assignment.ScenarioID+"\x00"+assignment.StepID]
		if !ok {
			cause := Cause{Code: "primary_step_not_scheduled", Message: "primary step has no execution row", Source: assignment.ScenarioID}
			rows = append(rows, ToolPrimaryRow{Tool: assignment.Tool, ScenarioID: assignment.ScenarioID, StepID: assignment.StepID, Status: StatusNotRun, Cause: cause})
			continue
		}
		evidence := step.Evidence
		rows = append(rows, ToolPrimaryRow{Tool: assignment.Tool, ScenarioID: assignment.ScenarioID, StepID: assignment.StepID, Status: step.Status, Cause: step.Cause, Evidence: &evidence})
	}
	return rows
}

func bindToolEvidenceReferences(scenarios []ToolScenarioExecution) {
	for scenarioIndex := range scenarios {
		for stepIndex := range scenarios[scenarioIndex].Steps {
			step := &scenarios[scenarioIndex].Steps[stepIndex]
			step.Evidence.EvidenceRef = fmt.Sprintf("evidence/tool-campaign.json#/scenarios/%d/steps/%d/recorded_evidence", scenarioIndex, stepIndex)
		}
		for stepIndex := range scenarios[scenarioIndex].Cleanup {
			step := &scenarios[scenarioIndex].Cleanup[stepIndex]
			step.Evidence.EvidenceRef = fmt.Sprintf("evidence/tool-campaign.json#/scenarios/%d/cleanup/%d/recorded_evidence", scenarioIndex, stepIndex)
		}
	}
}

func finalizeToolPrimaryEvidence(result *ToolCampaignResult, assignments []CoverageAssignment) {
	bindToolEvidenceReferences(result.Scenarios)
	result.PrimaryRows = collectPrimaryRows(assignments, result.Scenarios)
	result.PrimaryEvidence = nil
	for _, row := range result.PrimaryRows {
		if row.Status == StatusPass && row.Evidence != nil {
			result.PrimaryEvidence = append(result.PrimaryEvidence, *row.Evidence)
		}
	}
}

func primaryRowsNotRun(scenarios []Scenario, cause Cause) []ToolPrimaryRow {
	assignments, _ := PrimaryAssignments(scenarios)
	rows := make([]ToolPrimaryRow, 0, len(assignments))
	for _, assignment := range assignments {
		rows = append(rows, ToolPrimaryRow{Tool: assignment.Tool, ScenarioID: assignment.ScenarioID, StepID: assignment.StepID, Status: StatusNotRun, Cause: cause})
	}
	return rows
}

func scenarioNotRun(scenario Scenario, cause Cause, status Status) ToolScenarioExecution {
	execution := ToolScenarioExecution{ID: scenario.ID, Status: status, Cause: cause}
	for _, step := range scenario.Steps {
		execution.Steps = append(execution.Steps, notRunStep("", scenario.ID, step, cause))
	}
	for _, step := range scenario.Cleanup {
		execution.Cleanup = append(execution.Cleanup, notRunStep("", scenario.ID+"-cleanup", step, cause))
	}
	return execution
}

func allScenariosNotRun(scenarios []Scenario, cause Cause) []ToolScenarioExecution {
	results := make([]ToolScenarioExecution, 0, len(scenarios))
	for _, scenario := range scenarios {
		results = append(results, scenarioNotRun(scenario, cause, StatusNotRun))
	}
	return results
}

func scenariosNotRunExcept(scenarios []Scenario, except string, cause Cause) []ToolScenarioExecution {
	results := make([]ToolScenarioExecution, 0, len(scenarios)-1)
	for _, scenario := range scenarios {
		if scenario.ID != except {
			results = append(results, scenarioNotRun(scenario, cause, StatusNotRun))
		}
	}
	return results
}

func combineScenarioExecutions(prefix, remainder ToolScenarioExecution) ToolScenarioExecution {
	result := prefix
	result.Steps = append(result.Steps, remainder.Steps...)
	result.Cleanup = append(result.Cleanup, remainder.Cleanup...)
	if remainder.Status != StatusPass {
		result.Status, result.Cause = remainder.Status, remainder.Cause
	}
	return result
}

func cloneVariables(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}
