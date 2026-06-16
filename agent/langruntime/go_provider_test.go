// go_provider_test.go 验证 Go Language Runtime Provider。
//
// 职责：锁定 Go schema、配置建议、normalize、四种 intent 的执行计划。
// 边界：不启动真实 go/dlv 进程，不验证 codedebug/process 编排层。
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

func TestGoProviderSchemaMinimalFields(t *testing.T) {
	schema := langruntime.NewGoProvider().RuntimeSchema(context.Background())
	require.NoError(t, schema.Validate())
	require.Len(t, schema.Fields, 3)
	assert.Equal(t, "program", schema.Fields[0].Key)
	assert.True(t, schema.Fields[0].Required)
	assert.Equal(t, "program_args", schema.Fields[1].Key)
	assert.Equal(t, "build_flags", schema.Fields[2].Key)
}

func TestGoProviderCapabilitiesAttachStrategy(t *testing.T) {
	caps := langruntime.NewGoProvider().Capabilities()
	assert.Equal(t, langruntime.DebugReadyByAttach, caps.DebugReady)
	assert.True(t, caps.DebugLaunch)
	assert.True(t, caps.StopOnEntry)
}

func TestGoProviderSuggestsUniqueCmdMainPackage(t *testing.T) {
	root := t.TempDir()
	serverDir := filepath.Join(root, "server")
	require.NoError(t, os.MkdirAll(filepath.Join(serverDir, "cmd", "server"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "go.mod"), []byte("module demo\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "cmd", "server", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))

	suggestions, err := langruntime.NewGoProvider().SuggestConfig(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: root,
		CWD:         "./server",
	})
	require.NoError(t, err)
	require.Len(t, suggestions, 1)
	assert.Equal(t, "./cmd/server", suggestions[0].Config["program"])
	assert.Equal(t, "high", suggestions[0].Confidence)
}

func TestGoProviderSuggestsMultipleMainPackagesAsAmbiguous(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "api"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "cmd", "worker"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cmd", "api", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cmd", "worker", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))

	suggestions, err := langruntime.NewGoProvider().SuggestConfig(context.Background(), langruntime.RuntimeConfigInput{ProjectRoot: root})
	require.NoError(t, err)
	require.Len(t, suggestions, 2)
	assert.Equal(t, "medium", suggestions[0].Confidence)
}

func normalizeGoConfig(t *testing.T, input langruntime.RuntimeConfigInput) langruntime.NormalizedRuntimeConfig {
	t.Helper()
	normalized, diagnostics, err := langruntime.NewGoProvider().Normalize(context.Background(), input)
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))
	return normalized
}

func TestGoProviderNormalizeDefaultsProgramAndResolvesCWD(t *testing.T) {
	normalized := normalizeGoConfig(t, langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
	})
	assert.Equal(t, "/repo/server", normalized.CWD)
	assert.Equal(t, ".", normalized.Config["program"])
}

func TestGoProviderNormalizeRejectsNonStringProgram(t *testing.T) {
	_, diagnostics, err := langruntime.NewGoProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		Config:      map[string]any{"program": 42},
	})
	require.NoError(t, err)
	require.True(t, langruntime.HasErrorDiagnostic(diagnostics))
	assert.Equal(t, "program_type_invalid", diagnostics[0].Code)
}

func TestGoProviderNormalizeRejectsCWDOutsideProjectRoot(t *testing.T) {
	_, diagnostics, err := langruntime.NewGoProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "../outside",
	})
	require.NoError(t, err)
	require.True(t, langruntime.HasErrorDiagnostic(diagnostics))
	assert.Equal(t, "runtime_cwd_outside_project", diagnostics[0].Code)
}

func TestGoProviderNormalizeRejectsProgramOutsideProjectRoot(t *testing.T) {
	_, diagnostics, err := langruntime.NewGoProvider().Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
		Config:      map[string]any{"program": "../../outside"},
	})
	require.NoError(t, err)
	require.True(t, langruntime.HasErrorDiagnostic(diagnostics))
	assert.Equal(t, "runtime_program_outside_project", diagnostics[0].Code)
}

