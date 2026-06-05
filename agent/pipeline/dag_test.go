// Package pipeline_test 验证插件化流水线的阶段内 DAG 校验。
//
// 职责：
//   - 验证未知依赖会被拒绝
//   - 验证拓扑排序保证依赖先于使用方
//
// 边界：
//   - 不执行插件命令
//   - 不解析模板 include
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

func TestValidateDAGDetectsUnknownDependency(t *testing.T) {
	_, err := pipeline.ValidateDAG([]model.Step{{Name: "Deploy", Type: "remote_command", Needs: []string{"Build"}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown dependency")
}

func TestValidateDAGOrdersDependencies(t *testing.T) {
	order, err := pipeline.ValidateDAG([]model.Step{
		{Name: "Restart", Type: "remote_command", Needs: []string{"Upload"}},
		{Name: "Upload", Type: "transfer", Needs: []string{"Prepare"}},
		{Name: "Prepare", Type: "remote_command"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Prepare", "Upload", "Restart"}, order.Names())
}
