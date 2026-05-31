// Package template_test 验证版本化流水线模板模型与 digest。
//
// 职责：
//   - 验证模板 digest 的稳定性
//   - 验证模板最小结构校验
//
// 边界：
//   - 不访问模板库文件系统
//   - 不展开 include 或执行流水线步骤
package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinetemplate "github.com/superdev/agent/template"
)

func TestDigestStableForSameTemplate(t *testing.T) {
	tpl := pipelinetemplate.Template{
		ID:      "go-build",
		Name:    "Go Build",
		Version: "1.0.0",
		Inputs: map[string]pipelinetemplate.Input{
			"app_name": {Label: "应用名", Type: "string", Required: true},
		},
		Steps: []pipelinetemplate.Step{{
			Name: "Build",
			Type: "local_command",
			With: map[string]interface{}{"cmd": "go build -o ./dist/${vars.app_name}"},
		}},
	}
	a, err := pipelinetemplate.Digest(tpl)
	require.NoError(t, err)
	b, err := pipelinetemplate.Digest(tpl)
	require.NoError(t, err)
	assert.Equal(t, a, b)
	assert.Contains(t, a, "sha256:")
}

func TestValidateTemplateRequiresIDNameVersionAndStep(t *testing.T) {
	err := pipelinetemplate.Validate(pipelinetemplate.Template{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")
}
