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

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