func TestGoProviderStartDevBuildsThenExecs(t *testing.T) {
	normalized := normalizeGoConfig(t, langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
		Env:         map[string]string{"ENABLE": "true"},
		Config:      map[string]any{"program": "./cmd/server", "program_args": []any{"--port", "8080"}},
	})

	for _, intent := range []langruntime.BuildIntent{langruntime.IntentStartDev, langruntime.IntentStartNormal} {
		plan, diagnostics, err := langruntime.NewGoProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
			Intent:      intent,
			Config:      normalized,
			ArtifactDir: "/data/run-bin/dep-api-dev",
		})
		require.NoError(t, err)
		require.Empty(t, diagnostics)
		require.NotNil(t, plan.Command, intent)
		require.NotNil(t, plan.Command.PreRun, intent)
		require.Nil(t, plan.Debug, intent)

		// PreRun: go build -gcflags=all=-N -l -o <artifact> ./cmd/server
		assert.Equal(t, "go", plan.Command.PreRun.Executable)
		assert.Contains(t, plan.Command.PreRun.Args, "build")
		assert.Contains(t, plan.Command.PreRun.Args, "all=-N -l")
		artifact := "/data/run-bin/dep-api-dev/server"
		assert.Contains(t, plan.Command.PreRun.Args, artifact)
		assert.Contains(t, plan.Command.PreRun.Args, "./cmd/server")

		// exec: <artifact> --port 8080
		assert.Equal(t, artifact, plan.Command.Executable)
		assert.Equal(t, []string{"--port", "8080"}, plan.Command.Args)
		assert.Equal(t, "/repo/server", plan.WorkingDir)
		assert.Equal(t, map[string]string{"ENABLE": "true"}, plan.Env)
	}
}

func TestGoBuildPlanEscapeHatchExecutesVerbatim(t *testing.T) {
	p := langruntime.NewGoProvider()
	cfg := langruntime.NormalizedRuntimeConfig{
		CWD: "/repo/server",
		Config: map[string]any{
			"runtime_executable": "make",
			"runtime_args":       []any{"run"},
		},
	}
	plan, diagnostics, err := p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg, ArtifactDir: "/data/x",
	})
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	// 逃生口：原样执行 make run，不 go build。
	require.NotNil(t, plan.Command)
	assert.Nil(t, plan.Command.PreRun)
	assert.Equal(t, "make", plan.Command.Executable)
	assert.Equal(t, []string{"run"}, plan.Command.Args)
}

func TestGoBuildPlanStartDevDeclaresAttachReadiness(t *testing.T) {
	p := langruntime.NewGoProvider()
	cfg := langruntime.NormalizedRuntimeConfig{CWD: "/repo", Config: map[string]any{"program": "."}}
	plan, _, err := p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev, Config: cfg, ArtifactDir: "/data/x",
	})
	require.NoError(t, err)
	require.NotNil(t, plan.Debugger)
	assert.Equal(t, model.CodeDebugProviderGo, plan.Debugger.Adapter)
	assert.Equal(t, langruntime.ReadinessAttachPID, plan.Debugger.Readiness)
}

func TestGoProviderStartDevRequiresArtifactDir(t *testing.T) {
	normalized := normalizeGoConfig(t, langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
		Config:      map[string]any{"program": "./cmd/server"},
	})
	_, diagnostics, err := langruntime.NewGoProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev,
		Config: normalized,
	})
	require.NoError(t, err)
	require.True(t, langruntime.HasErrorDiagnostic(diagnostics))
	assert.Equal(t, "artifact_dir_required", diagnostics[0].Code)
}

func TestGoProviderDebugLaunchPlan(t *testing.T) {
	normalized := normalizeGoConfig(t, langruntime.RuntimeConfigInput{
		ProjectRoot: "/repo",
		CWD:         "./server",
		Env:         map[string]string{"ENABLE": "true"},
		Config:      map[string]any{"program": "./cmd/server"},
	})
	plan, diagnostics, err := langruntime.NewGoProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent:      langruntime.IntentDebugLaunch,
		Config:      normalized,
		StopOnEntry: true,
	})
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	require.NotNil(t, plan.Debug)
	require.Nil(t, plan.Command)
	assert.Equal(t, model.CodeDebugProviderGo, plan.Debug.Provider)
	assert.Equal(t, "/repo/server/cmd/server", plan.Debug.Program)
	assert.True(t, plan.Debug.StopOnEntry)
	assert.Contains(t, plan.Preview, "dlv")
}

func TestGoProviderAttachPlan(t *testing.T) {
	normalized := normalizeGoConfig(t, langruntime.RuntimeConfigInput{ProjectRoot: "/repo"})
	plan, diagnostics, err := langruntime.NewGoProvider().BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentAttach,
		Config: normalized,
	})
	require.NoError(t, err)
	require.Empty(t, diagnostics)
	require.NotNil(t, plan.Attach)
	assert.Equal(t, "pid", plan.Attach.Mode)
}
