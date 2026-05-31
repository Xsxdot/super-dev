// engine.go 实现流水线执行引擎：按 Run 骨架串行 step、并行 task、fail-fast。
package pipeline

import (
	"context"
	"fmt"
	"sync"
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

// Engine 按 Run 骨架驱动流水线执行。
type Engine struct {
	exec Executor
}

// NewEngine 创建引擎，注入具体 Executor。
func NewEngine(exec Executor) *Engine {
	return &Engine{exec: exec}
}

// Run 执行整条流水线。
//
// 语义：
//   - StepRun 按 Run skeleton 顺序执行；前一步任一 task 失败则中断
//   - 同一 StepRun 内的 task 并行
//   - emit 为可选回调（nil 则不上报），引擎按 task 粒度回调进度事件
//
// 注意：
//   - emit 回调可能被并发 goroutine 调用，调用方需自行保证线程安全
//
// 返回：执行后的 Run 终态 + 整体错误（任一 task 失败时非 nil）。
func (e *Engine) Run(ctx context.Context, p model.Pipeline, run model.Run, emit func(Event)) (model.Run, error) {
	if e.exec == nil {
		return run, fmt.Errorf("pipeline executor is required")
	}
	run.Status = model.RunStatusRunning
	run.StartedAt = time.Now().UnixMilli()

	// stepByName 便于按 StepRun.StepName 找回声明 Step（取 Type/With 等）。
	stepByName := map[string]model.Step{}
	for _, phase := range pipelinePhases() {
		for _, s := range stepsForPhase(p, phase) {
			stepByName[s.Name] = s
		}
	}

	var runErr error
	for si := range run.StepRuns {
		sr := &run.StepRuns[si]
		step, ok := stepByName[sr.StepName]
		if !ok {
			sr.Status = model.RunStatusFailed
			run.Status = model.RunStatusFailed
			runErr = fmt.Errorf("step %s not found", sr.StepName)
			break
		}
		sr.Status = model.RunStatusRunning

		var wg sync.WaitGroup
		var mu sync.Mutex
		stepFailed := false

		for ti := range sr.Tasks {
			wg.Add(1)
			go func(task *model.Task) {
				defer wg.Done()
				target := Target{HostID: task.HostID, HostName: task.HostName}
				e.runTask(ctx, sr.StepName, step, target, task, emit)
				if task.Status == model.RunStatusFailed {
					mu.Lock()
					stepFailed = true
					mu.Unlock()
				}
			}(&sr.Tasks[ti])
		}
		wg.Wait()

		if stepFailed {
			sr.Status = model.RunStatusFailed
			run.Status = model.RunStatusFailed
			runErr = fmt.Errorf("step %s failed", sr.StepName)
			break // fail-fast：后续 StepRun 保持 pending
		}
		sr.Status = model.StatusSuccess
	}

	if runErr == nil {
		run.Status = model.StatusSuccess
	}
	run.FinishedAt = time.Now().UnixMilli()
	if emit != nil {
		emit(Event{Type: EventRunFinished, Status: run.Status, At: run.FinishedAt})
	}
	return run, runErr
}

// runTask 执行单个 task（一个 step 在一个 target 上），更新 task 状态并上报事件。
func (e *Engine) runTask(ctx context.Context, stepName string, step model.Step, target Target, task *model.Task, emit func(Event)) {
	task.Status = model.RunStatusRunning
	task.StartedAt = time.Now().UnixMilli()
	if emit != nil {
		emit(Event{Type: EventTaskStarted, StepName: stepName, HostID: target.HostID, At: task.StartedAt})
	}

	onLine := func(line, stream string) {
		if emit != nil {
			emit(Event{Type: EventTaskLog, StepName: stepName, HostID: target.HostID, Line: line, Stream: stream})
		}
	}

	var err error
	switch step.Type {
	case "local_command", "remote_command":
		task.ExitCode, err = e.exec.Run(ctx, target, step, onLine)
	case "transfer":
		err = e.exec.Sync(ctx, target, step, onLine)
	default:
		err = fmt.Errorf("unknown step type %q", step.Type)
	}

	task.FinishedAt = time.Now().UnixMilli()
	if err != nil || task.ExitCode != 0 {
		task.Status = model.RunStatusFailed
	} else {
		task.Status = model.StatusSuccess
	}
	if emit != nil {
		emit(Event{Type: EventTaskFinished, StepName: stepName, HostID: target.HostID,
			Status: task.Status, ExitCode: task.ExitCode, At: task.FinishedAt})
	}
}
