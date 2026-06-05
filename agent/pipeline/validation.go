// Package pipeline 中的 validation.go 实现流水线执行前静态校验。
//
// 职责：
//   - 校验已展开 Plan 中的插件注册、插件参数、run_if、concurrency 和 target 形态
//
// 边界：
//   - 不执行插件
//   - 不访问远程主机
//   - 不读写文件或持久化 Run
package pipeline

import (
	"fmt"

	"github.com/xsxdot/super-dev/agent/model"
)

// ValidatePlan 对已构建的 Plan/Run 骨架执行静态校验。
//
// 参数：
//   - plan: BuildPlan 返回的执行计划
//   - run: BuildPlan 返回的 Run 骨架
//
// 返回：
//   - 插件未注册、参数无效、条件表达式无效、并发配置无效或 target 形态无效时返回错误
//
// 注意：
//   - 该方法只调用插件 Validate/ValidateTargets，不调用 Execute
func (e *Engine) ValidatePlan(plan Plan, run model.Run) error {
	stepRuns := indexStepRuns(run.StepRuns)
	for _, phase := range pipelinePhases() {
		for _, step := range plan.Phases[phase] {
			if err := e.validateStep(phase, step, stepRuns); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) validateStep(phase model.PipelinePhase, step model.Step, stepRuns stepRunIndex) error {
	prefix := fmt.Sprintf("%s phase step %q", phase, step.Name)
	sr := stepRuns.get(phase, step.Name)
	if sr == nil {
		return fmt.Errorf("%s: run skeleton is missing", prefix)
	}
	plugin, ok := e.plugins[step.Type]
	if !ok {
		return fmt.Errorf("%s: plugin %q not registered", prefix, step.Type)
	}
	if err := plugin.Validate(step); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if _, err := EvaluateRunIf(step.RunIf); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if _, err := ParseStepConcurrency(step.Concurrency); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if validator, ok := plugin.(TargetValidator); ok {
		if err := validator.ValidateTargets(step, targetsFromStepRun(sr)); err != nil {
			return fmt.Errorf("%s: %w", prefix, err)
		}
	}
	return nil
}

func targetsFromStepRun(sr *model.StepRun) []Target {
	targets := make([]Target, 0, len(sr.Tasks))
	for _, task := range sr.Tasks {
		if task.HostID == "" {
			continue
		}
		targets = append(targets, Target{HostID: task.HostID, HostName: task.HostName})
	}
	return targets
}
