// 本文件负责 MCP 写操作的安全策略规划。
//
// 职责：
//   - 根据项目、环境、服务和 deployment 生成可解释的 operation plan
//   - 为模板导入生成绑定 digest 的审批计划
//
// 边界：
//   - 不执行运行态变更
//   - 不读写模板文件或审批存储
package operation

import (
	"fmt"
	"sort"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// RuntimeDeploymentTarget 描述批量运行态操作中的单个 deployment 目标。
//
// 参数：
//   - Service: deployment 所属服务，用于生成可读目标摘要
//   - Deployment: 将要被操作的 deployment
//
// 注意：
//   - 调用方应先完成“哪些服务被选中、哪些已运行”的业务过滤
//   - 本类型只承载安全预检所需的稳定目标信息
type RuntimeDeploymentTarget struct {
	Service    model.Service
	Deployment model.Deployment
}

// PlanRuntime 为 deployment 运行态写操作生成安全预检计划。
//
// 参数：
//   - kind: runtime.start/runtime.stop/runtime.restart
//   - project: deployment 所属项目
//   - service: deployment 所属服务
//   - dep: 解析后的 deployment
//
// 返回：
//   - 可用于审批和执行授权的稳定 plan
//   - 操作类型非法时返回错误
//
// 注意：
//   - 此函数只做策略判断，不启动或停止进程
func PlanRuntime(kind string, project model.Project, service model.Service, dep model.Deployment) (Plan, error) {
	if kind != OperationRuntimeStart && kind != OperationRuntimeStop && kind != OperationRuntimeRestart {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	env, isDev := findEnvironment(project, dep.EnvName)
	location := effectiveDeployLocation(dep)
	effectLocation := "local"
	if location == model.LocationRemote {
		effectLocation = "remote"
	}
	target := Target{
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		EnvName:      dep.EnvName,
		ServiceID:    service.ID,
		ServiceName:  service.Name,
		DeploymentID: dep.ID,
	}
	plan := Plan{
		ID:            newID("op"),
		Kind:          kind,
		Target:        target,
		TargetSummary: fmt.Sprintf("%s/%s/%s", project.Name, dep.EnvName, service.Name),
		RiskLevel:     RiskLow,
		CreatedAt:     now,
		ExpiresAt:     now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("%s %s deployment %s", runtimeVerb(kind), effectLocation, dep.ID),
		},
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: "deployment target resolved by agent"},
		},
	}

	if env.Name == "" {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment definition not found")
	}
	if dep.IsReadOnly() {
		plan.Denied = true
		plan.RiskLevel = RiskCritical
		plan.Reasons = append(plan.Reasons, "deployment is read-only")
	}
	if location == model.LocationRemote && !plan.Denied {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "remote deployment control requires approval")
	}
	if location == model.LocationLocal && !isDev && !plan.Denied {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment is not marked as dev")
	}
	if location == model.LocationLocal && isDev && !plan.Denied {
		plan.RiskLevel = RiskLow
		plan.RequiresApproval = false
	}

	// fingerprint 只绑定稳定目标和策略结果，避免时间戳或随机 plan ID 导致审批不可复用。
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

// PlanRuntimeStartSelected 为项目环境下的“启动所选”批量操作生成安全预检计划。
//
// 参数：
//   - project: deployment 所属项目
//   - envName: 被操作的环境名称
//   - targets: 已解析且需要启动的 deployment 目标列表
//
// 返回：
//   - 可用于审批和执行授权的稳定 plan
//   - envName 为空或 targets 为空时返回错误
//
// 注意：
//   - 此函数不判断服务是否被用户选中，只验证批量目标的安全边界
//   - fingerprint 会绑定 deployment ID 列表，避免 token 被换目标复用
func PlanRuntimeStartSelected(project model.Project, envName string, targets []RuntimeDeploymentTarget) (Plan, error) {
	envName = trim(envName)
	if envName == "" || len(targets) == 0 {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	env, isDev := findEnvironment(project, envName)
	target := Target{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		EnvName:     envName,
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationRuntimeStartSelected,
		Target:           target,
		TargetSummary:    fmt.Sprintf("%s/%s selected services", project.Name, envName),
		RiskLevel:        RiskLow,
		RequiresApproval: false,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: fmt.Sprintf("%d deployment target(s) resolved by agent", len(targets))},
		},
	}

	deploymentIDs := make([]string, 0, len(targets))
	effects := make([]string, 0, len(targets))
	hasRemoteTarget := false
	for _, item := range targets {
		dep := item.Deployment
		location := effectiveDeployLocation(dep)
		effectLocation := "local"
		if location == model.LocationRemote {
			effectLocation = "remote"
			hasRemoteTarget = true
		}
		deploymentIDs = append(deploymentIDs, dep.ID)
		effects = append(effects, fmt.Sprintf("start %s deployment %s", effectLocation, dep.ID))
		if dep.EnvName != envName {
			plan.Denied = true
			plan.RiskLevel = RiskCritical
			plan.Reasons = append(plan.Reasons, fmt.Sprintf("deployment %s is not in environment %s", dep.ID, envName))
		}
		if dep.IsReadOnly() {
			plan.Denied = true
			plan.RiskLevel = RiskCritical
			plan.Reasons = append(plan.Reasons, fmt.Sprintf("deployment %s is read-only", dep.ID))
		}
	}
	sort.Strings(deploymentIDs)
	sort.Strings(effects)
	plan.ExpectedEffects = effects

	if env.Name == "" {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment definition not found")
	}
	if !plan.Denied && hasRemoteTarget {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "remote deployment control requires approval")
	}
	if !plan.Denied && !isDev {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment is not marked as dev")
	}
	if !plan.Denied && isDev && !hasRemoteTarget {
		plan.RiskLevel = RiskLow
		plan.RequiresApproval = false
	}

	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"deployment_ids":   deploymentIDs,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan, nil
}

