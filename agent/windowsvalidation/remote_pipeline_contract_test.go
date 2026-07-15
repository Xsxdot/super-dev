// remote_pipeline_contract_test.go 验证 Windows 真机包的远端根目录状态机。
//
// 职责：
//   - 锁定 deploy A 与后续操作的 create/existing 根目录合同；
//   - 保证根目录预检先于任何远端写入且留下可审核日志；
//   - 保证 project pipeline 与每次场景调用显式透传受限 root_mode。
//
// 边界：
//   - 不连接真实 Linux Agent；
//   - 不执行模板中的 shell 命令。
package windowsvalidation

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

func TestRemotePipelineRootLifecycleContract(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(filepath.Join("..", "..", "validation", "windows-real"))
	preview := pipelinetemplate.PreviewFile(filepath.Join(root, "pipeline", "templates", "remote-validation-deploy.yaml"))
	if len(preview.Errors) != 0 {
		t.Fatalf("preview remote pipeline template: %v", preview.Errors)
	}

	rootMode, ok := preview.Template.Inputs["root_mode"]
	if !ok {
		t.Fatal("remote pipeline template is missing root_mode input")
	}
	if rootMode.Type != "select" || !rootMode.Required || rootMode.Default != "" || !slices.Equal(rootMode.Options, []string{"create", "existing"}) {
		t.Fatalf("root_mode input = %+v, want required select [create existing] without an implicit default", rootMode)
	}

	preflightIndex, preflight := remoteTemplateStep(t, preview.Template.Steps, "Preflight Campaign Root")
	initializeIndex, initialize := remoteTemplateStep(t, preview.Template.Steps, "Initialize Campaign Root")
	transferIndex, _ := remoteTemplateStep(t, preview.Template.Steps, "Transfer Artifact")
	cleanupIndex, cleanup := remoteTemplateStep(t, preview.Template.Steps, "Cleanup Campaign Root")
	if preflightIndex != 0 || initializeIndex <= preflightIndex || transferIndex <= initializeIndex || cleanupIndex <= preflightIndex {
		t.Fatalf("remote root step order is unsafe: preflight=%d initialize=%d transfer=%d cleanup=%d", preflightIndex, initializeIndex, transferIndex, cleanupIndex)
	}
	preflightCommand := remoteTemplateCommand(t, preflight)
	for _, marker := range []string{
		"root_mode='${vars.root_mode}'",
		"case \"$root_mode\" in",
		"create)",
		"test ! -e \"$root\"",
		"test ! -L \"$root\"",
		"root_state=absent",
		"existing)",
		"test -d \"$root\"",
		"test -f \"$root/.campaign-owner\"",
		"test \"$(cat \"$root/.campaign-owner\")\" = \"$campaign\"",
		"root_state=owned",
		`"stage":"preflight_root"`,
		`"root_mode":"%s"`,
		`"root_state":"%s"`,
	} {
		if !strings.Contains(preflightCommand, marker) {
			t.Errorf("root preflight command is missing %q", marker)
		}
	}
	for _, forbidden := range []string{"mkdir ", "rm ", "chmod ", "touch ", "eval ", "sh -c", "bash -c"} {
		if strings.Contains(preflightCommand, forbidden) {
			t.Errorf("read-only root preflight contains forbidden shell fragment %q", forbidden)
		}
	}
	createBranch := remoteShellCaseBranch(t, preflightCommand, "create)", "existing)")
	if strings.Contains(createBranch, ".campaign-owner") || strings.Contains(createBranch, "test -d \"$root\"") {
		t.Fatal("create preflight still accepts an existing campaign root based on owner identity")
	}
	if strings.Count(preflightCommand, "${vars.root_mode}") != 1 {
		t.Fatalf("root_mode appears %d times in shell command, want one single-quoted assignment", strings.Count(preflightCommand, "${vars.root_mode}"))
	}
	if !slices.Equal(initialize.Needs, []string{"Preflight Campaign Root"}) {
		t.Fatalf("initialize dependencies = %v, want root preflight", initialize.Needs)
	}
	initializeCommand := remoteTemplateCommand(t, initialize)
	for _, marker := range []string{"test ! -e \"$root\"", "test ! -L \"$root\"", "mkdir \"$root\"", "test -d \"$root\"", "test \"$(cat \"$root/.campaign-owner\")\" = \"$campaign\""} {
		if !strings.Contains(initializeCommand, marker) {
			t.Errorf("root initialize command is missing race-safe marker %q", marker)
		}
	}
	if strings.Contains(initializeCommand, "mkdir -p \"$root\"") {
		t.Fatal("create mode must use atomic mkdir for the exact root, not idempotent mkdir -p")
	}
	if !slices.Equal(cleanup.Needs, []string{"Preflight Campaign Root"}) {
		t.Fatalf("cleanup dependencies = %v, want root preflight", cleanup.Needs)
	}

	assertRemoteProjectRootModeContract(t, root)
	assertRemoteScenarioRootModeContract(t, root)
}

