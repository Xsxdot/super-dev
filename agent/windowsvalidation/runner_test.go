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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type capturingToolCaller struct {
	tool      string
	arguments map[string]any
	result    ToolCallResult
}

type providerMixedResultCaller struct {
	stopped   bool
	codeDebug map[string]any
}

func TestVerifyPreinstallThenLoadLifecycleStopsBeforeProductBoundariesWhenPreInstallRejected(t *testing.T) {
	t.Parallel()
	preInstallErr := errors.New("pre-install environment admission rejected")
	calls := map[string]int{}
	err := verifyPreinstallThenLoadLifecycle(
		func() error {
			calls["pre_install"]++
			return preInstallErr
		},
		func() error {
			calls["installer_lifecycle"]++
			return nil
		},
	)
	if err == nil {
		// 这两个调用代表 helper 之后的真实 RunCampaign 产品边界；只有 A 与 lifecycle
		// 都成功才允许进入，避免测试一个生产从未使用的五阶段调度器。
		func() {
			calls["installed_mcp"]++
		}()
		func() {
			calls["post_install"]++
		}()
		func() {
			calls["functional"]++
		}()
	}
	if !errors.Is(err, preInstallErr) {
		t.Fatalf("verifyPreinstallThenLoadLifecycle() error = %v, want pre-install rejection", err)
	}
	if calls["pre_install"] != 1 {
		t.Fatalf("pre-install verifier calls = %d, want 1", calls["pre_install"])
	}
	for _, boundary := range []string{"installer_lifecycle", "installed_mcp", "post_install", "functional"} {
		if calls[boundary] != 0 {
			t.Errorf("%s calls = %d, want 0 before pre-install admission", boundary, calls[boundary])
		}
	}
}

func (c *providerMixedResultCaller) CallTool(_ context.Context, tool string, arguments map[string]any) (ToolCallResult, error) {
	if tool == "upsert_service" {
		value, _ := LookupPath(arguments, "service.deployments.0.code_debug")
		c.codeDebug, _ = value.(map[string]any)
	}
	if tool == "stop_service" {
		c.stopped = true
	}
	data := map[string]any{}
	if tool == "list_services" {
		status := "running"
		if c.stopped {
			status = "stopped"
		}
		data["services"] = []any{map[string]any{"deployments": []any{map[string]any{"id": "provider-java-dev", "status": status}}}}
	}
	return ToolCallResult{StructuredContent: map[string]any{"ok": true, "data": data}}, nil
}

func (c *capturingToolCaller) CallTool(_ context.Context, tool string, arguments map[string]any) (ToolCallResult, error) {
	c.tool = tool
	c.arguments = cloneJSONMap(arguments)
	return c.result, nil
}

func TestPersistGateFailurePreservesVerifiedInstallerLifecycleAndSafeCause(t *testing.T) {
	pass := testPassResult()
	catalog, _, _, toolNames := testValidationSurface(pass)
	scenario := Scenario{ID: catalog.Scenarios[0].ID, Title: catalog.Scenarios[0].Title}
	for _, step := range catalog.Scenarios[0].Steps {
		scenario.Steps = append(scenario.Steps, ScenarioStep{ID: step.StepID, Tool: step.Tool, Coverage: step.Coverage})
	}
	for _, step := range catalog.Scenarios[0].Cleanup {
		scenario.Cleanup = append(scenario.Cleanup, ScenarioStep{ID: step.StepID, Tool: step.Tool, Coverage: step.Coverage})
	}
	source := PackageSource{Scenarios: []Scenario{scenario}, Coverage: catalog.Coverage}
	source.Frozen.Build.GitCommit = "e3cc94f"
	source.Frozen.Build.ProductVersion = "0.2.1"
	providerNames := make([]string, 0, 7)
	for index := 0; index < 7; index++ {
		name := []string{"go", "python", "node", "java", "kotlin", "rust", "cpp"}[index]
		providerNames = append(providerNames, name)
		source.Fixtures = append(source.Fixtures, FixtureManifest{Provider: name})
	}
	installer := testCompleteInstaller(t, "nsis")
	root := t.TempDir()
	campaignID := "w10x64-e3cc94f-20260715T010203Z-a1b2c3"
	checks := []PackageFileIdentity{{Path: "frozen.exe", SizeBytes: 22, SHA256: strings.Repeat("b", 64)}}
	attestation := RuntimeAttestation{Result: pass, ToolNames: toolNames, ProviderNames: providerNames}
	report := persistGateFailure(
		filepath.Join(root, campaignID), NewRedactor(), source,
		RuntimeInput{Lane: "nsis_core", ResultsRoot: root}, campaignID, time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC), checks,
		installer, attestation, "environment_preflight", errors.New(`C:\Users\alice\private\probe.exe was unavailable`),
	)

	if report.Installer.Install.PhaseStatus != PhaseStatusPass || report.Installer.Start.PhaseStatus != PhaseStatusPass || !report.Installer.InstallerExecuted {
		t.Fatalf("gate report erased verified installer lifecycle: %#v", report.Installer)
	}
	if strings.Contains(report.FailureReason, "alice") || strings.Contains(CanonicalJSON(report), `C:\Users\alice`) {
		t.Fatalf("gate report leaked the raw cause: %s", CanonicalJSON(report))
	}
	if !strings.Contains(report.FailureReason, "cause_code=environment_preflight_failed") {
		t.Fatalf("gate report has no stable cause code: %q", report.FailureReason)
	}
}

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
	input.LinuxRoot = windowsValidationLinuxRootTemplate
	input.AgentDataDirectory = `C:\Users\Validation\.superdev`
	input.RemoteGovernanceAttestationPath = `C:\SuperDev\remote-governance-attestation.json`
	if err := validateRuntimeInput(input); err != nil {
		t.Fatalf("core_only should be a supported diagnostic lane: %v", err)
	}
	input.InstallerDirectory = ""
	if err := validateRuntimeInput(input); err != nil {
		t.Fatalf("core_only must not require an installer directory: %v", err)
	}
	for _, lane := range []string{"msi_smoke", "nsis_core"} {
		input.Lane = lane
		if err := validateRuntimeInput(input); err == nil || !strings.Contains(err.Error(), "installer_directory") {
			t.Fatalf("%s must require installer_directory, got %v", lane, err)
		}
	}
	input.Lane = "msi_smoke"
	input.InstallerDirectory = "installers"
	input.CampaignID = "unsafe-campaign"
	if err := validateRuntimeInput(input); err == nil {
		t.Fatal("prepared campaign identity must be validated before path construction")
	}
}

