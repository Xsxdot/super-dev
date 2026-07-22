// tunnel_invalidation_coordinator.go 编排连接目标持久化、旧 tunnel 失效与安全审计。
//
// 职责：
//   - 在配置持久化前写入 prepared 审计，形成可恢复的安全 outbox
//   - 配置提交后立即断开旧 tunnel，并写入 executed 终态审计
//   - 根据配置同文件保存的 revision 或删除结果补齐中断的审计计划
//
// 边界：
//   - 不解析 HTTP 请求，也不决定 Host/Agent 字段是否构成 target 变化
//   - 不读取或记录密码、私钥、token、fingerprint 原值
//   - 不负责清除配置记录中的 pending revision，由 mutation application 完成
package api

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

type auditedTunnelRuntimeInvalidator struct {
	statusOf   func(string) tunnel.Status
	disconnect func(string)
	auditStore func() operation.AuditStore
}

func newAuditedTunnelRuntimeInvalidator(statusOf func(string) tunnel.Status, disconnect func(string), auditStore func() operation.AuditStore) tunnelRuntimeInvalidator {
	return &auditedTunnelRuntimeInvalidator{statusOf: statusOf, disconnect: disconnect, auditStore: auditStore}
}

func (i *auditedTunnelRuntimeInvalidator) Apply(ctx context.Context, invalidation tunnelRuntimeInvalidation, persist func() error) (tunnelRuntimeInvalidationResult, error) {
	var result tunnelRuntimeInvalidationResult
	if ctx == nil {
		ctx = context.Background()
	}
	if persist == nil {
		return result, errors.New("tunnel invalidation persistence callback is required")
	}
	if err := validateTunnelRuntimeInvalidation(invalidation); err != nil {
		return result, err
	}
	plan, err := operation.PlanTunnelInvalidation(operation.TunnelInvalidationRequest{
		HostID:        invalidation.HostID,
		Trigger:       invalidation.Trigger,
		ChangedFields: invalidation.ChangedFields,
	})
	if err != nil {
		return result, fmt.Errorf("生成 tunnel 失效审计计划: %w", err)
	}
	previousStatus := i.statusOf(invalidation.HostID)
	log := tunnelInvalidationLogger(invalidation).WithField("previous_status", string(previousStatus))
	data := tunnelInvalidationAuditData(invalidation, previousStatus)
	log.Info("开始持久化 tunnel 失效 prepared 审计")
	if _, err := i.auditStore().Append(ctx, operation.AuditEvent{
		Kind:    operation.OperationTunnelInvalidate,
		Action:  operation.AuditPrepared,
		Plan:    plan,
		Summary: "connection target mutation prepared before stale tunnel invalidation",
		Data:    data,
	}); err != nil {
		log.WithErr(err).Error("tunnel 失效 prepared 审计写入失败，拒绝提交连接配置")
		return result, fmt.Errorf("写入 tunnel 失效 prepared 审计: %w", err)
	}
	result.AuditPrepared = true

	if err := persist(); err != nil {
		failureData := cloneTunnelInvalidationAuditData(data)
		failureData["mutation_persisted"] = false
		if _, auditErr := i.auditStore().Append(ctx, operation.AuditEvent{
			Kind:    operation.OperationTunnelInvalidate,
			Action:  operation.AuditFailed,
			Plan:    plan,
			Summary: "connection target mutation did not persist; stale tunnel was retained",
			Data:    failureData,
		}); auditErr != nil {
			// prepared 事件已持久化，即使终态写入故障也不会让本次安全意图消失。
			log.WithErr(auditErr).Error("连接配置持久化失败，且 tunnel 失效 failed 终态审计写入失败")
		}
		log.WithErr(err).Error("连接配置持久化失败，旧 tunnel 保持不变")
		return result, err
	}
	result.Persisted = true

	log.Info("连接配置已持久化，开始撤销旧 tunnel 运行态")
	i.disconnect(invalidation.HostID)
	result.TunnelInvalidated = true
	executedData := cloneTunnelInvalidationAuditData(data)
	executedData["mutation_persisted"] = true
	if _, err := i.auditStore().Append(ctx, operation.AuditEvent{
		Kind:    operation.OperationTunnelInvalidate,
		Action:  operation.AuditExecuted,
		Plan:    plan,
		Summary: "stale tunnel runtime invalidated after persisted connection target mutation",
		Data:    executedData,
	}); err != nil {
		log.WithErr(err).Error("旧 tunnel 已失效，prepared 审计已保留，但 executed 终态写入失败")
		return result, fmt.Errorf("写入 tunnel 失效 executed 审计: %w", err)
	}
	result.AuditCompleted = true
	log.Info("旧 tunnel 运行态已失效并完成安全审计")
	return result, nil
}