func assertRemoteProjectRootModeContract(t *testing.T, root string) {
	t.Helper()
	var project map[string]any
	if err := readJSONFile(filepath.Join(root, "pipeline", "project-pipeline.json"), &project); err != nil {
		t.Fatal(err)
	}
	variables := remoteMapValue(t, project, "variables")
	if variables["root_mode"] != "create" {
		t.Fatalf("project root_mode default = %v, want fail-closed create", variables["root_mode"])
	}
	pipeline := remoteMapValue(t, project, "pipeline")
	deploy, ok := pipeline["deploy"].([]any)
	if !ok || len(deploy) != 1 {
		t.Fatalf("project deploy steps = %T %v, want one include", pipeline["deploy"], pipeline["deploy"])
	}
	include, ok := deploy[0].(map[string]any)
	if !ok {
		t.Fatalf("project deploy include = %T, want object", deploy[0])
	}
	with := remoteMapValue(t, include, "with")
	includeVariables := remoteMapValue(t, with, "vars")
	if includeVariables["root_mode"] != "${root_mode}" {
		t.Fatalf("template include root_mode = %v, want project variable passthrough", includeVariables["root_mode"])
	}
}

func assertRemoteScenarioRootModeContract(t *testing.T, root string) {
	t.Helper()
	var scenario Scenario
	if err := readJSONFile(filepath.Join(root, "scenarios", "remote-pipeline.json"), &scenario); err != nil {
		t.Fatal(err)
	}
	if _, exposed := scenario.Variables["root_mode"]; exposed {
		t.Fatal("root_mode must remain fixed by each scenario call, not exposed as runtime input")
	}
	hostPreflight := remoteScenarioStep(t, scenario.Steps, "pipeline-host-id-preflight")
	tagLocked := false
	for _, assertion := range hostPreflight.Expect.Assertions {
		expected, ok := assertion.Value.(map[string]any)
		if assertion.Operator != "contains_item" || !ok || expected["id"] != "{{linux_host_id}}" || expected["is_self"] != false {
			continue
		}
		tags, ok := expected["tags"].([]any)
		if ok && slices.Equal(tags, []any{"superdev-validation-dedicated-resettable"}) {
			tagLocked = true
		}
	}
	if !tagLocked {
		t.Fatal("pipeline Host preflight does not mechanically bind the exact dedicated-resettable governance tag to linux_host_id")
	}
	wantModes := map[string]string{
		"pipeline-config-validate":  "create",
		"pipeline-deploy-a":         "create",
		"pipeline-deploy-b":         "existing",
		"pipeline-rollback-a":       "existing",
		"pipeline-cleanup":          "existing",
		"pipeline-cleanup-on-abort": "existing",
	}
	seen := map[string]bool{}
	for _, step := range append(append([]ScenarioStep{}, scenario.Steps...), scenario.Cleanup...) {
		if step.Tool != "validate_project_pipeline" && step.Tool != "deploy_project_pipeline" {
			continue
		}
		variables := remoteMapValue(t, step.Arguments, "variables")
		want, exists := wantModes[step.ID]
		if !exists {
			t.Errorf("unexpected remote pipeline execution step %s", step.ID)
			continue
		}
		seen[step.ID] = true
		if variables["root_mode"] != want {
			t.Errorf("%s root_mode = %v, want %s", step.ID, variables["root_mode"], want)
		}
	}
	for id := range wantModes {
		if !seen[id] {
			t.Errorf("remote pipeline root-mode step %s is missing", id)
		}
	}
	waitA := remoteScenarioStep(t, scenario.Steps, "pipeline-wait-a")
	if waitA.Capture["pipeline_run_a_complete"] != "structuredContent.data.runs.0.id" {
		t.Fatalf("pipeline-wait-a capture = %v, want successful A completion ownership gate", waitA.Capture)
	}
	abortCleanup := remoteScenarioStep(t, scenario.Cleanup, "pipeline-cleanup-on-abort")
	if abortCleanup.RunIf != "variable_set:pipeline_run_a_complete&&variable_unset:pipeline_run_cleanup" {
		t.Fatalf("abort cleanup run_if = %q, want successful A completion ownership gate", abortCleanup.RunIf)
	}

	wantLogProof := map[string]string{
		"pipeline-logs-a":                `"stage":"preflight_root","root_mode":"create","root_state":"absent"`,
		"pipeline-logs-b":                `"stage":"preflight_root","root_mode":"existing","root_state":"owned"`,
		"pipeline-logs-rollback-a":       `"stage":"preflight_root","root_mode":"existing","root_state":"owned"`,
		"pipeline-logs-cleanup":          `"stage":"preflight_root","root_mode":"existing","root_state":"owned"`,
		"pipeline-logs-cleanup-on-abort": `"stage":"preflight_root","root_mode":"existing","root_state":"owned"`,
	}
	allSteps := append(append([]ScenarioStep{}, scenario.Steps...), scenario.Cleanup...)
	for id, proof := range wantLogProof {
		step := remoteScenarioStep(t, allSteps, id)
		found := false
		for _, assertion := range step.Expect.Assertions {
			if assertion.Path == "structuredContent.data.logs" && assertion.Operator == "contains" && assertion.Value == proof {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not require root preflight proof %q", id, proof)
		}
	}
}

func remoteTemplateStep(t *testing.T, steps []pipelinetemplate.Step, name string) (int, pipelinetemplate.Step) {
	t.Helper()
	for index, step := range steps {
		if step.Name == name {
			return index, step
		}
	}
	t.Fatalf("remote template step %q is missing", name)
	return -1, pipelinetemplate.Step{}
}

func remoteTemplateCommand(t *testing.T, step pipelinetemplate.Step) string {
	t.Helper()
	command, ok := step.With["cmd"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		t.Fatalf("remote template step %q command = %T %v", step.Name, step.With["cmd"], step.With["cmd"])
	}
	return command
}

func remoteShellCaseBranch(t *testing.T, command, start, end string) string {
	t.Helper()
	startIndex := strings.Index(command, start)
	endIndex := strings.Index(command, end)
	if startIndex < 0 || endIndex <= startIndex {
		t.Fatalf("shell case branch %q..%q is missing", start, end)
	}
	return command[startIndex:endIndex]
}

func remoteMapValue(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %T %v, want object", key, object[key], object[key])
	}
	return value
}

func remoteScenarioStep(t *testing.T, steps []ScenarioStep, id string) ScenarioStep {
	t.Helper()
	for _, step := range steps {
		if step.ID == id {
			return step
		}
	}
	t.Fatal(fmt.Sprintf("remote scenario step %q is missing", id))
	return ScenarioStep{}
}
