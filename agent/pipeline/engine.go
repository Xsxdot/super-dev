// engine.go 实现阶段化 DAG 插件调度引擎。
package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/superdev/agent/model"
)

// EventType 进度事件类型。
type EventType string

const (
	EventTaskStarted  EventType = "task_started"
	EventTaskLog      EventType = "task_log"
	EventTaskFinished EventType = "task_finished"
	EventRunFinished  EventType = "run_finished"
)

// Event 引擎执行过程中上报的增量进度事件。
type Event struct {
	Type     EventType
	StepName string
	HostID   string
	Line     string          // EventTaskLog 时有效
	Stream   string          // EventTaskLog 时有效："stdout"/"stderr"
	Status   model.RunStatus // EventTaskFinished/EventRunFinished 时有效
	ExitCode int
	At       int64
}

// Engine 按 Plan 和 Run 骨架调度插件执行。
type Engine struct {
	plugins map[string]StepPlugin
}

// NewEngine 创建插件化流水线引擎。
//
// 返回：
//   - 空插件注册表的 Engine，调用 Run 前需要 Register 所需插件
func NewEngine() *Engine {
	return &Engine{plugins: map[string]StepPlugin{}}
}

// Register 注册一个 StepPlugin。
//
// 参数：
//   - plugin: 插件实例，Name() 用作 Step.Type 映射键
//
// 注意：
//   - 同名插件会被后注册的实例覆盖，便于测试替换
func (e *Engine) Register(plugin StepPlugin) {
	if plugin == nil {
		return
	}
	e.plugins[plugin.Name()] = plugin
}

// Run 执行整条流水线。
//
// 参数：
//   - ctx: 上下文，用于取消插件执行和重试等待
//   - plan: 已按阶段拓扑排序的执行计划
//   - run: BuildPlan 生成的 Run 骨架
//   - emit: 可选事件回调
//
// 返回：
//   - 执行后的 Run 终态
//   - 任一非 finally 阶段或 finally 步骤失败时返回错误
//
// 注意：
//   - build 失败会跳过 deploy，但 finally 总会执行
//   - 本实现按拓扑顺序串行执行步骤；并发优化可在保持语义后再引入
func (e *Engine) Run(ctx context.Context, plan Plan, run model.Run, emit func(Event)) (model.Run, error) {
	run.Status = model.RunStatusRunning
	run.StartedAt = time.Now().UnixMilli()
	stepRuns := indexStepRuns(run.StepRuns)

	var runErr error
	buildFailed, err := e.runPhase(ctx, model.PhaseBuild, plan.Phases[model.PhaseBuild], stepRuns, emit)
	if err != nil {
		runErr = err
	}
	if buildFailed {
		skipPhase(model.PhaseDeploy, plan.Phases[model.PhaseDeploy], stepRuns)
	} else {
		deployFailed, err := e.runPhase(ctx, model.PhaseDeploy, plan.Phases[model.PhaseDeploy], stepRuns, emit)
		if err != nil && runErr == nil {
			runErr = err
		}
		if deployFailed {
			run.Status = model.RunStatusFailed
		}
	}
	_, finallyErr := e.runPhase(ctx, model.PhaseFinally, plan.Phases[model.PhaseFinally], stepRuns, emit)
	if finallyErr != nil && runErr == nil {
		runErr = finallyErr
	}

	run.StepRuns = stepRuns.slice
	if runErr != nil {
		run.Status = model.RunStatusFailed
	} else {
		run.Status = model.StatusSuccess
	}
	run.FinishedAt = time.Now().UnixMilli()
	if emit != nil {
		emit(Event{Type: EventRunFinished, Status: run.Status, At: run.FinishedAt})
	}
	return run, runErr
}

