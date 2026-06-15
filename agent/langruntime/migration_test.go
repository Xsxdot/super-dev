// migration_test.go 验证 command runtime 到 language runtime 的迁移预览。
//
// 职责：锁定简单 go run 的可转换结果，以及复杂 shell/env_file 的保守诊断。
// 边界：不修改配置文件，不覆盖迁移 apply 流程。
package langruntime_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestPreviewCommandMigrationGoRunWithInlineEnv(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguageGo, model.RuntimeConfig{
		Type:       model.RuntimeTypeCommand,
		Command:    "ENABLE=true go run ./cmd/server --port 8080",
		WorkingDir: "./server",
	})

	require.True(t, preview.Convertible)
	assert.Equal(t, model.RuntimeTypeLanguage, preview.Runtime.Type)
	assert.Equal(t, "./server", preview.Runtime.CWD)
	assert.Equal(t, map[string]string{"ENABLE": "true"}, preview.Runtime.Env)
	assert.Equal(t, "./cmd/server", preview.Runtime.Config["program"])
	assert.Equal(t, []string{"--port", "8080"}, preview.Runtime.Config["program_args"])
	assert.Empty(t, preview.Diagnostics)
}

func TestPreviewCommandMigrationKeepsComplexShellCommand(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguageGo, model.RuntimeConfig{
		Type:    model.RuntimeTypeCommand,
		Command: "go run ./cmd/server | tee server.log",
	})
	require.False(t, preview.Convertible)
	assert.Equal(t, model.RuntimeTypeCommand, preview.Runtime.Type)
	require.NotEmpty(t, preview.Diagnostics)
	assert.Equal(t, "command_shell_features_unsupported", preview.Diagnostics[0].Code)
}

func TestPreviewCommandMigrationReportsEnvFile(t *testing.T) {
	// spec 迁移策略：env_file 必须显式处理，不能安全展开时保留 command 并给 diagnostic
	preview := langruntime.PreviewCommandMigration(model.LanguageGo, model.RuntimeConfig{
		Type:    model.RuntimeTypeCommand,
		Command: "go run ./cmd/server",
		EnvFile: ".env",
	})
	require.False(t, preview.Convertible)
	require.NotEmpty(t, preview.Diagnostics)
	assert.Equal(t, "env_file_requires_apply_flow", preview.Diagnostics[0].Code)
}

func TestPreviewNodeMigrationPlainNode(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguageNode, model.RuntimeConfig{
		Type: model.RuntimeTypeCommand, Command: "node src/index.js", WorkingDir: "./web",
	})
	require.True(t, preview.Convertible)
	assert.Equal(t, "src/index.js", preview.Runtime.Config["program"])
}

func TestPreviewNodeMigrationPnpmUsesEscapeHatch(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguageNode, model.RuntimeConfig{
		Type: model.RuntimeTypeCommand, Command: "pnpm worker", WorkingDir: "./worker",
	})
	require.True(t, preview.Convertible)
	// 两层模型：pnpm 不是 program，进第二层逃生口。
	assert.Equal(t, "pnpm", preview.Runtime.Config["runtime_executable"])
	assert.Equal(t, []string{"worker"}, preview.Runtime.Config["runtime_args"])
}

func TestPreviewPythonMigrationProgram(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguagePython, model.RuntimeConfig{
		Type: model.RuntimeTypeCommand, Command: "python app.py",
	})
	require.True(t, preview.Convertible)
	assert.Equal(t, "app.py", preview.Runtime.Config["program"])
}

func TestPreviewPythonMigrationModule(t *testing.T) {
	preview := langruntime.PreviewCommandMigration(model.LanguagePython, model.RuntimeConfig{
		Type: model.RuntimeTypeCommand, Command: "python -m myapp.server",
	})
	require.True(t, preview.Convertible)
	assert.Equal(t, "myapp.server", preview.Runtime.Config["module"])
}
