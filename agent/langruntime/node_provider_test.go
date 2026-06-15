// node_provider_test.go 验证 Node Language Runtime Provider。
//
// 职责：锁定 Node schema、配置建议、normalize 等基础契约。
// 边界：BuildPlan 与 registry 注册在后续检查点单独验证。
package langruntime_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestNodeProviderLanguageAndCapabilities(t *testing.T) {
	p := langruntime.NewNodeProvider()
	assert.Equal(t, model.LanguageNode, p.Language())
	caps := p.Capabilities()
	assert.Equal(t, langruntime.DebugReadyBySignal, caps.DebugReady)
}

func TestNodeRuntimeSchemaHasProgram(t *testing.T) {
	schema := langruntime.NewNodeProvider().RuntimeSchema(context.Background())
	assert.Equal(t, model.LanguageNode, schema.Language)
	require.NotEmpty(t, schema.Fields)
	assert.Equal(t, "program", schema.Fields[0].Key)
}

func TestNodeSuggestReadsPackageJSONMain(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, writeFile(filepath.Join(root, "package.json"), `{"main":"src/index.js"}`))
	out, err := langruntime.NewNodeProvider().SuggestConfig(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: root, CWD: ".",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	assert.Equal(t, "src/index.js", out[0].Config["program"])
}

func TestNodeNormalizeRejectsNonStringProgram(t *testing.T) {
	_, diagnostics, err := langruntime.NewNodeProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo", Config: map[string]any{"program": 42},
	})
	require.NoError(t, err)
	require.NotEmpty(t, diagnostics)
	assert.Equal(t, langruntime.SeverityError, diagnostics[0].Severity)
}

func TestNodeBuildPlanStartDevPlainNode(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"program": "src/index.js"}}
	plan, diagnostics, err := langruntime.NewNodeProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg,
	})
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	require.NotNil(t, plan.Command)
	assert.Equal(t, "node", plan.Command.Executable)
	assert.Equal(t, []string{"src/index.js"}, plan.Command.Args)
	require.NotNil(t, plan.Debugger)
	assert.Equal(t, langruntime.ReadinessSignalAttach, plan.Debugger.Readiness)
	assert.Equal(t, "SIGUSR1", plan.Debugger.Signal)
}

func TestNodeBuildPlanEscapeHatchPnpm(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{
		"runtime_executable": "pnpm", "runtime_args": []any{"worker"},
	}}
	plan, _, err := langruntime.NewNodeProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg,
	})
	require.NoError(t, err)
	require.NotNil(t, plan.Command)
	assert.Equal(t, "pnpm", plan.Command.Executable)
	assert.Equal(t, []string{"worker"}, plan.Command.Args)
	// 逃生口仍声明 signal readiness（attach 时发信号给 node 子进程）。
	require.NotNil(t, plan.Debugger)
	assert.Equal(t, langruntime.ReadinessSignalAttach, plan.Debugger.Readiness)
}

func TestNodeBuildPlanNodeArgsBeforeProgram(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{
		"program": "app.js", "node_args": []any{"--enable-source-maps"}, "program_args": []any{"--port", "9000"},
	}}
	plan, _, err := langruntime.NewNodeProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartNormal, Config: cfg,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"--enable-source-maps", "app.js", "--port", "9000"}, plan.Command.Args)
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
