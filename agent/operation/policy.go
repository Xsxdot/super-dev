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
	"time"

	"github.com/superdev/agent/model"
)

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
			fmt.Sprintf("%s local deployment %s", runtimeVerb(kind), dep.ID),
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
	if dep.Location == model.LocationRemote {
		plan.Denied = true
		plan.RiskLevel = RiskCritical
		plan.Reasons = append(plan.Reasons, "remote deployment control is not supported by MCP safe operations")
	}
	if dep.Location == model.LocationLocal && !isDev && !plan.Denied {
		plan.RiskLevel = RiskHigh
		plan.RequiresApproval = true
		plan.Reasons = append(plan.Reasons, "environment is not marked as dev")
	}
	if dep.Location == model.LocationLocal && isDev && !plan.Denied {
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
