// Package template_test 验证流水线变量与模板变量渲染。
//
// 职责：
//   - 验证 pipeline 变量 `${name}` 渲染
//   - 验证模板变量 `${vars.name}` 渲染到 Step 副本
//   - 验证模板变量扫描结果稳定去重
//
// 边界：
//   - 不读取模板文件
//   - 不解析 include 或校验 DAG
package template_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinetemplate "github.com/superdev/agent/template"
)

func TestRenderPipelineVars(t *testing.T) {
	got := pipelinetemplate.RenderPipelineVars("${app_name}-${version}", map[string]string{
		"app_name": "api",
		"version":  "1.0.0",
	})
	assert.Equal(t, "api-1.0.0", got)
}

func TestRenderTemplateVarsInStep(t *testing.T) {
	step := pipelinetemplate.Step{
		Name:  "Upload",
		Type:  "transfer",
		Roles: []string{"${vars.role}"},
		With:  map[string]interface{}{"target": "${vars.release_dir}/${vars.version}/"},
	}
	got, err := pipelinetemplate.RenderStepTemplateVars(step, map[string]interface{}{
		"role":        "compute",
		"release_dir": "/opt/api/releases",
		"version":     "1.0.0",
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"compute"}, got.Roles)
	assert.Equal(t, "/opt/api/releases/1.0.0/", got.With["target"])
}

func TestRenderTemplateVarsSupportsDefaults(t *testing.T) {
	step := pipelinetemplate.Step{
		Name: "Build",
		Type: "local_command",
		With: map[string]interface{}{
			"cmd": "CGO_ENABLED=${vars.cgo_enabled:-0} go build -o ${vars.output:-${vars.run_temp_dir}/app}",
		},
	}
	got, err := pipelinetemplate.RenderStepTemplateVars(step, map[string]interface{}{
		"run_temp_dir": "/tmp/run",
	})
	require.NoError(t, err)
	assert.Equal(t, "CGO_ENABLED=0 go build -o /tmp/run/app", got.With["cmd"])
}

func TestRenderTemplateVarsPreservesStructuredExactValue(t *testing.T) {
	files := []interface{}{
		map[string]interface{}{"from": "/repo/bin/api", "to": "bin/api"},
		map[string]interface{}{"from": "/repo/config.yaml", "to": "config/config.yaml"},
	}
	step := pipelinetemplate.Step{
		Name: "Package",
		Type: "archive_package",
		With: map[string]interface{}{
			"artifact": "/tmp/api.tar.gz",
			"files":    "${vars.files}",
		},
	}

	got, err := pipelinetemplate.RenderStepTemplateVars(step, map[string]interface{}{
		"files": files,
	})

	require.NoError(t, err)
	assert.Equal(t, files, got.With["files"])
}

func TestScanTemplateVars(t *testing.T) {
	vars := pipelinetemplate.ScanTemplateVars("${vars.app_name} ${vars.role} ${vars.app_name}")
	assert.Equal(t, []string{"app_name", "role"}, vars)
}
