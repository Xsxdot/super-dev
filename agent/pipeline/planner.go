// Package pipeline 中的 planner.go 负责将声明式 Pipeline 转成可执行计划。
//
// 职责：
//   - 按 build/deploy/finally 阶段校验 DAG
//   - 将 roles 解析为具体 host target
//   - 构造初始 Run / StepRun / Task 骨架
//
// 边界：
//   - 不展开 include 模板
//   - 不执行插件命令
//   - 不持久化 Run 状态
package pipeline

import (
	"fmt"

	"github.com/xsxdot/super-dev/agent/model"
)

// Plan is the executable pipeline grouped by phase.
type Plan struct {
	Phases    map[model.PipelinePhase][]model.Step
	Variables map[string]string
}

// Target is where a plugin task executes. Empty HostID means local/global.
type Target struct {
	HostID   string
	HostName string
}

// IsLocal 报告该目标是否为本机。
//
// 返回：
//   - HostID 为空时返回 true
func (t Target) IsLocal() bool { return t.HostID == "" }

// BuildPlan validates a pipeline and creates the initial Run skeleton.
//
// 参数：
//   - deploymentID: 当前 deployment ID
//   - p: 已展开 include 的 pipeline
//   - hosts: 可被 roles 引用的主机列表
//
// 返回：
//   - 按阶段分组且已拓扑排序的 Plan
//   - 所有状态均为 pending 的 Run 骨架
//   - DAG 或 role/host 解析错误
//
// 注意：
//   - Roles 为空的 step 会生成一个本地/global task
func BuildPlan(deploymentID string, p model.Pipeline, hosts []model.HostRef) (Plan, model.Run, error) {
	phases := map[model.PipelinePhase][]model.Step{}
	for _, phase := range pipelinePhases() {
		steps := stepsForPhase(p, phase)
		order, err := ValidateDAG(steps)
		if err != nil {
			return Plan{}, model.Run{}, fmt.Errorf("%s phase: %w", phase, err)
		}
		phases[phase] = order.Steps
	}
	plan := Plan{Phases: phases, Variables: copyStringMap(p.Variables)}
	run := model.Run{DeploymentID: deploymentID, Status: model.StatusPending}
	for _, phase := range pipelinePhases() {
		for _, step := range phases[phase] {
			targets, err := ResolveStepTargets(step, p.Roles, hosts)
			if err != nil {
				return Plan{}, model.Run{}, err
			}
			sr := model.StepRun{
				StepName: step.Name,
				Type:     step.Type,
				Phase:    phase,
				Needs:    append([]string(nil), step.Needs...),
				Status:   model.StatusPending,
			}
			if len(step.Roles) == 0 {
				sr.Tasks = []model.Task{{Status: model.StatusPending}}
			} else {
				for _, t := range targets {
					sr.Tasks = append(sr.Tasks, model.Task{HostID: t.HostID, HostName: t.HostName, Status: model.StatusPending})
				}
			}
			run.StepRuns = append(run.StepRuns, sr)
		}
	}
	return plan, run, nil
}

func copyStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

// ResolveStepTargets resolves step roles to concrete host targets.
//
// 参数：
//   - step: 待解析目标的步骤
//   - roles: pipeline roles，role 名到 host ID 列表
//   - hosts: 可选目标主机最小信息
//
// 返回：
//   - 去重后的目标列表；step.Roles 为空时返回 nil
//   - role 或 host 不存在时返回错误
//
// 注意：
//   - 同一 host 被多个 role 命中时只执行一次
func ResolveStepTargets(step model.Step, roles map[string][]string, hosts []model.HostRef) ([]Target, error) {
	if len(step.Roles) == 0 {
		return nil, nil
	}
	hostByID := map[string]model.HostRef{}
	for _, h := range hosts {
		hostByID[h.ID] = h
	}
	seen := map[string]bool{}
	var out []Target
	for _, role := range step.Roles {
		hostIDs, ok := roles[role]
		if !ok {
			return nil, fmt.Errorf("role %q not found", role)
		}
		for _, id := range hostIDs {
			if seen[id] {
				continue
			}
			h, ok := hostByID[id]
			if !ok {
				return nil, fmt.Errorf("host %q not found for role %q", id, role)
			}
			seen[id] = true
			out = append(out, Target{HostID: h.ID, HostName: h.Name})
		}
	}
	return out, nil
}

func stepsForPhase(p model.Pipeline, phase model.PipelinePhase) []model.Step {
	switch phase {
	case model.PhaseBuild:
		return p.Build
	case model.PhaseDeploy:
		return p.Deploy
	case model.PhaseFinally:
		return p.Finally
	default:
		return nil
	}
}

func pipelinePhases() []model.PipelinePhase {
	return []model.PipelinePhase{model.PhaseBuild, model.PhaseDeploy, model.PhaseFinally}
}