func TestValidateRuntimeInputRejectsRelativeFunctionalAdapterPaths(t *testing.T) {
	t.Parallel()
	base := RuntimeInput{
		MCPPath: `C:\SuperDev\superdev-mcp.exe`, InstallerDirectory: `C:\SuperDev\installers`,
		CampaignRoot: `C:\SuperDev\campaigns`, ResultsRoot: `C:\SuperDev\results`, Lane: "nsis_core",
		LinuxHostID: "linux-validation-host", LinuxRoot: windowsValidationLinuxRootTemplate,
		AgentDataDirectory:              `C:\Users\Validation\.superdev`,
		RemoteGovernanceAttestationPath: `C:\SuperDev\remote-governance-attestation.json`,
	}
	tests := []struct {
		name  string
		apply func(*RuntimeInput)
	}{
		{"agent data", func(input *RuntimeInput) { input.AgentDataDirectory = `.superdev` }},
		{"go adapter", func(input *RuntimeInput) { input.GoAdapterCommand = `tools\dlv.exe` }},
		{"python adapter", func(input *RuntimeInput) { input.PythonAdapterCommand = `tools\python.exe` }},
		{"node adapter", func(input *RuntimeInput) { input.NodeAdapterCommand = `tools\node.exe` }},
		{"native adapter", func(input *RuntimeInput) { input.NativeAdapterCommand = `tools\lldb-dap.exe` }},
		{"jvm adapter", func(input *RuntimeInput) { input.JVMAdapterCommand = `tools\jvm-wrapper.exe` }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := base
			test.apply(&input)
			if err := validateRuntimeInput(input); err == nil || !strings.Contains(err.Error(), "absolute") {
				t.Fatalf("validateRuntimeInput() error = %v, want absolute-path rejection", err)
			}
		})
	}
}

func TestLinuxRootRequiresAndExpandsOnlyTheFrozenCampaignTemplate(t *testing.T) {
	t.Parallel()
	const campaignID = "w10x64-e3cc94f-20260715T010203Z-a1b2c3"
	root, err := expandLinuxCampaignRoot(windowsValidationLinuxRootTemplate, campaignID)
	if err != nil {
		t.Fatal(err)
	}
	if root != "/srv/superdev-validation/"+campaignID {
		t.Fatalf("expanded linux_root = %q", root)
	}
	for _, invalid := range []string{
		" /srv/superdev-validation/{{run_id}}",
		"/srv/superdev-validation/{{run_id}}/child",
		"/srv/superdev-validation/../{{run_id}}",
		"'/srv/superdev-validation/{{run_id}}'",
		"/srv/superdev-validation/{{run_id}}-suffix",
	} {
		if _, err := expandLinuxCampaignRoot(invalid, campaignID); err == nil {
			t.Errorf("linux_root %q should be rejected", invalid)
		}
	}
	if _, err := expandLinuxCampaignRoot(windowsValidationLinuxRootTemplate, "../../unsafe"); err == nil {
		t.Fatal("unsafe campaign ID should be rejected before remote root expansion")
	}
}

