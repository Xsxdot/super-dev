// Package configchange 中的 plan.go 负责为配置写操作生成安全预检计划。
//
// 职责：
//   - 将配置 upsert 转换为 operation.Plan
//   - 生成稳定 fingerprint 供审批 token 绑定
//
// 边界：
//   - 不创建审批记录
//   - 不保存配置或执行运行时操作
package configchange

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

// Plan 为配置变更生成 agent-enforced safe operation plan。
//
// 参数：
//   - before: 变更前项目快照，当前用于保持 API 语义完整
//   - after: 变更后项目快照
//   - change: 原始配置变更请求
//   - diff: 已计算的结构化差异
//   - validation: 已完成的校验结果
//
// 返回：
//   - operation.Plan，合法配置写入默认需要审批，不合法请求直接 denied
func Plan(before model.Project, after model.Project, change ChangeRequest, diff []DiffEntry, validation ValidationResult) operation.Plan {
	_ = before
	now := time.Now().UTC()
	plan := operation.Plan{
		ID:               newPlanID(change.Kind, after.ID),
		Kind:             change.Kind,
		Target:           operation.Target{ProjectID: after.ID, ProjectName: after.Name},
		TargetSummary:    targetSummary(after, change),
		RiskLevel:        operation.RiskHigh,
		RequiresApproval: true,
		CreatedAt:        now,
		ExpiresAt:        now.Add(operation.DefaultPlanTTL),
		ExpectedEffects:  expectedEffects(change, after),
		Checks: []operation.Check{{
			Name:    "config_validation",
			Status:  validationStatus(validation),
			Message: validationMessage(validation),
		}},
	}
	if change.Kind == KindProjectUpsert && onlyProjectNameOrVariables(change) {
		plan.RiskLevel = operation.RiskMedium
	}
	if !validation.OK {
		plan.Denied = true
		plan.RiskLevel = operation.RiskCritical
		plan.RequiresApproval = false
		plan.Reasons = append(plan.Reasons, validation.Errors...)
	}
	plan.Fingerprint = fingerprint(map[string]any{
		"kind":             plan.Kind,
		"target":           plan.Target,
		"change":           normalizedChange(change),
		"diff":             diff,
		"expected_effects": plan.ExpectedEffects,
		"denied":           plan.Denied,
	})
	return plan
}

func targetSummary(project model.Project, change ChangeRequest) string {
	switch change.Kind {
	case KindServiceUpsert:
		if change.Service != nil {
			return fmt.Sprintf("%s/service %s", project.Name, change.Service.Name)
		}
	case KindPipelineUpsert:
		if change.Pipeline != nil {
			return fmt.Sprintf("%s/pipeline %s", project.Name, change.Pipeline.ID)
		}
	}
	return project.Name
}

func expectedEffects(change ChangeRequest, project model.Project) []string {
	switch change.Kind {
	case KindProjectUpsert:
		return []string{"update project configuration " + project.Name}
	case KindServiceUpsert:
		if change.Service != nil {
			return []string{"upsert service " + change.Service.Name}
		}
	case KindPipelineUpsert:
		if change.Pipeline != nil {
			id := change.Pipeline.ID
			if id == "" {
				id = slugID(change.Pipeline.Name)
			}
			return []string{"update project pipeline " + id}
		}
	}
	return []string{"update project configuration"}
}

func validationStatus(validation ValidationResult) string {
	if validation.OK {
		return "passed"
	}
	return "failed"
}

func validationMessage(validation ValidationResult) string {
	if validation.OK {
		return "config change validation passed"
	}
	return "config change validation failed"
}

func onlyProjectNameOrVariables(change ChangeRequest) bool {
	return change.Project != nil && len(change.Project.Environments) == 0
}

func normalizedChange(change ChangeRequest) ChangeRequest {
	change.ApprovalToken = ""
	change.DebugSessionID = ""
	return change
}

func fingerprint(parts any) string {
	raw, err := json.Marshal(parts)
	if err != nil {
		panic(fmt.Sprintf("fingerprint config change: %v", err))
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func newPlanID(kind string, projectID string) string {
	raw := kind + ":" + projectID
	sum := sha256.Sum256([]byte(raw))
	return "op_" + hex.EncodeToString(sum[:8])
}
