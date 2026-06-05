// Package pipeline_test 验证插件化流水线执行计划与 Run 骨架。
//
// 职责：
//   - 验证 roles 能展开为具体 host task
//   - 验证 Run skeleton 保留阶段、类型和依赖
//
// 边界：
//   - 不调度执行插件
//   - 不访问模板 Store 或 HTTP API
package pipeline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

func TestBuildRunSkeletonExpandsRolesToTasks(t *testing.T) {
	p := model.Pipeline{
		Roles: map[string][]string{"compute": {"h1", "h2"}},
		Deploy: []model.Step{{
			Name: "Upload", Type: "transfer", Roles: []string{"compute"},
		}},
	}
	hosts := []model.HostRef{{ID: "h1", Name: "box1"}, {ID: "h2", Name: "box2"}}
	plan, run, err := pipeline.BuildPlan("dep-1", p, hosts)
	require.NoError(t, err)
	require.Len(t, plan.Phases[model.PhaseDeploy], 1)
	require.Len(t, run.StepRuns, 1)
	assert.Equal(t, model.PhaseDeploy, run.StepRuns[0].Phase)
	require.Len(t, run.StepRuns[0].Tasks, 2)
	assert.Equal(t, "h1", run.StepRuns[0].Tasks[0].HostID)
	assert.Equal(t, "box1", run.StepRuns[0].Tasks[0].HostName)
}