func TestValidateRuntimeInputRejectsEveryLinuxRootVariant(t *testing.T) {
	t.Parallel()
	base := RuntimeInput{
		MCPPath: `C:\SuperDev\superdev-mcp.exe`, InstallerDirectory: `C:\SuperDev\installers`,
		CampaignRoot: `C:\SuperDev\campaigns`, ResultsRoot: `C:\SuperDev\results`, Lane: "core_only",
		LinuxHostID: "linux-validation-host", LinuxRoot: windowsValidationLinuxRootTemplate,
		AgentDataDirectory:              `C:\Users\Validation\.superdev`,
		RemoteGovernanceAttestationPath: `C:\SuperDev\remote-governance-attestation.json`,
	}
	if err := validateRuntimeInput(base); err != nil {
		t.Fatalf("frozen linux_root contract rejected: %v", err)
	}
	for _, invalid := range []string{
		" /srv/superdev-validation/{{run_id}}",
		"/srv/superdev-validation/{{run_id}}/child",
		"/srv/superdev-validation/../{{run_id}}",
		"'/srv/superdev-validation/{{run_id}}'",
		"/srv/superdev-validation/{{run_id}}-suffix",
	} {
		input := base
		input.LinuxRoot = invalid
		if err := validateRuntimeInput(input); err == nil || !strings.Contains(err.Error(), "exactly equal") {
			t.Errorf("validateRuntimeInput() linux_root %q error = %v, want exact-contract rejection", invalid, err)
		}
	}
}

func TestValidateRuntimeInputRejectsNonWaivablePlatformBlockers(t *testing.T) {
	t.Parallel()
	base := RuntimeInput{
		MCPPath: `C:\SuperDev\superdev-mcp.exe`, InstallerDirectory: `C:\SuperDev\installers`,
		CampaignRoot: `C:\SuperDev\campaigns`, ResultsRoot: `C:\SuperDev\results`, Lane: "core_only",
		LinuxHostID: "linux-validation-host", LinuxRoot: windowsValidationLinuxRootTemplate,
		AgentDataDirectory:              `C:\Users\Validation\.superdev`,
		RemoteGovernanceAttestationPath: `C:\SuperDev\remote-governance-attestation.json`,
	}
	for _, key := range []string{EnvironmentKeyPlatformWindows, EnvironmentKeyPlatformArchitecture, EnvironmentKeyPowerShell51} {
		t.Run(key, func(t *testing.T) {
			input := base
			input.AllowedEnvironmentBlockers = []string{key}
			err := validateRuntimeInput(input)
			if err == nil || !strings.Contains(err.Error(), "non-waivable platform prerequisite") {
				t.Fatalf("validateRuntimeInput() error = %v, want direct non-waivable rejection", err)
			}
		})
	}
	base.AllowedEnvironmentBlockers = []string{EnvironmentKeyToolchainNode}
	if err := validateRuntimeInput(base); err != nil {
		t.Fatalf("core_only should still allow an explicitly named capability blocker: %v", err)
	}
}

func TestAgentDataDirectoryBindingUsesInheritedRuntimeRoot(t *testing.T) {
	t.Parallel()
	inputPath := `C:\Users\Validation\.superdev`
	if err := validateAgentDataDirectoryBinding("nsis_core", inputPath, `c:/users/validation/.superdev/`); err != nil {
		t.Fatalf("case-insensitive clean paths should bind: %v", err)
	}
	if err := validateAgentDataDirectoryBinding("core_only", inputPath, `C:\Other\.superdev`); err == nil {
		t.Fatal("different Agent data roots must be rejected before preflight")
	}
	if err := validateAgentDataDirectoryBinding("nsis_core", inputPath, ""); err == nil {
		t.Fatal("missing inherited SUPERDEV_AGENT_DATA_DIR must be rejected")
	}
	if err := validateAgentDataDirectoryBinding("msi_smoke", "", ""); err != nil {
		t.Fatalf("MSI smoke must stay independent from Agent data binding: %v", err)
	}
}

