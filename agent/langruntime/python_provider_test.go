// python_provider_test.go 验证 Python Language Runtime Provider。
//
// 职责：锁定 Python schema、配置建议、normalize 等基础契约。
// 边界：BuildPlan 与 registry 注册在后续检查点单独验证。
package langruntime_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestPythonProviderCapabilitiesPrearm(t *testing.T) {
	caps := langruntime.NewPythonProvider().Capabilities()
	assert.Equal(t, langruntime.DebugReadyByPrearm, caps.DebugReady)
}

func TestPythonRuntimeSchemaHasProgramAndModule(t *testing.T) {
	schema := langruntime.NewPythonProvider().RuntimeSchema(context.Background())
	assert.Equal(t, model.LanguagePython, schema.Language)
	keys := map[string]bool{}
	for _, field := range schema.Fields {
		keys[field.Key] = true
	}
	assert.True(t, keys["program"])
	assert.True(t, keys["module"])
}

func TestPythonSuggestFindsMainPy(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, writeFile(filepath.Join(root, "main.py"), "if __name__ == '__main__':\n    pass\n"))
	out, err := langruntime.NewPythonProvider().SuggestConfig(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: root, CWD: ".",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	assert.Equal(t, "main.py", out[0].Config["program"])
}

func TestPythonNormalizeRequiresEntry(t *testing.T) {
	_, diagnostics, err := langruntime.NewPythonProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		Config:      map[string]any{},
	})
	require.NoError(t, err)
	assert.True(t, hasDiag(diagnostics, "python_entry_required"), "diagnostics=%v", diagnostics)
}

func TestPythonNormalizeAllowsModule(t *testing.T) {
	_, diagnostics, err := langruntime.NewPythonProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		Config:      map[string]any{"module": "myapp.server"},
	})
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}

func TestPythonNormalizeAllowsEscapeHatch(t *testing.T) {
	_, diagnostics, err := langruntime.NewPythonProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		Config:      map[string]any{"runtime_executable": "make"},
	})
	require.NoError(t, err)
	assert.Empty(t, diagnostics)
}

func TestPythonNormalizeRejectsCWDOutsideProjectRoot(t *testing.T) {
	_, diagnostics, err := langruntime.NewPythonProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "../outside",
		Config:      map[string]any{"program": "main.py"},
	})
	require.NoError(t, err)
	assert.True(t, hasDiag(diagnostics, "runtime_cwd_outside_project"), "diagnostics=%v", diagnostics)
}

func TestPythonNormalizeRejectsProgramOutsideProjectRoot(t *testing.T) {
	_, diagnostics, err := langruntime.NewPythonProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
		Config:      map[string]any{"program": "../../outside/main.py"},
	})
	require.NoError(t, err)
	assert.True(t, hasDiag(diagnostics, "runtime_program_outside_project"), "diagnostics=%v", diagnostics)
}

func TestPythonStartDevPrearmListen(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"program": "main.py"}}
	plan, diagnostics, err := langruntime.NewPythonProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg, DebugPort: 5678,
	})
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	require.NotNil(t, plan.Command)
	assert.Equal(t, "python", plan.Command.Executable)
	// dev prearm：python -m debugpy --listen 127.0.0.1:5678 main.py，不带 --wait-for-client。
	assert.Equal(t, []string{"-m", "debugpy", "--listen", "127.0.0.1:5678", "main.py"}, plan.Command.Args)
	assert.NotContains(t, plan.Command.Args, "--wait-for-client")
	require.NotNil(t, plan.Debugger)
	assert.Equal(t, langruntime.ReadinessPrearmListen, plan.Debugger.Readiness)
	assert.Equal(t, 5678, plan.Debugger.Port)
}

func TestPythonStartDevMissingPortDiagnostic(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"program": "main.py"}}
	_, diagnostics, err := langruntime.NewPythonProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg,
	})
	require.NoError(t, err)
	require.NotEmpty(t, diagnostics)
	assert.Equal(t, "debug_port_required", diagnostics[0].Code)
}

func TestPythonStartNormalPlainPython(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"program": "main.py"}}
	plan, _, err := langruntime.NewPythonProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartNormal, Config: cfg,
	})
	require.NoError(t, err)
	// start_normal 不预埋 debugpy。
	assert.Equal(t, []string{"main.py"}, plan.Command.Args)
	assert.NotContains(t, plan.Command.Args, "debugpy")
}

func TestPythonModuleMode(t *testing.T) {
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"module": "myapp.server"}}
	plan, _, err := langruntime.NewPythonProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartNormal, Config: cfg,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"-m", "myapp.server"}, plan.Command.Args)
}

func TestPythonDebugLaunchRequiresProgram(t *testing.T) {
	for name, config := range map[string]map[string]any{
		"module":       {"module": "myapp.server"},
		"escape_hatch": {"runtime_executable": "make"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := langruntime.NormalizedRuntimeConfig{
				ProjectRoot: "/repo",
				CWD:         "/repo",
				Config:      config,
			}
			_, diagnostics, err := langruntime.NewPythonProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
				Intent: langruntime.IntentDebugLaunch,
				Config: cfg,
			})
			require.NoError(t, err)
			assert.True(t, hasDiag(diagnostics, "python_debug_program_required"), "diagnostics=%v", diagnostics)
		})
	}
}
