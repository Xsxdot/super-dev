// engine.go 实现阶段化 DAG 插件调度引擎。
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
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
	HostName string
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

// RunOptions 定义引擎阶段间的外部接线点。
type RunOptions struct {
	SkipBuild    bool
	AfterBuild   func(run model.Run, vars map[string]string) (model.Run, error)
	BeforeDeploy func(run model.Run, vars map[string]string) (model.Run, error)
	OnRunUpdate  func(run model.Run)
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
	return e.RunWithOptions(ctx, plan, run, emit, RunOptions{})
}

// RunWithOptions 执行整条流水线，并在阶段边界调用外部 hook。
func (e *Engine) RunWithOptions(ctx context.Context, plan Plan, run model.Run, emit func(Event), opts RunOptions) (model.Run, error) {
	run.Status = model.RunStatusRunning
	run.StartedAt = time.Now().UnixMilli()
	stepRuns := indexStepRuns(run.StepRuns)
	runTempDir, cleanup, err := createRunTempDir(run.DeploymentID)
	if err != nil {
		run.Status = model.RunStatusFailed
		run.FinishedAt = time.Now().UnixMilli()
		return run, err
	}
	defer cleanup()
	runVars := MergeVariables(plan.Variables, RuntimeReservedVars(runTempDir, ReservedVarOptions{
		Workspace: plan.Variables["workspace"],
		Version:   plan.Variables["version"],
		Env:       plan.Variables["env"],
	}))
	runVars = pipelinetemplate.RenderPipelineVariableMap(runVars)
	executionPlan := renderRuntimePlan(plan, runVars)
	if err := ensureRunReservedDirs(runVars); err != nil {
		run.Status = model.RunStatusFailed
		run.FinishedAt = time.Now().UnixMilli()
		return run, err
	}
	notify := func() {
		if opts.OnRunUpdate != nil {
			run.StepRuns = stepRuns.slice
			opts.OnRunUpdate(run)
		}
	}

	var runErr error
	buildFailed := false
	if opts.SkipBuild {
		skipPhase(model.PhaseBuild, executionPlan.Phases[model.PhaseBuild], stepRuns)
		notify()
	} else {
		var err error
		buildFailed, err = e.runPhase(ctx, model.PhaseBuild, executionPlan.Phases[model.PhaseBuild], stepRuns, emit, runTempDir, runVars, notify)
		if err != nil {
			runErr = err
		}
		if !buildFailed && opts.AfterBuild != nil {
			run.StepRuns = stepRuns.slice
			run, err = opts.AfterBuild(run, runVars)
			if err != nil {
				buildFailed = true
				runErr = err
			}
			notify()
		}
	}
	if buildFailed {
		skipPhase(model.PhaseDeploy, executionPlan.Phases[model.PhaseDeploy], stepRuns)
		notify()
	} else {
		if opts.BeforeDeploy != nil {
			run.StepRuns = stepRuns.slice
			var err error
			run, err = opts.BeforeDeploy(run, runVars)
			if err != nil {
				runErr = err
				skipPhase(model.PhaseDeploy, executionPlan.Phases[model.PhaseDeploy], stepRuns)
				notify()
			}
		}
		if runErr == nil {
			deployFailed, err := e.runPhase(ctx, model.PhaseDeploy, executionPlan.Phases[model.PhaseDeploy], stepRuns, emit, runTempDir, runVars, notify)
			if err != nil && runErr == nil {
				runErr = err
			}
			if deployFailed {
				run.Status = model.RunStatusFailed
			}
		}
	}
	_, finallyErr := e.runPhase(ctx, model.PhaseFinally, executionPlan.Phases[model.PhaseFinally], stepRuns, emit, runTempDir, runVars, notify)
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
	if opts.OnRunUpdate != nil {
		opts.OnRunUpdate(run)
	}
	return run, runErr
}