func TestProductionAgentEndpointIsCanonicalAndBoundToLifecycleListener(t *testing.T) {
	t.Parallel()
	evidence := []InstallerLifecycleActionEvidence{{
		Action: LifecycleActionStart, ExecutionFacts: ExecutionFacts{Attempted: true, Succeeded: true},
		Observation: InstallerLifecycleObservation{
			Port57017: &InstallerLifecyclePortIdentity{Port: 57017, Listening: true, OwningProcessID: 4242},
			Processes: []InstallerLifecycleProcessIdentity{{Role: "agent", ProcessID: 4242}},
		},
	}}
	endpoint, err := bindProductionAgentEndpoint("nsis_core", evidence)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.URL != "http://127.0.0.1:57017" || endpoint.Identity != "loopback-agent-57017" {
		t.Fatalf("endpoint = %#v, want canonical local Agent identity", endpoint)
	}
	if _, err := bindProductionAgentEndpoint("core_only", nil); err != nil {
		t.Fatalf("core_only has no installer lifecycle but must retain the fixed local endpoint: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*InstallerLifecycleActionEvidence)
	}{
		{"wrong port", func(value *InstallerLifecycleActionEvidence) { value.Observation.Port57017.Port = 57018 }},
		{"not listening", func(value *InstallerLifecycleActionEvidence) { value.Observation.Port57017.Listening = false }},
		{"wrong owner", func(value *InstallerLifecycleActionEvidence) { value.Observation.Port57017.OwningProcessID = 9999 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := evidence[0]
			port := *evidence[0].Observation.Port57017
			invalid.Observation.Port57017 = &port
			invalid.Observation.Processes = append([]InstallerLifecycleProcessIdentity{}, evidence[0].Observation.Processes...)
			test.mutate(&invalid)
			if _, err := bindProductionAgentEndpoint("nsis_core", []InstallerLifecycleActionEvidence{invalid}); err == nil {
				t.Fatal("installer lane accepted endpoint not bound to lifecycle Agent listener")
			}
		})
	}
}

func TestValidationAgentEndpointRejectsUntrustedURLFormsWithoutEchoingSecrets(t *testing.T) {
	t.Parallel()
	for _, value := range []string{
		"http://user:secret-value@127.0.0.1:57017",
		"http://127.0.0.1:57017?token=secret-value",
		"http://127.0.0.1:57017/#secret-value",
		"https://127.0.0.1:57017",
		"http://localhost:57017",
		"http://127.0.0.1:57018",
		"http://10.0.0.2:57017",
		" http://127.0.0.1:57017",
	} {
		_, err := parseProductionAgentEndpoint(value)
		if err == nil {
			t.Fatalf("parseProductionAgentEndpoint(%q) unexpectedly succeeded", value)
		}
		if strings.Contains(err.Error(), "secret-value") {
			t.Fatalf("endpoint validation error leaked URL secret: %v", err)
		}
	}
	custom, err := validationAgentEndpointForTest("http://127.0.0.1:49123")
	if err != nil || custom.URL != "http://127.0.0.1:49123" {
		t.Fatalf("package-private loopback test seam = %#v, %v", custom, err)
	}
}

func TestProductionEntrypointsExposeNoAgentEndpointOverride(t *testing.T) {
	t.Parallel()
	for _, path := range []string{
		filepath.Join("..", "cmd", "windows-validation", "main.go"),
		filepath.Join("..", "..", "validation", "windows-real", "Run-Validation.ps1"),
		filepath.Join("..", "..", "validation", "windows-real", "manifest", "runtime-input.example.json"),
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read production entrypoint %s: %v", path, err)
		}
		normalized := strings.ToLower(string(content))
		for _, forbidden := range []string{"agent-url", "agent_url", "agenturl"} {
			if strings.Contains(normalized, forbidden) {
				t.Errorf("production entrypoint %s exposes forbidden Agent endpoint override %q", path, forbidden)
			}
		}
	}
}

