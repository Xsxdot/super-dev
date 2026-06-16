// jvm_provider_test.go 验证 Java/Kotlin Language Runtime Provider。
//
// 职责：锁定 JVM schema、normalize 与 prearm-listen 执行计划。
// 边界：不启动真实 java/java-debug，不验证 DAP/JDWP attach。
package langruntime_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestJVMProviderInjectsJdwpOnStartDev(t *testing.T) {
	p := langruntime.NewJVMProvider(model.LanguageJava)
	norm, diagnostics, err := p.Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/proj",
		Config:      map[string]any{"program": "App", "classpath": "build"},
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))

	plan, diagnostics, err := p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent:    langruntime.IntentStartDev,
		Config:    norm,
		DebugPort: 5005,
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))
	require.NotNil(t, plan.Command)
	joined := strings.Join(plan.Command.Args, " ")
	assert.Contains(t, joined, "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5005")
	assert.Equal(t, langruntime.ReadinessPrearmListen, plan.Debugger.Readiness)
	assert.Equal(t, 5005, plan.Debugger.Port)
}

func TestJVMStartDevRequiresDebugPort(t *testing.T) {
	p := langruntime.NewJVMProvider(model.LanguageJava)
	norm, diagnostics, err := p.Normalize(context.Background(), langruntime.RuntimeConfigInput{
		ProjectRoot: "/proj",
		Config:      map[string]any{"program": "App"},
	})
	require.NoError(t, err)
	require.False(t, langruntime.HasErrorDiagnostic(diagnostics))

	_, diagnostics, err = p.BuildPlan(context.Background(), langruntime.BuildPlanInput{
		Intent: langruntime.IntentStartDev,
		Config: norm,
	})
	require.NoError(t, err)
	assert.True(t, langruntime.HasErrorDiagnostic(diagnostics))
}