func (e *Engine) runPhase(ctx context.Context, phase model.PipelinePhase, steps []model.Step, runs stepRunIndex, emit func(Event), runTempDir string, runVars map[string]string, onStepComplete func()) (bool, error) {
	statuses := map[string]model.RunStatus{}
	var phaseErr error
	for _, step := range steps {
		sr := runs.get(phase, step.Name)
		if sr == nil {
			phaseErr = fmt.Errorf("%s phase step %q has no run skeleton", phase, step.Name)
			continue
		}
		completeStep := func() {
			if onStepComplete != nil {
				onStepComplete()
			}
		}
		if dependencyBlocked(step, statuses) {
			markStepSkipped(sr)
			statuses[step.Name] = model.StatusSkipped
			completeStep()
			continue
		}
		shouldRun, err := EvaluateRunIf(step.RunIf)
		if err != nil {
			markStepFailed(sr)
			statuses[step.Name] = model.RunStatusFailed
			if phaseErr == nil {
				phaseErr = err
			}
			completeStep()
			continue
		}
		if !shouldRun {
			markStepSkipped(sr)
			statuses[step.Name] = model.StatusSkipped
			completeStep()
			continue
		}
		if len(sr.Tasks) == 0 {
			sr.Status = model.StatusSuccess
			statuses[step.Name] = model.StatusSuccess
			completeStep()
			continue
		}
		err = e.executeStep(ctx, step, sr, emit, runTempDir, runVars)
		if err != nil {
			statuses[step.Name] = model.RunStatusFailed
			if phaseErr == nil {
				phaseErr = err
			}
			completeStep()
			continue
		}
		statuses[step.Name] = model.StatusSuccess
		completeStep()
	}
	return phaseErr != nil, phaseErr
}

func renderRuntimePlan(plan Plan, vars map[string]string) Plan {
	out := Plan{
		Phases:    map[model.PipelinePhase][]model.Step{},
		Variables: vars,
	}
	for phase, steps := range plan.Phases {
		out.Phases[phase] = renderSteps(steps, vars)
	}
	return out
}

func (e *Engine) executeStep(ctx context.Context, step model.Step, sr *model.StepRun, emit func(Event), runTempDir string, runVars map[string]string) error {
	plugin, ok := e.plugins[step.Type]
	if !ok {
		markStepFailed(sr)
		return fmt.Errorf("plugin %q not registered", step.Type)
	}
	if err := plugin.Validate(step); err != nil {
		markStepFailed(sr)
		return err
	}
	concurrency, err := ParseStepConcurrency(step.Concurrency)
	if err != nil {
		markStepFailed(sr)
		return err
	}
	sr.Status = model.RunStatusRunning
	err = e.executeStepTasks(ctx, plugin, step, sr, emit, runTempDir, runVars, concurrency)
	if err != nil {
		markStepFailed(sr)
		return fmt.Errorf("step %s failed: %w", step.Name, err)
	}
	sr.Status = model.StatusSuccess
	return nil
}

