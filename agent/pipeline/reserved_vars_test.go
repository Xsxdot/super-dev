// Package pipeline_test 验证流水线保留变量生成与冲突检测。
//
// 职责：
//   - 验证预览/运行时保留变量派生规则
//   - 验证用户变量不能覆盖保留变量
//
// 边界：
//   - 不测试模板展开
//   - 不创建真实流水线运行
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/pipeline"
)

func TestPreviewReservedVars(t *testing.T) {
	vars := pipeline.PreviewReservedVars(pipeline.ReservedVarOptions{
		Workspace: "/repo/app",
		Version:   "1.2.3",
		Env:       "prod",
	})

	assert.Equal(t, "/repo/app", vars["workspace"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/output", vars["output"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview/artifacts", vars["artifacts"])
	assert.Equal(t, "1.2.3", vars["version"])
	assert.Equal(t, "prod", vars["env"])
	assert.Equal(t, "20260101", vars["date"])
	assert.Equal(t, "000000", vars["time"])
	assert.Equal(t, "/tmp/super-debug-pipeline-preview", vars["run_temp_dir"])
}

func TestRuntimeReservedVars(t *testing.T) {
	vars := pipeline.RuntimeReservedVars("/tmp/run-123", pipeline.ReservedVarOptions{
		Workspace: "/repo/app",
		Version:   "1.2.3",
		Env:       "prod",
		NowUnix:   1788201296000,
	})

	assert.Equal(t, "/tmp/run-123/output", vars["output"])
	assert.Equal(t, "/tmp/run-123/artifacts", vars["artifacts"])
	assert.Equal(t, "/tmp/run-123", vars["run_temp_dir"])
	assert.Len(t, vars["date"], 8)
	assert.Len(t, vars["time"], 6)
}

func TestRejectReservedVariableOverrides(t *testing.T) {
	err := pipeline.RejectReservedVariableOverrides(map[string]string{
		"workspace": "/bad",
		"app_name":  "api",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace")
}
