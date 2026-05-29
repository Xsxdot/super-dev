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

// fakeExecutor 记录调用顺序，可按 (stepID, hostID) 注入失败。
type fakeExecutor struct {
	mu     sync.Mutex
	calls  []string // "stepID@hostID"
	failAt map[string]bool
}

func (f *fakeExecutor) record(step model.Step, t pipeline.Target) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, step.ID+"@"+t.HostID)
}

func (f *fakeExecutor) Run(ctx context.Context, t pipeline.Target, step model.Step, onLine func(string, string)) (int, error) {
	f.record(step, t)
	if f.failAt[step.ID+"@"+t.HostID] {
		return 1, errors.New("boom")
	}
	return 0, nil
}

func (f *fakeExecutor) Sync(ctx context.Context, t pipeline.Target, step model.Step, onLine func(string, string)) error {
	f.record(step, t)
	if f.failAt[step.ID+"@"+t.HostID] {
		return errors.New("sync boom")
	}
	return nil
}

func buildPipelineAndRun() (model.Pipeline, model.Run) {
	p := model.Pipeline{Steps: []model.Step{
		{ID: "build", Name: "构建", Scope: model.ScopeLocal, Action: model.ActionRun},
		{ID: "sync", Name: "同步", Scope: model.ScopeFanOut, Action: model.ActionSync},
		{ID: "restart", Name: "重启", Scope: model.ScopeFanOut, Action: model.ActionRun},
	}}
	run := p.Expand("dep-1", []model.HostRef{{ID: "h1", Name: "host-1"}, {ID: "h2", Name: "host-2"}})
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
	// build 在所有 fan-out 之前
	assert.Equal(t, "build@", fe.calls[0])
}

func TestEngineFailFastStopsLaterSteps(t *testing.T) {
	p, run := buildPipelineAndRun()
	fe := &fakeExecutor{failAt: map[string]bool{"sync@h1": true}}
	eng := pipeline.NewEngine(fe)

	final, err := eng.Run(context.Background(), p, run, nil)
	require.Error(t, err)
	assert.Equal(t, model.RunStatusFailed, final.Status)

	// build 成功，sync 失败，restart 完全不执行
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