func effectiveDeployLocation(dep model.Deployment) model.DeployLocation {
	if dep.Location == "" {
		return model.LocationLocal
	}
	return dep.Location
}

// PlanTemplateImport 为用户模板导入生成安全预检计划。
//
// 参数：
//   - req: preview 通过后的模板路径、digest 和摘要
//
// 返回：
//   - 需要用户审批的 template.import plan
//   - 缺少 path 或 digest 时返回错误
//
// 注意：
//   - 模板 YAML preview 必须由调用方先完成
func PlanTemplateImport(req TemplateImportRequest) (Plan, error) {
	req.Path = trim(req.Path)
	req.Digest = trim(req.Digest)
	if req.Path == "" || req.Digest == "" {
		return Plan{}, ErrInvalidOperation
	}

	now := time.Now().UTC()
	summary := req.Summary
	if summary.Digest == "" {
		summary.Digest = req.Digest
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationTemplateImport,
		Target:           Target{TemplatePath: req.Path, TemplateDigest: req.Digest},
		TargetSummary:    fmt.Sprintf("%s@%s from %s", summary.ID, summary.Version, req.Path),
		RiskLevel:        RiskMedium,
		RequiresApproval: true,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("import user pipeline template %s@%s", summary.ID, summary.Version),
		},
		Checks: []Check{
			{Name: "template_preview", Status: "passed", Message: "template preview succeeded before import"},
		},
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
	})
	return plan, nil
}

// PlanPipelineRun 为运行项目级流水线（部署或回滚）生成安全预检计划。
//
// 参数：
//   - project: 流水线所属项目
//   - pipelineID: 流水线 ID
//   - envName: 运行环境名
//   - isRollback: 是否为回滚（影响 fingerprint 与摘要）
//   - artifactVersion: 回滚目标制品版本，部署时为空
//
// 返回：
//   - 基准要求审批的 plan；pipelineID 为空时返回错误
//
// 注意：
//   - 基准不区分环境，是否真正审批由 API 层按 settings 开关覆盖
//   - Target 带 ProjectID，使项目级豁免窗口可命中
func PlanPipelineRun(project model.Project, pipelineID, envName string, isRollback bool, artifactVersion string) (Plan, error) {
	pipelineID = trim(pipelineID)
	if pipelineID == "" {
		return Plan{}, ErrInvalidOperation
	}
	now := time.Now().UTC()
	verb := "deploy"
	if isRollback {
		verb = "rollback"
	}
	target := Target{
		ProjectID:   project.ID,
		ProjectName: project.Name,
		EnvName:     trim(envName),
		PipelineID:  pipelineID,
	}
	plan := Plan{
		ID:               newID("op"),
		Kind:             OperationPipelineRun,
		Target:           target,
		TargetSummary:    fmt.Sprintf("%s/%s pipeline %s (%s)", project.Name, trim(envName), pipelineID, verb),
		RiskLevel:        RiskHigh,
		RequiresApproval: true,
		CreatedAt:        now,
		ExpiresAt:        now.Add(DefaultPlanTTL),
		ExpectedEffects: []string{
			fmt.Sprintf("%s pipeline %s in %s", verb, pipelineID, trim(envName)),
		},
		Checks: []Check{
			{Name: "target_resolved", Status: "passed", Message: "pipeline target resolved by agent"},
		},
	}
	plan.Fingerprint = stableFingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"expected_effects": plan.ExpectedEffects,
		"is_rollback":      isRollback,
		"artifact_version": trim(artifactVersion),
	})
	return plan, nil
}

func findEnvironment(project model.Project, envName string) (model.Environment, bool) {
	for _, env := range project.Environments {
		if env.Name == envName {
			return env, env.IsDev
		}
	}
	return model.Environment{}, false
}

func runtimeVerb(kind string) string {
	switch kind {
	case OperationRuntimeStart:
		return "start"
	case OperationRuntimeStop:
		return "stop"
	case OperationRuntimeRestart:
		return "restart"
	default:
		return "operate"
	}
}