func (e *Engine) executeStepTasks(ctx context.Context, plugin StepPlugin, step model.Step, sr *model.StepRun, emit func(Event), runTempDir string, runVars map[string]string, concurrency StepConcurrency) error {
	if len(sr.Tasks) == 1 {
		target := taskTarget(sr.Tasks[0])
		if target.IsLocal() {
			return e.executeOneTask(ctx, plugin, step, sr, 0, nil, target, emit, runTempDir, runVars)
		}
	}
	switch concurrency.Mode {
	case ConcurrencySerial:
		for i := range sr.Tasks {
			target := taskTarget(sr.Tasks[i])
			if err := e.executeOneTask(ctx, plugin, step, sr, i, []Target{target}, target, emit, runTempDir, runVars); err != nil {
				return err
			}
		}
		return nil
	case ConcurrencyParallel:
		return e.executeTaskBatch(ctx, plugin, step, sr, taskIndexes(sr.Tasks), emit, runTempDir, runVars)
	case ConcurrencyBatch:
		for start := 0; start < len(sr.Tasks); start += concurrency.Limit {
			end := start + concurrency.Limit
			if end > len(sr.Tasks) {
				end = len(sr.Tasks)
			}
			if err := e.executeTaskBatch(ctx, plugin, step, sr, rangeIndexes(start, end), emit, runTempDir, runVars); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("invalid concurrency mode %q", concurrency.Mode)
	}
}

func (e *Engine) executeTaskBatch(ctx context.Context, plugin StepPlugin, step model.Step, sr *model.StepRun, indexes []int, emit func(Event), runTempDir string, runVars map[string]string) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, idx := range indexes {
		idx := idx
		target := taskTarget(sr.Tasks[idx])
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.executeOneTask(ctx, plugin, step, sr, idx, []Target{target}, target, emit, runTempDir, runVars); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func (e *Engine) executeOneTask(ctx context.Context, plugin StepPlugin, step model.Step, sr *model.StepRun, taskIndex int, targets []Target, target Target, emit func(Event), runTempDir string, runVars map[string]string) error {
	startTask(sr, taskIndex, emit)
	runCtx := NewRunContext(ctx, RunContextOptions{
		RunTempDir: runTempDir,
		Vars:       runVars,
		Target:     target,
		LogLine: func(line, stream string) {
			if emit != nil {
				emit(Event{
					Type:     EventTaskLog,
					StepName: step.Name,
					HostID:   target.HostID,
					HostName: target.HostName,
					Line:     line,
					Stream:   stream,
					At:       time.Now().UnixMilli(),
				})
			}
		},
	})
	err := executeWithRetries(ctx, step, func() error {
		return plugin.Execute(runCtx, step, targets)
	})
	if err != nil {
		sr.Tasks[taskIndex].ExitCode = exitCodeFromError(err)
		finishTask(sr, taskIndex, model.RunStatusFailed, emit)
		return err
	}
	finishTask(sr, taskIndex, model.StatusSuccess, emit)
	return nil
}

var unsafeTempPrefix = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func createRunTempDir(deploymentID string) (string, func(), error) {
	prefix := "super-debug-pipeline-"
	if deploymentID != "" {
		prefix += sanitizeTempPrefix(deploymentID) + "-"
	}
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

func sanitizeTempPrefix(value string) string {
	value = strings.Trim(unsafeTempPrefix.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "run"
	}
	if len(value) > 48 {
		return value[:48]
	}
	return value
}

func ensureRunReservedDirs(vars map[string]string) error {
	for _, name := range []string{"output", "artifacts"} {
		dir := vars[name]
		if dir == "" {
			continue
		}
		// output/artifacts 是模板的公共写入根，运行前创建可以避免每个构建模板重复 mkdir。
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
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

func taskTarget(task model.Task) Target {
	return Target{HostID: task.HostID, HostName: task.HostName, HostAddress: task.HostAddress}
}

func taskIndexes(tasks []model.Task) []int {
	return rangeIndexes(0, len(tasks))
}

func rangeIndexes(start, end int) []int {
	out := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, i)
	}
	return out
}

func startTask(sr *model.StepRun, index int, emit func(Event)) {
	now := time.Now().UnixMilli()
	sr.Tasks[index].Status = model.RunStatusRunning
	sr.Tasks[index].StartedAt = now
	if emit != nil {
		emit(Event{
			Type:     EventTaskStarted,
			StepName: sr.StepName,
			HostID:   sr.Tasks[index].HostID,
			HostName: sr.Tasks[index].HostName,
			At:       now,
		})
	}
}

func finishTask(sr *model.StepRun, index int, status model.RunStatus, emit func(Event)) {
	now := time.Now().UnixMilli()
	sr.Tasks[index].Status = status
	sr.Tasks[index].FinishedAt = now
	if emit != nil {
		emit(Event{
			Type:     EventTaskFinished,
			StepName: sr.StepName,
			HostID:   sr.Tasks[index].HostID,
			HostName: sr.Tasks[index].HostName,
			Status:   status,
			ExitCode: sr.Tasks[index].ExitCode,
			At:       now,
		})
	}
}

type exitCodeCarrier interface {
	ExitCode() int
}

func exitCodeFromError(err error) int {
	var carrier exitCodeCarrier
	if errors.As(err, &carrier) {
		return carrier.ExitCode()
	}
	return 0
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