func (e *Engine) runPhase(ctx context.Context, phase model.PipelinePhase, steps []model.Step, runs stepRunIndex, emit func(Event)) (bool, error) {
	statuses := map[string]model.RunStatus{}
	var phaseErr error
	for _, step := range steps {
		sr := runs.get(phase, step.Name)
		if sr == nil {
			phaseErr = fmt.Errorf("%s phase step %q has no run skeleton", phase, step.Name)
			continue
		}
		if dependencyBlocked(step, statuses) {
			markStepSkipped(sr)
			statuses[step.Name] = model.StatusSkipped
			continue
		}
		if len(sr.Tasks) == 0 {
			sr.Status = model.StatusSuccess
			statuses[step.Name] = model.StatusSuccess
			continue
		}
		err := e.executeStep(ctx, step, sr, emit)
		if err != nil {
			statuses[step.Name] = model.RunStatusFailed
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		statuses[step.Name] = model.StatusSuccess
	}
	return phaseErr != nil, phaseErr
}

func (e *Engine) executeStep(ctx context.Context, step model.Step, sr *model.StepRun, emit func(Event)) error {
	plugin, ok := e.plugins[step.Type]
	if !ok {
		markStepFailed(sr)
		return fmt.Errorf("plugin %q not registered", step.Type)
	}
	if err := plugin.Validate(step); err != nil {
		markStepFailed(sr)
		return err
	}
	markTasksRunning(sr, emit)
	targets := targetsForStepRun(sr)
	runCtx := NewRunContext(ctx, RunContextOptions{
		LogLine: func(line, stream string) {
			if emit != nil {
				emit(Event{Type: EventTaskLog, StepName: step.Name, Line: line, Stream: stream, At: time.Now().UnixMilli()})
			}
		},
	})
	err := executeWithRetries(ctx, step, func() error {
		return plugin.Execute(runCtx, step, targets)
	})
	if err != nil {
		markStepFailed(sr)
		finishTasks(sr, model.RunStatusFailed, emit)
		return fmt.Errorf("step %s failed: %w", step.Name, err)
	}
	sr.Status = model.StatusSuccess
	finishTasks(sr, model.StatusSuccess, emit)
	return nil
}

func executeWithRetries(ctx context.Context, step model.Step, fn func() error) error {
	attempts := step.Retries + 1
	if attempts < 1 {
		attempts = 1
	}
	delay := retryDelay(step.RetryDelay)
	var err error
	for attempt := 0; attempt < attempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		err = fn()
		if err == nil {
			return nil
		}
		if attempt == attempts-1 || delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func retryDelay(value string) time.Duration {
	if value == "" {
		return 0
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return d
}

func dependencyBlocked(step model.Step, statuses map[string]model.RunStatus) bool {
	for _, dep := range step.Needs {
		if statuses[dep] != model.StatusSuccess {
			return true
		}
	}
	return false
}

func skipPhase(phase model.PipelinePhase, steps []model.Step, runs stepRunIndex) {
	for _, step := range steps {
		if sr := runs.get(phase, step.Name); sr != nil {
			markStepSkipped(sr)
		}
	}
}

func targetsForStepRun(sr *model.StepRun) []Target {
	targets := make([]Target, 0, len(sr.Tasks))
	for _, task := range sr.Tasks {
		if task.HostID == "" && task.HostName == "" {
			continue
		}
		targets = append(targets, Target{HostID: task.HostID, HostName: task.HostName})
	}
	return targets
}

func markTasksRunning(sr *model.StepRun, emit func(Event)) {
	sr.Status = model.RunStatusRunning
	now := time.Now().UnixMilli()
	for i := range sr.Tasks {
		sr.Tasks[i].Status = model.RunStatusRunning
		sr.Tasks[i].StartedAt = now
		if emit != nil {
			emit(Event{Type: EventTaskStarted, StepName: sr.StepName, HostID: sr.Tasks[i].HostID, At: now})
		}
	}
}

func finishTasks(sr *model.StepRun, status model.RunStatus, emit func(Event)) {
	now := time.Now().UnixMilli()
	for i := range sr.Tasks {
		sr.Tasks[i].Status = status
		sr.Tasks[i].FinishedAt = now
		if emit != nil {
			emit(Event{Type: EventTaskFinished, StepName: sr.StepName, HostID: sr.Tasks[i].HostID, Status: status, ExitCode: sr.Tasks[i].ExitCode, At: now})
		}
	}
}

func markStepFailed(sr *model.StepRun) {
	sr.Status = model.RunStatusFailed
}

func markStepSkipped(sr *model.StepRun) {
	sr.Status = model.StatusSkipped
	for i := range sr.Tasks {
		sr.Tasks[i].Status = model.StatusSkipped
	}
}

type stepRunIndex struct {
	slice []model.StepRun
	items map[string]int
}

func indexStepRuns(runs []model.StepRun) stepRunIndex {
	idx := stepRunIndex{slice: append([]model.StepRun(nil), runs...), items: map[string]int{}}
	for i, sr := range idx.slice {
		idx.items[phaseStepKey(sr.Phase, sr.StepName)] = i
	}
	return idx
}

func (i stepRunIndex) get(phase model.PipelinePhase, name string) *model.StepRun {
	idx, ok := i.items[phaseStepKey(phase, name)]
	if !ok {
		return nil
	}
	return &i.slice[idx]
}

func phaseStepKey(phase model.PipelinePhase, name string) string {
	return string(phase) + "\x00" + name
}
