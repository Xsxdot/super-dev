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

// fakeExecutor 记录调用顺序，可按 (stepName, hostID) 注入失败。
type fakeExecutor struct {
	mu     sync.Mutex
	calls  []string // "stepName@hostID"
	failAt map[string]bool
}

func (f *fakeExecutor) record(step model.Step, t pipeline.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, step.Name+"@"+t.HostID)
}

func (f *fakeExecutor) Run(ctx context.Context, t pipeline.Target, step model.Step, onLine func(string, string)) (int, error) {
	f.record(step, t)
	if f.failAt[step.Name+"@"+t.HostID] {
		return 1, errors.New("boom")
	}
	return 0, nil
}

func (f *fakeExecutor) Sync(ctx context.Context, t pipeline.Target, step model.Step, onLine func(string, string)) error {
	f.record(step, t)
	if f.failAt[step.Name+"@"+t.HostID] {
		return errors.New("sync boom")
	}
	return nil
}

func buildPipelineAndRun() (model.Pipeline, model.Run) {
	p := model.Pipeline{
		Roles: map[string][]string{"compute": {"h1", "h2"}},
		Build: []model.Step{
			{Name: "build", Type: "local_command"},
		},
		Deploy: []model.Step{
			{Name: "sync", Type: "transfer", Roles: []string{"compute"}},
			{Name: "restart", Type: "remote_command", Roles: []string{"compute"}, Needs: []string{"sync"}},
		},
	}
	_, run, err := pipeline.BuildPlan("dep-1", p, []model.HostRef{{ID: "h1", Name: "host-1"}, {ID: "h2", Name: "host-2"}})
	if err != nil {
		panic(err)
	}
	return p, run
}

func TestEngineHappyPath(t *testing.T) {
	p, run := buildPipelineAndRun()
	fe := &fakeExecutor{failAt: map[string]bool{}}
	eng := pipeline.NewEngine(fe)

	final, err := eng.Run(context.Background(), p, run, nil)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, final.Status)
	for _, sr := range final.StepRuns {
		assert.Equal(t, model.StatusSuccess, sr.Status)
		for _, tk := range sr.Tasks {
			assert.Equal(t, model.StatusSuccess, tk.Status)
		}
	}
	// build 在所有 remote tasks 之前。
	assert.Equal(t, "build@", fe.calls[0])
}

func TestEngineFailFastStopsLaterSteps(t *testing.T) {
	p, run := buildPipelineAndRun()
	fe := &fakeExecutor{failAt: map[string]bool{"sync@h1": true}}
	eng := pipeline.NewEngine(fe)

	final, err := eng.Run(context.Background(), p, run, nil)
	require.Error(t, err)
	assert.Equal(t, model.RunStatusFailed, final.Status)

	// build 成功，sync 失败，restart 完全不执行。
	assert.Equal(t, model.StatusSuccess, final.StepRuns[0].Status)
	assert.Equal(t, model.RunStatusFailed, final.StepRuns[1].Status)
	assert.Equal(t, model.StatusPending, final.StepRuns[2].Status)
	for _, c := range fe.calls {
		assert.NotContains(t, c, "restart@")
	}
}

func TestEngineEmitsStatusCallbacks(t *testing.T) {
	p, run := buildPipelineAndRun()
	fe := &fakeExecutor{failAt: map[string]bool{}}
	eng := pipeline.NewEngine(fe)

	var mu sync.Mutex
	var events []string
	cb := func(ev pipeline.Event) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, string(ev.Type))
	}
	_, err := eng.Run(context.Background(), p, run, cb)
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, events, "task_started")
	assert.Contains(t, events, "task_finished")
	assert.Contains(t, events, "run_finished")
}

func TestEngineEmptyFanOutStepSucceeds(t *testing.T) {
	// role 存在但没有 host（0 个 task）：视为该步骤直接成功。
	p := model.Pipeline{
		Roles:  map[string][]string{"compute": nil},
		Deploy: []model.Step{{Name: "sync", Type: "transfer", Roles: []string{"compute"}}},
	}
	_, run, err := pipeline.BuildPlan("dep-1", p, nil)
	require.NoError(t, err)
	fe := &fakeExecutor{failAt: map[string]bool{}}
	eng := pipeline.NewEngine(fe)

	final, err := eng.Run(context.Background(), p, run, nil)
	require.NoError(t, err)
	assert.Equal(t, model.StatusSuccess, final.Status)
	assert.Equal(t, model.StatusSuccess, final.StepRuns[0].Status)
	assert.Empty(t, fe.calls)
}
