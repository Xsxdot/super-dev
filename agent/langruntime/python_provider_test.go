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
