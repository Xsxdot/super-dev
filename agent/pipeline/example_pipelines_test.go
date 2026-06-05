// Package pipeline_test 验证 examples 中的流水线声明可由内置模板展开。
//
// Responsibilities:
//   - Parse each example pipeline manifest.
//   - Merge deterministic reserved variables.
//   - Expand include templates and build a runnable plan skeleton.
//
// Boundaries:
//   - Does not execute build commands.
//   - Does not connect to local-02.
package pipeline_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
	"gopkg.in/yaml.v3"
)

func TestExamplePipelinesExpandAndPlan(t *testing.T) {
	root := repoRoot(t)
	builtins, err := pipelinetemplate.LoadBuiltins()
	require.NoError(t, err)
	resolver := pipelinetemplate.NewStore(t.TempDir(), builtins, root)

	for _, name := range []string{
		"go-http",
		"node-http",
		"python-http",
		"java-springboot",
		"rust-http",
		"php-http",
		"vue-go-combined",
	} {
		t.Run(name, func(t *testing.T) {
			var p model.Pipeline
			data, err := os.ReadFile(filepath.Join(root, "examples", name, "pipeline.yaml"))
			require.NoError(t, err)
			require.NoError(t, yaml.Unmarshal(data, &p))

			p.Variables = pipeline.MergeVariables(p.Variables, pipeline.PreviewReservedVars(pipeline.ReservedVarOptions{
				Workspace: root,
				Version:   "test-version",
				Env:       "e2e",
			}))
			p.Variables = pipelinetemplate.RenderPipelineVariableMap(p.Variables)

			p.Build, err = pipelinetemplate.ExpandSteps(p.Build, resolver, p.Variables, 5)
			require.NoError(t, err)
			p.Deploy, err = pipelinetemplate.ExpandSteps(p.Deploy, resolver, p.Variables, 5)
			require.NoError(t, err)

			plan, run, err := pipeline.BuildPlan(name, p, []model.HostRef{{ID: "local-02", Name: "100.90.99.61"}})
			require.NoError(t, err)
			assert.NotEmpty(t, plan.Phases[model.PhaseBuild])
			assert.NotEmpty(t, plan.Phases[model.PhaseDeploy])
			assert.NotEmpty(t, run.StepRuns)
			assertLocal02BinaryBuildTargets(t, name, plan.Phases[model.PhaseBuild])
		})
	}
}

func assertLocal02BinaryBuildTargets(t *testing.T, name string, steps []model.Step) {
	t.Helper()
	commands := ""
	for _, step := range steps {
		if step.Type == "local_command" {
			commands += " " + stringValue(step.With, "cmd")
		}
	}
	switch name {
	case "go-http", "vue-go-combined":
		assert.Contains(t, commands, "GOOS=linux")
		assert.Contains(t, commands, "GOARCH=amd64")
	case "rust-http":
		assert.Contains(t, commands, "x86_64-unknown-linux-musl")
	}
}

func stringValue(values map[string]interface{}, key string) string {
	raw, ok := values[key]
	if !ok || raw == nil {
		return ""
	}
	value, _ := raw.(string)
	return value
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	require.NoError(t, err)
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
