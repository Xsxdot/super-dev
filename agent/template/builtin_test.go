// Package template_test 验证内置流水线模板加载。
//
// 职责：
//   - 验证首批 builtin 模板可从 embed FS 加载
//   - 验证模板包含 inputs 与 steps
//
// 边界：
//   - 不测试模板执行
//   - 不访问用户模板目录
package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinetemplate "github.com/superdev/agent/template"
)

func TestLoadBuiltinTemplates(t *testing.T) {
	builtins, err := pipelinetemplate.LoadBuiltins()
	require.NoError(t, err)
	for _, id := range []string{"go-binary-build", "vue-standard-build", "systemd-seamless-deploy", "nginx-static-deploy"} {
		tpl, ok := builtins[id]
		require.True(t, ok, id)
		assert.NotEmpty(t, tpl.Inputs)
		assert.NotEmpty(t, tpl.Steps)
	}
}
