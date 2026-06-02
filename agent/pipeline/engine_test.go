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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

type contextCapturePlugin struct {
	tempDir string
	vars    map[string]string
}

func (p *contextCapturePlugin) Name() string              { return "capture_context" }
func (p *contextCapturePlugin) Validate(model.Step) error { return nil }
func (p *contextCapturePlugin) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	p.tempDir = ctx.RunTempDir
	p.vars = ctx.Vars
	return nil
}

type recordingPlugin struct {
	mu       sync.Mutex
	started  []string
	finished []string
	failHost string
	delay    time.Duration
}

func (p *recordingPlugin) Name() string                   { return "recording" }
func (p *recordingPlugin) Validate(step model.Step) error { return nil }
func (p *recordingPlugin) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	target := "local"
	if len(targets) > 0 {
		target = targets[0].HostID
	}
	p.mu.Lock()
	p.started = append(p.started, target)
	p.mu.Unlock()
	ctx.LogLine("hello "+target, "stdout")
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	if target == p.failHost {
		return fmt.Errorf("fail %s", target)
	}
	p.mu.Lock()
	p.finished = append(p.finished, target)
	p.mu.Unlock()
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

func TestEngineSkipsStepWhenRunIfFalse(t *testing.T) {
	plugin := &fakePlugin{name: "local_command", failOn: map[string]bool{}}
	eng := pipeline.NewEngine()
	eng.Register(plugin)
	p := model.Pipeline{
		Build: []model.Step{{Name: "Optional", Type: "local_command", RunIf: "false"}},
	}
	plan, run, err := pipeline.BuildPlan("dep-1", p, nil)
	require.NoError(t, err)

	final, err := eng.Run(context.Background(), plan, run, nil)

	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, final.Status)
	assert.Empty(t, plugin.calls)
	require.Len(t, final.StepRuns, 1)
	assert.Equal(t, model.StatusSkipped, final.StepRuns[0].Status)
}

func TestEngineFailsStepWhenRunIfInvalid(t *testing.T) {
	plugin := &fakePlugin{name: "local_command", failOn: map[string]bool{}}
	eng := pipeline.NewEngine()
	eng.Register(plugin)
	p := model.Pipeline{
		Build: []model.Step{{Name: "Invalid", Type: "local_command", RunIf: "prod"}},
	}
	plan, run, err := pipeline.BuildPlan("dep-1", p, nil)
	require.NoError(t, err)

	final, err := eng.Run(context.Background(), plan, run, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_if")
	assert.Equal(t, model.RunStatusFailed, final.Status)
	assert.Empty(t, plugin.calls)
	require.Len(t, final.StepRuns, 1)
	assert.Equal(t, model.RunStatusFailed, final.StepRuns[0].Status)
}

func TestEngineCreatesRunTempDirAndVars(t *testing.T) {
	plugin := &contextCapturePlugin{}
	eng := pipeline.NewEngine()
	eng.Register(plugin)
	plan, run, err := pipeline.BuildPlan("dep-1", model.Pipeline{
		Variables: map[string]string{"env": "prod", "version": "1.2.3"},
		Build:     []model.Step{{Name: "Capture", Type: "capture_context"}},
	}, nil)
	require.NoError(t, err)

	_, err = eng.Run(context.Background(), plan, run, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, plugin.tempDir)
	assert.Equal(t, plugin.tempDir, plugin.vars["run_temp_dir"])
	assert.Equal(t, filepath.Join(plugin.tempDir, "output"), plugin.vars["output"])
	assert.Equal(t, filepath.Join(plugin.tempDir, "artifacts"), plugin.vars["artifacts"])
	assert.Equal(t, "prod", plugin.vars["env"])
	assert.Equal(t, "1.2.3", plugin.vars["version"])
	_, statErr := os.Stat(plugin.tempDir)
	assert.True(t, os.IsNotExist(statErr), "run temp dir removed after run")
}

func TestEngineSerialStopsAfterFirstHostFailure(t *testing.T) {
	plugin := &recordingPlugin{failHost: "h1"}
	engine := pipeline.NewEngine()
	engine.Register(plugin)
	plan, run := multiHostPlan("serial")
	_, err := engine.Run(context.Background(), plan, run, nil)
	require.Error(t, err)
	assert.Equal(t, []string{"h1"}, plugin.started)
}

func TestEngineParallelRunsAllHostsBeforeFailing(t *testing.T) {
	plugin := &recordingPlugin{failHost: "h2"}
	engine := pipeline.NewEngine()
	engine.Register(plugin)
	plan, run := multiHostPlan("parallel")
	_, err := engine.Run(context.Background(), plan, run, nil)
	require.Error(t, err)
	assert.ElementsMatch(t, []string{"h1", "h2", "h3"}, plugin.started)
}

func TestEngineBatchRunsChunksAndStopsNextBatchOnFailure(t *testing.T) {
	plugin := &recordingPlugin{failHost: "h2"}
	engine := pipeline.NewEngine()
	engine.Register(plugin)
	plan, run := multiHostPlan("batch:2")
	_, err := engine.Run(context.Background(), plan, run, nil)
	require.Error(t, err)
	assert.ElementsMatch(t, []string{"h1", "h2"}, plugin.started)
}

func multiHostPlan(concurrency string) (pipeline.Plan, model.Run) {
	step := model.Step{Name: "Deploy", Type: "recording", Concurrency: concurrency, Roles: []string{"targets"}}
	plan := pipeline.Plan{
		Phases: map[model.PipelinePhase][]model.Step{
			model.PhaseBuild:   {},
			model.PhaseDeploy:  {step},
			model.PhaseFinally: {},
		},
		Variables: map[string]string{"version": "v1"},
	}
	run := model.Run{
		ID: "run-1", DeploymentID: "dep-1", Status: model.StatusPending,
		StepRuns: []model.StepRun{{
			StepName: "Deploy", Type: "recording", Phase: model.PhaseDeploy, Status: model.StatusPending,
			Tasks: []model.Task{
				{HostID: "h1", HostName: "host-1", Status: model.StatusPending},
				{HostID: "h2", HostName: "host-2", Status: model.StatusPending},
				{HostID: "h3", HostName: "host-3", Status: model.StatusPending},
			},
		}},
	}
	return plan, run
}
