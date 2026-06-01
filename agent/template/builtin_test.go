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
	for _, id := range []string{
		"archive-package",
		"go-binary-build",
		"java-maven-build",
		"nginx-static-deploy",
		"node-standard-build",
		"php-standard-build",
		"python-standard-build",
		"rust-cargo-build",
		"systemd-seamless-deploy",
		"vue-go-combined-build",
		"vue-standard-build",
	} {
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

func TestSystemdSeamlessDeployIsSelfContained(t *testing.T) {
	builtins, err := pipelinetemplate.LoadBuiltins()
	require.NoError(t, err)

	tpl := builtins["systemd-seamless-deploy"]
	for _, name := range []string{
		"role",
		"artifact",
		"release_dir",
		"current_dir",
		"app_name",
		"service_name",
		"exec_start",
		"working_dir",
		"port",
		"health_path",
	} {
		input, ok := tpl.Inputs[name]
		require.True(t, ok, name)
		assert.True(t, input.Required, name)
		assert.NotEmpty(t, input.Description, name)
	}
	assert.Equal(t, "target_role", tpl.Inputs["role"].Type)

	stepNames := make([]string, 0, len(tpl.Steps))
	for _, step := range tpl.Steps {
		stepNames = append(stepNames, step.Name)
	}
	assert.Contains(t, stepNames, "Write Service")
	assert.Contains(t, stepNames, "Daemon Reload")
	assert.Contains(t, stepNames, "Health Check")
}