func TestProviderAdapterBindingsPinPassedManifestPathsIntoServiceConfig(t *testing.T) {
	t.Parallel()
	plan := EnvironmentCollectionPlan{Adapters: []EnvironmentAdapterPlan{
		{Key: EnvironmentKeyAdapterGo, Expected: EnvironmentExpected{Source: "path_fallback"}},
		{Key: EnvironmentKeyAdapterPython, Expected: EnvironmentExpected{Source: "provider_default"}},
		{Key: EnvironmentKeyAdapterNode, Expected: EnvironmentExpected{Source: "path_fallback"}},
		{Key: EnvironmentKeyAdapterNative, Expected: EnvironmentExpected{Source: "path_fallback"}},
		{Key: EnvironmentKeyAdapterJVM, Expected: EnvironmentExpected{Source: "explicit"}},
	}}
	paths := map[string]string{
		EnvironmentKeyAdapterGo:     `C:\Tools\Delve\dlv.exe`,
		EnvironmentKeyAdapterPython: `C:\Python314\python.exe`,
		EnvironmentKeyAdapterNode:   `C:\Program Files\nodejs\node.exe`,
		EnvironmentKeyAdapterNative: `C:\Program Files\LLVM\bin\lldb-dap.exe`,
		EnvironmentKeyAdapterJVM:    `C:\SuperDev\jvm-wrapper.exe`,
	}
	sources := map[string]string{
		EnvironmentKeyAdapterGo: "path_fallback", EnvironmentKeyAdapterPython: "provider_default",
		EnvironmentKeyAdapterNode: "path_fallback", EnvironmentKeyAdapterNative: "path_fallback",
		EnvironmentKeyAdapterJVM: "explicit",
	}
	manifest := EnvironmentManifest{}
	for _, adapter := range plan.Adapters {
		manifest.Prerequisites = append(manifest.Prerequisites, EnvironmentPrerequisite{
			Key: adapter.Key, Result: testPassResult(),
			Resolved: EnvironmentResolved{Path: paths[adapter.Key], Source: adapter.Expected.Source},
		})
	}

	bindings, err := buildProviderAdapterBindings(plan, manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		provider string
		key      string
	}{
		{"go", EnvironmentKeyAdapterGo}, {"python", EnvironmentKeyAdapterPython}, {"node", EnvironmentKeyAdapterNode},
		{"rust", EnvironmentKeyAdapterNative}, {"cpp", EnvironmentKeyAdapterNative},
		{"java", EnvironmentKeyAdapterJVM}, {"kotlin", EnvironmentKeyAdapterJVM},
	} {
		config, err := providerCodeDebugConfig(test.provider, bindings)
		if err != nil {
			t.Fatalf("providerCodeDebugConfig(%s): %v", test.provider, err)
		}
		if got := config["adapter_command"]; got != paths[test.key] {
			t.Errorf("provider %s adapter_command = %v, want collector path %s", test.provider, got, paths[test.key])
		}
		if got := bindings[test.provider].Source; got != sources[test.key] {
			t.Errorf("provider %s source = %s, want frozen plan source", test.provider, got)
		}
	}

	fixtureEnvironment, err := providerFixtureEnvironment("java", bindings, `C:\Users\Validation\.superdev`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixtureEnvironment["SUPERDEV_JVM_ADAPTER_COMMAND"]; got != paths[EnvironmentKeyAdapterJVM] {
		t.Fatalf("JVM fixture command = %q, want admitted resolved path", got)
	}
	fixtureEnvironment, err = providerFixtureEnvironment("node", bindings, `C:\Users\Validation\.superdev`)
	if err != nil {
		t.Fatal(err)
	}
	if got := fixtureEnvironment["SUPERDEV_AGENT_DATA_DIR"]; got != `C:\Users\Validation\.superdev` {
		t.Fatalf("Node fixture Agent data = %q, want bound runtime root", got)
	}
}

func TestPrimaryGoServiceUpsertUsesAdmittedAdapterPath(t *testing.T) {
	t.Parallel()
	source, err := LoadPackageSource(filepath.Join("..", "..", "validation", "windows-real"))
	if err != nil {
		t.Fatal(err)
	}
	scenario, found := scenarioByID(source.Scenarios, "config-security-lifecycle")
	if !found {
		t.Fatal("config-security-lifecycle scenario is missing")
	}
	var step ScenarioStep
	for _, candidate := range scenario.Steps {
		if candidate.ID == "upsert-go-service" {
			step = candidate
			break
		}
	}
	if step.ID == "" {
		t.Fatal("upsert-go-service step is missing")
	}
	const admitted = `C:\Tools\Delve\dlv.exe`
	caller := &capturingToolCaller{
		result: ToolCallResult{
			StructuredContent: map[string]any{
				"data": map[string]any{
					"preview": map[string]any{
						"kind": "config.service.upsert", "validation": map[string]any{"ok": true}, "diff": map[string]any{},
					},
				},
			},
		},
	}
	executor := &ScenarioExecutor{
		client: caller, resultsDir: t.TempDir(), redactor: NewRedactor(), campaignID: "adapter-bound-go", lane: "nsis_core",
		variables: map[string]any{
			"project_id": "p1", "project_root": `C:\Campaign\workspace`, "campaign_id": "adapter-bound-go", "run_id": "adapter-bound-go",
			"go_service_id": "go-validation", "go_service_name": "go-validation", "go_deployment_id": "go-validation-dev",
			"go_runtime_config": map[string]any{"entry": "main.go"}, "go_readiness_url": "http://127.0.0.1:18190",
			"go_adapter_command": admitted,
		},
		passed: map[string]bool{},
	}
	execution := executor.executeStep(context.Background(), scenario.ID, step)
	if execution.Result.PhaseStatus != PhaseStatusPass {
		t.Fatalf("upsert step result = %#v", execution.Result)
	}
	if caller.tool != "upsert_service" {
		t.Fatalf("tool = %q, want upsert_service", caller.tool)
	}
	command, found := LookupPath(caller.arguments, "service.deployments.0.code_debug.adapter_command")
	if !found || command != admitted {
		t.Fatalf("upsert adapter_command = %v, want admitted path %s", command, admitted)
	}
}

func TestCodeDebugScenarioObservesExactGoDeploymentIdentity(t *testing.T) {
	t.Parallel()
	source, err := LoadPackageSource(filepath.Join("..", "..", "validation", "windows-real"))
	if err != nil {
		t.Fatal(err)
	}
	scenario, found := scenarioByID(source.Scenarios, "code-debug")
	if !found {
		t.Fatal("code-debug scenario is missing")
	}
	wantSteps := map[string]string{
		"code-runtime-before":        "running",
		"code-runtime-after":         "running",
		"wait-go-stopped-after-code": "stopped",
	}
	for _, step := range append(scenario.Steps, scenario.Cleanup...) {
		wantStatus, wanted := wantSteps[step.ID]
		if !wanted {
			continue
		}
		if step.Tool != "diagnose_service" {
			t.Errorf("%s tool = %q, want diagnose_service", step.ID, step.Tool)
		}
		if got := step.Arguments["deployment_id"]; got != "{{go_deployment_id}}" {
			t.Errorf("%s deployment_id = %v", step.ID, got)
		}
		if len(step.Expect.Assertions) < 2 || step.Expect.Assertions[0].Path != "structuredContent.data.target.deployment.id" || step.Expect.Assertions[1].Path != "structuredContent.data.status" || step.Expect.Assertions[1].Value != wantStatus {
			t.Errorf("%s assertions = %#v", step.ID, step.Expect.Assertions)
		}
		delete(wantSteps, step.ID)
	}
	if len(wantSteps) != 0 {
		t.Fatalf("missing exact deployment observation steps: %v", wantSteps)
	}
}

func TestCodeDebugScenarioMatchesVariableObjectsByStableName(t *testing.T) {
	t.Parallel()
	source, err := LoadPackageSource(filepath.Join("..", "..", "validation", "windows-real"))
	if err != nil {
		t.Fatal(err)
	}
	scenario, found := scenarioByID(source.Scenarios, "code-debug")
	if !found {
		t.Fatal("code-debug scenario is missing")
	}
	wantSteps := map[string]bool{
		"code-capture-at":     true,
		"code-inspect-paused": true,
		"code-variables":      true,
	}
	for _, step := range scenario.Steps {
		if !wantSteps[step.ID] {
			continue
		}
		assertion := step.Expect.Assertions[len(step.Expect.Assertions)-1]
		value, _ := assertion.Value.(map[string]any)
		if assertion.Operator != "contains_item" || value["name"] != "validationMarker" {
			t.Errorf("%s variable assertion = %#v", step.ID, assertion)
		}
		delete(wantSteps, step.ID)
	}
	if len(wantSteps) != 0 {
		t.Fatalf("missing stable variable assertions: %v", wantSteps)
	}
}

func TestCodeDebugScenarioEvidenceOnlyRequiresStableSuccessFields(t *testing.T) {
	t.Parallel()
	source, err := LoadPackageSource(filepath.Join("..", "..", "validation", "windows-real"))
	if err != nil {
		t.Fatal(err)
	}
	scenario, found := scenarioByID(source.Scenarios, "code-debug")
	if !found {
		t.Fatal("code-debug scenario is missing")
	}
	breakpointSteps := 0
	readSteps := 0
	evaluateSteps := 0
	for _, step := range append(scenario.Steps, scenario.Cleanup...) {
		records := strings.Join(step.Evidence.Record, "\n")
		switch step.Tool {
		case "set_debug_breakpoints":
			breakpointSteps++
			if strings.Contains(records, "structuredContent.data.result.session_id") || !strings.Contains(records, "structuredContent.data.result.lease_created") {
				t.Errorf("%s evidence records unstable fields: %v", step.ID, step.Evidence.Record)
			}
		case "debug_stack_trace", "debug_scopes", "debug_variables":
			readSteps++
			if strings.Contains(records, "structuredContent.data.result.session_id") || !strings.Contains(records, "structuredContent.data.result.lease_created") {
				t.Errorf("%s evidence records unstable fields: %v", step.ID, step.Evidence.Record)
			}
		case "debug_evaluate":
			evaluateSteps++
			for _, unstable := range []string{"structuredContent.code", "structuredContent.message", "structuredContent.data.result.session_id"} {
				if strings.Contains(records, unstable) {
					t.Errorf("%s evidence records success-absent field %s", step.ID, unstable)
				}
			}
		}
	}
	if breakpointSteps != 3 || readSteps != 3 || evaluateSteps != 1 {
		t.Fatalf("evidence step counts breakpoints=%d reads=%d evaluate=%d", breakpointSteps, readSteps, evaluateSteps)
	}
}

func TestProviderCodeDebugConfigNeverFallsBackAfterMissingAdmittedBinding(t *testing.T) {
	t.Parallel()
	config, err := providerCodeDebugConfig("python", map[string]providerAdapterBinding{})
	if err != nil {
		t.Fatalf("providerCodeDebugConfig() missing diagnostic binding: %v", err)
	}
	if config["policy"] != "disabled" {
		t.Fatalf("providerCodeDebugConfig() = %#v, want explicitly disabled debug policy", config)
	}
	if _, found := config["adapter_command"]; found {
		t.Fatalf("providerCodeDebugConfig() = %#v, blocked provider must not receive an adapter command", config)
	}
	plan := EnvironmentCollectionPlan{Adapters: []EnvironmentAdapterPlan{{
		Key: EnvironmentKeyAdapterPython, Expected: EnvironmentExpected{Source: "explicit"},
	}}}
	manifest := EnvironmentManifest{Prerequisites: []EnvironmentPrerequisite{{
		Key: EnvironmentKeyAdapterPython, Result: testPassResult(),
		Resolved: EnvironmentResolved{Path: `C:\Python314\python.exe`, Source: "path_fallback"},
	}}}
	if _, err := buildProviderAdapterBindings(plan, manifest); err == nil {
		t.Fatal("manifest source drift must be rejected instead of falling back")
	}
}

func TestProviderAdapterBindingsKeepDiagnosticBlockedProviderLocal(t *testing.T) {
	t.Parallel()
	plan := EnvironmentCollectionPlan{Adapters: []EnvironmentAdapterPlan{
		{Key: EnvironmentKeyAdapterGo, Expected: EnvironmentExpected{Source: "path_fallback"}},
		{Key: EnvironmentKeyAdapterPython, Expected: EnvironmentExpected{Source: "provider_default"}},
	}}
	manifest := EnvironmentManifest{Prerequisites: []EnvironmentPrerequisite{
		{Key: EnvironmentKeyAdapterGo, Result: testPassResult(), Resolved: EnvironmentResolved{Path: `C:\Tools\dlv.exe`, Source: "path_fallback"}},
		{Key: EnvironmentKeyAdapterPython, Result: blockedResult("adapter.python", "diagnostic blocker")},
	}}
	bindings, err := buildProviderAdapterBindings(plan, manifest)
	if err != nil {
		t.Fatalf("allowed diagnostic blocker must not invalidate other provider bindings: %v", err)
	}
	if _, err := providerCodeDebugConfig("go", bindings); err != nil {
		t.Fatalf("PASS Go binding must remain executable: %v", err)
	}
	pythonConfig, err := providerCodeDebugConfig("python", bindings)
	if err != nil {
		t.Fatalf("blocked Python adapter must remain a local diagnostic blocker: %v", err)
	}
	if pythonConfig["policy"] != "disabled" {
		t.Fatalf("blocked Python adapter config = %#v, want disabled without PATH fallback", pythonConfig)
	}
}

func TestFixtureAdapterBindingOnlyBlocksDebugPreflight(t *testing.T) {
	t.Parallel()
	if fixtureCommandRequiresAdapterBinding("preflight.cmd", []string{"runtime"}) {
		t.Fatal("runtime preflight must stay executable when only debug capability is blocked")
	}
	if fixtureCommandRequiresAdapterBinding("preflight.cmd", []string{"build"}) {
		t.Fatal("build preflight must stay executable when only debug capability is blocked")
	}
	if !fixtureCommandRequiresAdapterBinding("preflight.cmd", []string{"debug"}) {
		t.Fatal("debug preflight must require the admitted adapter binding")
	}
	if _, err := providerFixtureEnvironment("java", map[string]providerAdapterBinding{}, `C:\Users\Validation\.superdev`); err == nil {
		t.Fatal("debug fixture environment must reject a missing JVM adapter binding")
	}
	config, err := providerCodeDebugConfig("java", map[string]providerAdapterBinding{})
	if err != nil || config["policy"] != "disabled" {
		t.Fatalf("missing JVM binding config = %#v, %v; want disabled diagnostic policy", config, err)
	}
}

func TestExecuteProviderKeepsRuntimePassWhenOnlyDebugAdapterIsBlocked(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/ready":
			_, _ = response.Write([]byte(`{"ready":true,"provider":"java"}`))
		case "/probe":
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload["outcome"] == "error" {
				response.WriteHeader(http.StatusInternalServerError)
				_, _ = response.Write([]byte(`{"ok":false,"code":"fixture_controlled_error"}`))
				return
			}
			_, _ = response.Write([]byte(`{"ok":true,"code":"fixture_ok"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	port, err := strconv.Atoi(strings.TrimPrefix(server.URL, "http://127.0.0.1:"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := FixtureManifest{Provider: "java", CWD: "fixtures/java", Runtime: FixtureRuntime{Config: map[string]any{}, Env: map[string]string{}}}
	fixture.Build.WindowsCommand = "build.cmd"
	fixture.Run.Port = port
	fixture.Readiness.URL = server.URL + "/ready"
	fixture.Readiness.Status = http.StatusOK
	fixture.Contract.NormalPath = "/probe"

	caller := &providerMixedResultCaller{}
	executor := &ScenarioExecutor{
		client: caller, resultsDir: t.TempDir(), redactor: NewRedactor(), campaignID: "mixed-provider", lane: "core_only",
		variables: map[string]any{
			"project_id": "p1", "project_root": t.TempDir(), "campaign_id": "mixed-provider", "fixture_authorization": "Basic fixture-test",
		},
		providerAdapters: map[string]providerAdapterBinding{}, passed: map[string]bool{},
	}
	executor.fixtureCommandRunner = func(_ context.Context, _, command string, args ...string) stageEvidence {
		started := time.Now().UTC()
		if fixtureCommandRequiresAdapterBinding(command, args) {
			_, bindingErr := providerFixtureEnvironment(fixture.Provider, executor.providerAdapters, executor.agentDataDirectory)
			if bindingErr == nil {
				t.Fatal("debug preflight unexpectedly resolved an ambient adapter")
			}
			return stageEvidence{Stage: "preflight", Tool: filepath.Base(command), Result: blockedResult("provider_adapter_binding", bindingErr.Error())}
		}
		return stageEvidence{Stage: "fixture", Tool: filepath.Base(command), Result: providerStageResult(true, "", started, time.Now().UTC())}
	}

	result := executor.executeProvider(context.Background(), fixture)
	if result.Runtime.PhaseStatus != PhaseStatusPass || result.Debug.PhaseStatus != PhaseStatusBlocked || result.Result.PhaseStatus != PhaseStatusBlocked {
		t.Fatalf("mixed provider result = runtime %s debug %s overall %s", result.Runtime.PhaseStatus, result.Debug.PhaseStatus, result.Result.PhaseStatus)
	}
	if caller.codeDebug["policy"] != "disabled" {
		t.Fatalf("service code_debug = %#v, want disabled without adapter fallback", caller.codeDebug)
	}
	if _, found := caller.codeDebug["adapter_command"]; found {
		t.Fatalf("service code_debug = %#v, must not receive ambient adapter command", caller.codeDebug)
	}
}

func TestLoadRuntimeInputResolvesEveryAdapterCommandFromOneInputDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inputPath := filepath.Join(root, "runtime-input.json")
	input := RuntimeInput{
		SchemaVersion: 1, Kind: "superdev.windows-validation.runtime-input",
		MCPPath: "mcp.exe", InstallerDirectory: "installers", CampaignRoot: "campaigns", ResultsRoot: "results",
		AgentDataDirectory: "agent-data", JVMAdapterCommand: filepath.Join("adapters", "jvm.exe"),
		RemoteGovernanceAttestationPath: "remote-governance-attestation.json",
		GoAdapterCommand:                filepath.Join("adapters", "dlv.exe"), PythonAdapterCommand: filepath.Join("adapters", "python.exe"),
		NodeAdapterCommand: filepath.Join("adapters", "node.exe"), NativeAdapterCommand: filepath.Join("adapters", "lldb-dap.exe"),
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadRuntimeInput(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"jvm": loaded.JVMAdapterCommand, "go": loaded.GoAdapterCommand, "python": loaded.PythonAdapterCommand,
		"node": loaded.NodeAdapterCommand, "native": loaded.NativeAdapterCommand, "governance": loaded.RemoteGovernanceAttestationPath,
	} {
		if !filepath.IsAbs(value) || !strings.HasPrefix(value, root) {
			t.Errorf("%s adapter path = %q, want absolute path below input directory", name, value)
		}
	}
}

func TestLoadRuntimeInputRejectsUnknownFieldsAndTrailingValues(t *testing.T) {
	t.Parallel()
	valid := `{"schema_version":1,"kind":"superdev.windows-validation.runtime-input"}`
	tests := []struct {
		name    string
		content string
	}{
		{name: "unknown secret field", content: `{"schema_version":1,"kind":"superdev.windows-validation.runtime-input","debug_token":"must-not-be-ignored"}`},
		{name: "trailing value", content: valid + ` {"debug_token":"must-not-be-ignored"}`},
		{name: "oversized input", content: valid + strings.Repeat(" ", maxRuntimeInputBytes)},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime-input.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadRuntimeInput(path); err == nil {
				t.Fatalf("runtime input accepted %s", test.name)
			}
		})
	}
}

func TestValidateDebugCredentialInputRequiresHiddenValueOnlyForFunctionalLanes(t *testing.T) {
	t.Parallel()
	if err := validateDebugCredentialInput("msi_smoke", ""); err != nil {
		t.Fatalf("MSI smoke must not require a debug credential: %v", err)
	}
	for _, lane := range []string{"nsis_core", "core_only"} {
		if err := validateDebugCredentialInput(lane, ""); err == nil {
			t.Fatalf("%s must require the human-entered one-time credential", lane)
		}
		if err := validateDebugCredentialInput(lane, "one-time-test-value"); err != nil {
			t.Fatalf("%s rejected a non-empty credential: %v", lane, err)
		}
	}
}

func TestRunOptionsNeverSerializeDebugCredentialValue(t *testing.T) {
	t.Parallel()
	encoded, err := json.Marshal(RunOptions{PackageRoot: "package", DebugCredentialValue: "must-not-serialize"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-serialize") || strings.Contains(string(encoded), "DebugCredentialValue") {
		t.Fatalf("RunOptions serialized the one-time debug credential: %s", encoded)
	}
}
