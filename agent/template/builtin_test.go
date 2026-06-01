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
	for _, id := range []string{"go-binary-build", "vue-standard-build", "archive-package", "systemd-seamless-deploy", "nginx-static-deploy"} {
		tpl, ok := builtins[id]
		require.True(t, ok, id)
		assert.NotEmpty(t, tpl.Inputs)
		assert.NotEmpty(t, tpl.Steps)
	}
}

func TestBuiltinTemplatesHaveInputDescriptionsAndTargetRoles(t *testing.T) {
	builtins, err := pipelinetemplate.LoadBuiltins()
	require.NoError(t, err)
	for _, tpl := range builtins {
		for name, input := range tpl.Inputs {
			assert.NotEmpty(t, input.Description, "%s input %s needs description", tpl.ID, name)
		}
	}
	assert.Equal(t, "target_role", builtins["systemd-seamless-deploy"].Inputs["role"].Type)
	assert.Equal(t, "target_role", builtins["nginx-static-deploy"].Inputs["role"].Type)
	assert.Equal(t, "file_list", builtins["archive-package"].Inputs["files"].Type)
	assert.Equal(t, "${output}/app", builtins["go-binary-build"].Inputs["output"].Default)
	assert.True(t, builtins["systemd-seamless-deploy"].Inputs["artifact"].Required)
	assert.Empty(t, builtins["systemd-seamless-deploy"].Inputs["artifact"].Default)
	assert.True(t, builtins["nginx-static-deploy"].Inputs["artifact"].Required)
	assert.Empty(t, builtins["nginx-static-deploy"].Inputs["artifact"].Default)
}

func TestGoBinaryBuildPackagingIsOptional(t *testing.T) {
	builtins, err := pipelinetemplate.LoadBuiltins()
	require.NoError(t, err)
	require.Len(t, builtins["go-binary-build"].Steps, 2)

	step, err := pipelinetemplate.RenderStepTemplateVars(builtins["go-binary-build"].Steps[1], map[string]interface{}{
		"output": "/tmp/app",
	})

	require.NoError(t, err)
	assert.Equal(t, `"" != ""`, step.RunIf)
}
