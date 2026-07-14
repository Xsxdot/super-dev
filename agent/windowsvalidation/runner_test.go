// runner_test.go 验证场景判定和 cleanup 条件的安全边界。
//
// 职责：
//   - 防止普通产品错误被洗成预期策略拒绝
//   - 防止 cleanup 在缺少资源身份时误执行
//
// 边界：
//   - 不启动真实 MCP 或 Windows 服务
package windowsvalidation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateStepResultUsesStrictPolicyDenialWhitelist(t *testing.T) {
	t.Parallel()
	allowed := ToolCallResult{IsError: true, StructuredContent: map[string]any{"ok": false, "code": "browser_evaluate_disabled"}}
	step := ScenarioStep{Tool: "browser_evaluate", Expect: StepExpectation{Outcome: "success_or_policy_denied"}}
	if _, err := EvaluateStepResult(step, allowed, nil); err != nil {
		t.Fatalf("documented policy denial rejected: %v", err)
	}

	approvalTimeout := ToolCallResult{IsError: true, StructuredContent: map[string]any{"ok": false, "code": "approval_required"}}
	if _, err := EvaluateStepResult(step, approvalTimeout, nil); err == nil {
		t.Fatal("approval_required must not pass as a policy denial")
	}
}

func TestShouldRunCleanupSupportsCombinedConditions(t *testing.T) {
	t.Parallel()
	variables := map[string]any{"browser_session_id": "brs-1"}
	passed := map[string]bool{"browser-close-session": false}
	condition := "variable_set:browser_session_id&&primary_step_not_passed:browser-close-session&&variable_unset:cleanup_started"
	if !ShouldRunCleanup(condition, variables, passed) {
		t.Fatal("guarded cleanup should run")
	}
	variables["cleanup_started"] = true
	if ShouldRunCleanup(condition, variables, passed) {
		t.Fatal("cleanup must not run twice")
	}
}

func TestRemoteHostPresentDoesNotDependOnOrdering(t *testing.T) {
	t.Parallel()
	value := map[string]any{"structuredContent": map[string]any{"data": map[string]any{"remote_hosts": []any{
		map[string]any{"id": "other-host", "is_self": false},
		map[string]any{"id": "linux-validation", "is_self": false},
	}}}}
	if !remoteHostPresent(value, "linux-validation") {
		t.Fatal("configured non-self host should be available regardless of list order")
	}
	if remoteHostPresent(value, "missing") {
		t.Fatal("missing host must not pass preflight")
	}
}

func TestWriteCampaignReportsRedactsMarkdownAndJSON(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	redactor := NewRedactor()
	redactor.RegisterSecret("AUTHORIZATION", "Basic report-secret")
	report := CampaignReport{
		SchemaVersion:      2,
		Kind:               "superdev.windows-validation.campaign-report",
		CampaignID:         "w10x64-e3cc94f-20260714T120000Z-aabbcc",
		Lane:               "msi_smoke",
		Installer:          testCompleteInstaller(t, "msi"),
		RuntimeAttestation: RuntimeAttestation{Result: attemptedResult(false, "request used Basic report-secret", resultTestTime, resultTestTime, nil)},
		Cleanup:            pendingCleanupRecord("cleanup pending", ""),
		Providers: []ProviderExecution{{
			Provider: "go", Result: attemptedResult(false, "request used Basic report-secret", resultTestTime, resultTestTime, nil),
			Runtime: attemptedResult(false, "request used Basic report-secret", resultTestTime, resultTestTime, nil),
			Debug:   blockedResult("runtime", "request used Basic report-secret"), Reason: "request used Basic report-secret",
		}},
	}
	report.ValidationCatalog, report.Scenarios, report.ToolRows, _ = testValidationSurface(notRunResult("independent MSI lane"))
	if err := writeCampaignReports(directory, redactor, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"campaign-report.json", "campaign-report.md"} {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(content), "report-secret") {
			t.Fatalf("%s leaked registered secret", name)
		}
	}
}

func TestValidateRuntimeInputKeepsMSISmokeIndependentFromRemoteInputs(t *testing.T) {
	t.Parallel()
	input := RuntimeInput{
		MCPPath: "superdev-mcp.exe", InstallerDirectory: "installers",
		CampaignRoot: "campaigns", ResultsRoot: "results", Lane: "msi_smoke",
		CampaignID: "w10x64-e3cc94f-20260713T010101Z-a1b2c3",
	}
	if err := validateRuntimeInput(input); err != nil {
		t.Fatalf("MSI smoke must not require Linux inputs: %v", err)
	}
	input.Lane = "nsis_core"
	if err := validateRuntimeInput(input); err == nil {
		t.Fatal("NSIS core must require the dedicated Linux host inputs")
	}
	input.Lane = "core_only"
	if err := validateRuntimeInput(input); err == nil {
		t.Fatal("core_only executes the remote functional surface and must require dedicated Linux inputs")
	}
	input.LinuxHostID = "linux-validation-host"
	input.LinuxRoot = "/srv/superdev-validation/{{run_id}}"
	if err := validateRuntimeInput(input); err != nil {
		t.Fatalf("core_only should be a supported diagnostic lane: %v", err)
	}
	input.Lane = "msi_smoke"
	input.CampaignID = "unsafe-campaign"
	if err := validateRuntimeInput(input); err == nil {
		t.Fatal("prepared campaign identity must be validated before path construction")
	}
}