func (i *auditedTunnelRuntimeInvalidator) Recover(ctx context.Context, recovery tunnelRuntimeInvalidationRecovery) (tunnelRuntimeInvalidationResult, error) {
	var result tunnelRuntimeInvalidationResult
	if ctx == nil {
		ctx = context.Background()
	}
	events, err := i.auditStore().List(ctx, operation.AuditFilter{Kind: operation.OperationTunnelInvalidate})
	if err != nil {
		if recovery.Persisted && strings.TrimSpace(recovery.ExpectedRevision) != "" {
			// pending revision 与配置在同一次文件提交中落盘，证明 prepared 已先成功持久化。
			// 审计暂时不可读时仍需 fail-close，避免旧 target 在恢复窗口继续被复用。
			i.disconnect(recovery.HostID)
			result.AuditPrepared = true
			result.Persisted = true
			result.TunnelInvalidated = true
			logger.GetLogger().WithEntryName("RemoteNodeTunnelInvalidationRecovery").WithFields(map[string]any{
				"host_id":           recovery.HostID,
				"target_kind":       recovery.TargetKind,
				"mutation":          recovery.Mutation,
				"expected_revision": recovery.ExpectedRevision,
			}).WithErr(err).Error("审计暂时不可读，已依据 pending revision 断开旧 tunnel 并保留部分成功状态")
		}
		return result, fmt.Errorf("读取待恢复 tunnel 失效审计: %w", err)
	}
	terminalPlanActions := make(map[string]string, len(events))
	for _, event := range events {
		if event.Action == operation.AuditExecuted || event.Action == operation.AuditFailed {
			if _, exists := terminalPlanActions[event.Plan.ID]; !exists {
				terminalPlanActions[event.Plan.ID] = event.Action
			}
		}
	}

	matchedPrepared := false
	for _, event := range events {
		if event.Action != operation.AuditPrepared || event.Plan.Target.HostID != recovery.HostID {
			continue
		}
		if !tunnelInvalidationRecoveryMatches(event, recovery) {
			continue
		}
		matchedPrepared = true
		result.AuditPrepared = true
		if terminalAction, terminal := terminalPlanActions[event.Plan.ID]; terminal {
			result.AuditCompleted = true
			if terminalAction == operation.AuditExecuted {
				result.Persisted = true
				result.TunnelInvalidated = true
			}
			continue
		}
		data := cloneTunnelInvalidationAuditData(event.Data)
		data["recovered"] = true
		data["mutation_persisted"] = recovery.Persisted
		if !recovery.Persisted {
			if _, err := i.auditStore().Append(ctx, operation.AuditEvent{
				Kind:    operation.OperationTunnelInvalidate,
				Action:  operation.AuditFailed,
				Plan:    event.Plan,
				Summary: "prepared connection target mutation was not persisted; stale tunnel was retained",
				Data:    data,
			}); err != nil {
				return result, fmt.Errorf("恢复 tunnel 失效 failed 审计: %w", err)
			}
			result.AuditCompleted = true
			continue
		}

		log := logger.GetLogger().WithEntryName("RemoteNodeTunnelInvalidationRecovery").WithFields(map[string]any{
			"host_id":           recovery.HostID,
			"target_kind":       recovery.TargetKind,
			"mutation":          recovery.Mutation,
			"expected_revision": recovery.ExpectedRevision,
			"plan_id":           event.Plan.ID,
		})
		log.Info("检测到已持久化但未完成的 tunnel 失效审计，开始补偿")
		i.disconnect(recovery.HostID)
		result.Persisted = true
		result.TunnelInvalidated = true
		if _, err := i.auditStore().Append(ctx, operation.AuditEvent{
			Kind:    operation.OperationTunnelInvalidate,
			Action:  operation.AuditExecuted,
			Plan:    event.Plan,
			Summary: "stale tunnel runtime invalidation audit completed by recovery",
			Data:    data,
		}); err != nil {
			log.WithErr(err).Error("tunnel 失效补偿已断开旧连接，但 executed 审计仍写入失败")
			return result, fmt.Errorf("恢复 tunnel 失效 executed 审计: %w", err)
		}
		result.AuditCompleted = true
		log.Info("tunnel 失效补偿与 executed 审计完成")
	}
	if !matchedPrepared {
		for _, event := range events {
			if event.Action != operation.AuditExecuted || event.Plan.Target.HostID != recovery.HostID {
				continue
			}
			if !tunnelInvalidationRecoveryMatches(event, recovery) {
				continue
			}
			// executed 携带完整 plan 与 revision；prepared 被正常裁剪后仍足以证明同一安全意图已完成。
			result.AuditPrepared = true
			result.Persisted = true
			result.TunnelInvalidated = true
			result.AuditCompleted = true
			break
		}
	}
	return result, nil
}

