// Package pipeline_test 验证插件化阶段 DAG 执行引擎。
//
// 职责：
//   - 验证 build/deploy/finally 阶段顺序
//   - 验证 build 失败时跳过 deploy 但仍执行 finally
//
// 边界：
//   - 不执行真实 shell/SSH/HTTP 插件
//   - 不测试模板 include 展开
package pipeline_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

type fakePlugin struct {
	name   string
	failOn map[string]bool
	mu     sync.Mutex
	calls  []string
}

func (p *fakePlugin) Name() string { return p.name }
func (p *fakePlugin) Validate(step model.Step) error {
	return nil
}
func (p *fakePlugin) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	p.mu.Lock()
	p.calls = append(p.calls, step.Name)
	p.mu.Unlock()
	if p.failOn[step.Name] {
		return errors.New("boom")
	}
	return nil
}

func TestEngineRunsBuildDeployFinally(t *testing.T) {
	plugin := &fakePlugin{name: "local_command", failOn: map[string]bool{}}
	eng := pipeline.NewEngine()
	eng.Register(plugin)
	p := model.Pipeline{
		Build:   []model.Step{{Name: "Build", Type: "local_command"}},
		Deploy:  []model.Step{{Name: "Deploy", Type: "local_command"}},
		Finally: []model.Step{{Name: "Cleanup", Type: "local_command"}},
	}
	plan, run, err := pipeline.BuildPlan("dep-1", p, nil)
	require.NoError(t, err)
	final, err := eng.Run(context.Background(), plan, run, nil)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, final.Status)
	assert.Equal(t, []string{"Build", "Deploy", "Cleanup"}, plugin.calls)
}

func TestEngineSkipsDeployAfterBuildFailureButRunsFinally(t *testing.T) {
	plugin := &fakePlugin{name: "local_command", failOn: map[string]bool{"Build": true}}
	eng := pipeline.NewEngine()
	eng.Register(plugin)
	p := model.Pipeline{
		Build:   []model.Step{{Name: "Build", Type: "local_command"}},
		Deploy:  []model.Step{{Name: "Deploy", Type: "local_command"}},
		Finally: []model.Step{{Name: "Cleanup", Type: "local_command"}},
	}
	plan, run, err := pipeline.BuildPlan("dep-1", p, nil)
	require.NoError(t, err)
	final, err := eng.Run(context.Background(), plan, run, nil)
	require.Error(t, err)
	assert.Equal(t, model.RunStatusFailed, final.Status)
	assert.Equal(t, []string{"Build", "Cleanup"}, plugin.calls)
	assert.Equal(t, model.StatusSkipped, final.StepRuns[1].Status)
}
