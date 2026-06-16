// native_provider_test.go 验证 Rust/C/C++ Language Runtime Provider。
//
// 职责：锁定原生系 schema、normalize 与 attach-pid 执行计划。
// 边界：不启动真实 cargo/make/lldb，不验证 codedebug DAP attach。
package langruntime_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestNativeProviderBuildsAttachPidPlan(t *testing.T) {
	p := langruntime.NewNativeProvider(model.LanguageRust)
	norm, diagnostics, err := p.Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/proj",
		Config:      map[string]any{"program": "target/debug/app"},
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))

	plan, diagnostics, err := p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev,
		Config: norm,
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))
	require.NotNil(t, plan.Command)
	assert.Equal(t, langruntime.ReadinessAttachPID, plan.Debugger.Readiness)
	assert.Equal(t, model.CodeDebugProviderNative, plan.Debugger.Adapter)
}

func TestNativeProviderRustBuildPreRun(t *testing.T) {
	p := langruntime.NewNativeProvider(model.LanguageRust)
	norm, diagnostics, err := p.Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/proj",
		Config:      map[string]any{"program": "target/debug/app", "build": "cargo"},
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))

	plan, diagnostics, err := p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev,
		Config: norm,
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))
	require.NotNil(t, plan.Command.PreRun)
	assert.Equal(t, "cargo", plan.Command.PreRun.Executable)
	assert.Equal(t, []string{"build"}, plan.Command.PreRun.Args)
}