func validateTunnelRuntimeInvalidation(invalidation tunnelRuntimeInvalidation) error {
	if invalidation.TargetKind != tunnelInvalidationTargetHost && invalidation.TargetKind != tunnelInvalidationTargetAgent {
		return errors.New("invalid tunnel invalidation target kind")
	}
	switch invalidation.Mutation {
	case tunnelInvalidationMutationUpdate:
		if strings.TrimSpace(invalidation.ExpectedRevision) == "" {
			return errors.New("tunnel invalidation update requires expected revision")
		}
	case tunnelInvalidationMutationDelete:
		if strings.TrimSpace(invalidation.ExpectedRevision) != "" {
			return errors.New("tunnel invalidation delete cannot carry expected revision")
		}
	default:
		return errors.New("invalid tunnel invalidation mutation")
	}
	return nil
}

func tunnelInvalidationAuditData(invalidation tunnelRuntimeInvalidation, previousStatus tunnel.Status) map[string]any {
	return map[string]any{
		"trigger":           invalidation.Trigger,
		"changed_fields":    append([]string(nil), invalidation.ChangedFields...),
		"previous_status":   string(previousStatus),
		"target_kind":       invalidation.TargetKind,
		"mutation":          invalidation.Mutation,
		"expected_revision": invalidation.ExpectedRevision,
	}
}

func cloneTunnelInvalidationAuditData(data map[string]any) map[string]any {
	cloned := make(map[string]any, len(data)+2)
	for key, value := range data {
		cloned[key] = value
	}
	return cloned
}

func tunnelInvalidationRecoveryMatches(event operation.AuditEvent, recovery tunnelRuntimeInvalidationRecovery) bool {
	if auditString(event.Data, "target_kind") != recovery.TargetKind || auditString(event.Data, "mutation") != recovery.Mutation {
		return false
	}
	if recovery.Mutation == tunnelInvalidationMutationUpdate {
		return auditString(event.Data, "expected_revision") == recovery.ExpectedRevision
	}
	return true
}

func auditString(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func tunnelInvalidationLogger(invalidation tunnelRuntimeInvalidation) *logger.Log {
	return logger.GetLogger().WithEntryName("RemoteNodeTunnelInvalidation").WithFields(map[string]any{
		"host_id":        invalidation.HostID,
		"trigger":        invalidation.Trigger,
		"changed_fields": invalidation.ChangedFields,
		"target_kind":    invalidation.TargetKind,
		"mutation":       invalidation.Mutation,
	})
}
