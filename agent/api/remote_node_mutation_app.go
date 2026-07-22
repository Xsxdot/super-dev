// remote_node_mutation_app.go 统一编排远端节点连接配置的持久化与运行态失效。
//
// 职责：
//   - 持久化 Host 与 Agent 连接配置变更
//   - 只在持久化成功且 tunnel.Target 语义改变后撤销旧隧道
//   - 为 HTTP handler 提供单一 mutation 入口和可测试的事务顺序
//
// 边界：
//   - 不解析 HTTP 请求或写 HTTP 响应
//   - 不建立隧道，只通过窄接口通知运行态失效
//   - 运行态已经失效但安全审计未完成时，返回可分类的部分成功错误
//   - 不记录 SSH 凭据、私钥或 Agent token 明文
package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
	apiassembler "github.com/xsxdot/super-dev/agent/api/internal/assembler"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/tunnel"
)

const (
	tunnelInvalidationTriggerHostChanged  = "host_connection_config_changed"
	tunnelInvalidationTriggerHostRemoved  = "host_removed"
	tunnelInvalidationTriggerAgentChanged = "agent_tunnel_target_changed"
	tunnelInvalidationTriggerAgentRemoved = "agent_removed"
	// tunnelInvalidationTriggerAgentDetached 标记 Detach 触发的配置删除；
	// 与 Uninstall 的 agent_removed 区分，避免跨入口把 Detach 恢复误报为远端卸载成功。
	tunnelInvalidationTriggerAgentDetached = "agent_detached"
	tunnelInvalidationTargetHost           = "host"
	tunnelInvalidationTargetAgent          = "agent"
	tunnelInvalidationMutationUpdate       = "update"
	tunnelInvalidationMutationDelete       = "delete"
)

type remoteNodeHostStore interface {
	ListHosts() ([]model.Host, error)
	AddHost(model.Host) (model.Host, error)
	UpdateHost(model.Host) error
	RemoveHost(string) error
}

type remoteNodeAgentStore interface {
	AgentByHostID(string) (model.Agent, bool, error)
	UpsertAgent(model.Agent) (model.Agent, error)
	RemoveAgent(string) error
}

type tunnelRuntimeInvalidator interface {
	Apply(context.Context, tunnelRuntimeInvalidation, func() error) (tunnelRuntimeInvalidationResult, error)
	Recover(context.Context, tunnelRuntimeInvalidationRecovery) (tunnelRuntimeInvalidationResult, error)
}

type tunnelRuntimeInvalidation struct {
	HostID           string
	Trigger          string
	ChangedFields    []string
	TargetKind       string
	Mutation         string
	ExpectedRevision string
}

type tunnelRuntimeInvalidationRecovery struct {
	HostID           string
	TargetKind       string
	Mutation         string
	ExpectedRevision string
	Persisted        bool
	// Trigger 非空时仅匹配该触发器留下的审计计划；为空匹配任意触发器。
	// 用于区分同一配置删除来自 Uninstall 还是 Detach。
	Trigger string
}

type tunnelRuntimeInvalidationResult struct {
	AuditPrepared     bool
	Persisted         bool
	TunnelInvalidated bool
	AuditCompleted    bool
	// RecoveredPending 表示本次 Recover 实际补偿了一个此前未终态的 prepared 计划；
	// 仅遇到已完成计划或无匹配计划时为 false，用于区分"卸载半途失败"与"配置本就不存在"。
	RecoveredPending bool
}

type hostMutationLock struct {
	mu   sync.Mutex
	refs int
}

type hostMutationLocks struct {
	mu    sync.Mutex
	locks map[string]*hostMutationLock
}

type remoteNodeMutationService interface {
	AddHost(context.Context, hostWriteDTO) (model.Host, error)
	UpdateHost(context.Context, string, hostWriteDTO) (model.Host, error)
	RemoveHost(context.Context, string) error
	UpsertAgent(context.Context, model.Agent) (model.Agent, error)
	RemoveAgent(context.Context, string) error
	// DetachAgentConfig 移除 Detach 场景的 Agent 配置，审计触发器与 Uninstall 区分。
	DetachAgentConfig(context.Context, string) error
	// RecoverPendingAgentRemoval 仅补偿指定触发器留下的未终态 Agent 删除审计计划；
	// trigger 为空时补偿任意触发器的计划。返回 true 表示本次调用完成了补偿。
	RecoverPendingAgentRemoval(ctx context.Context, hostID, trigger string) (bool, error)
}

type remoteNodeMutationApplication struct {
	hosts       remoteNodeHostStore
	agents      remoteNodeAgentStore
	assembler   *apiassembler.HostAssembler
	invalidator tunnelRuntimeInvalidator
	mutationMu  *hostMutationLocks
}

type invalidHostMutationError struct {
	cause error
}

type tunnelInvalidationAuditError struct {
	cause error
}

type tunnelInvalidationAuditUnavailableError struct {
	cause error
}

func (e *invalidHostMutationError) Error() string {
	if e == nil || e.cause == nil {
		return "invalid host mutation"
	}
	return e.cause.Error()
}

func (e *invalidHostMutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *tunnelInvalidationAuditError) Error() string {
	return "connection configuration change was saved and stale tunnel disconnected; its audit intent is durable, but audit completion is pending—retry the same request to complete it"
}

func (e *tunnelInvalidationAuditUnavailableError) Error() string {
	return "security audit is unavailable; connection configuration was not changed"
}

func (e *tunnelInvalidationAuditUnavailableError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *tunnelInvalidationAuditError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newRemoteNodeMutationApplication(hosts remoteNodeHostStore, agents remoteNodeAgentStore, assembler *apiassembler.HostAssembler, invalidator tunnelRuntimeInvalidator) remoteNodeMutationService {
	return &remoteNodeMutationApplication{
		hosts:       hosts,
		agents:      agents,
		assembler:   assembler,
		invalidator: invalidator,
		mutationMu:  &hostMutationLocks{locks: make(map[string]*hostMutationLock)},
	}
}

func (a *remoteNodeMutationApplication) AddHost(ctx context.Context, dto hostWriteDTO) (model.Host, error) {
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithField("operation", "add_host")
	log.Info("开始持久化 Host 连接配置")
	host, err := a.prepareNewHost(dto)
	if err != nil {
		log.WithErr(err).Error("Host 连接配置校验失败")
		return model.Host{}, &invalidHostMutationError{cause: err}
	}
	saved, err := a.hosts.AddHost(host)
	if err != nil {
		log.WithErr(err).Error("Host 连接配置持久化失败")
		return model.Host{}, err
	}
	log.WithFields(hostSafeLogFields(saved)).Info("Host 连接配置持久化完成")
	return saved, nil
}

func (a *remoteNodeMutationApplication) UpdateHost(ctx context.Context, hostID string, dto hostWriteDTO) (model.Host, error) {
	unlock := a.mutationMu.lock(hostID)
	defer unlock()
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "update_host",
		"host_id":   hostID,
	})
	log.Info("开始更新 Host 连接配置")
	if err := normalizeHostWriteFingerprint(&dto); err != nil {
		log.WithErr(err).Error("Host 连接配置校验失败")
		return model.Host{}, &invalidHostMutationError{cause: err}
	}
	existing, found, err := hostByID(a.hosts, hostID)
	if err != nil {
		log.WithErr(err).Error("读取待更新 Host 连接配置失败")
		return model.Host{}, err
	}
	if !found {
		log.Error("待更新 Host 不存在")
		return model.Host{}, remote.ErrNotFound
	}
	existing, err = a.recoverPendingHostInvalidation(ctx, existing)
	if err != nil {
		log.WithErr(err).Error("恢复 Host 待完成 tunnel 失效审计失败")
		return existing, err
	}
	updated := a.assembler.ApplyUpdate(existing, dto)
	if err := importHostPrivateKey(&updated, dto.SSHKeyPath); err != nil {
		log.WithErr(err).Error("Host SSH 私钥导入失败")
		return model.Host{}, &invalidHostMutationError{cause: err}
	}
	changedFields := changedHostTunnelTargetFields(existing, updated)
	if len(changedFields) == 0 {
		if err := a.hosts.UpdateHost(updated); err != nil {
			log.WithErr(err).Error("Host 连接配置更新持久化失败")
			return model.Host{}, err
		}
		log.WithFields(hostSafeLogFields(updated)).Info("Host 展示配置更新完成，tunnel target 未变化")
		return updated, nil
	}

	updated.PendingTunnelInvalidationRevision = uuid.NewString()
	result, applyErr := a.invalidator.Apply(ctx, tunnelRuntimeInvalidation{
		HostID:           hostID,
		Trigger:          tunnelInvalidationTriggerHostChanged,
		ChangedFields:    changedFields,
		TargetKind:       tunnelInvalidationTargetHost,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: updated.PendingTunnelInvalidationRevision,
	}, func() error {
		return a.hosts.UpdateHost(updated)
	})
	if applyErr != nil {
		classified := classifyTunnelInvalidationApplyError(result, applyErr)
		if result.Persisted {
			log.WithErr(classified).WithField("changed_fields", changedFields).Error("Host 连接配置已持久化且旧隧道已失效，但完成审计仍待恢复")
			return updated, classified
		}
		log.WithErr(classified).WithField("changed_fields", changedFields).Error("Host 连接配置未提交，tunnel 失效安全链路未完成")
		return model.Host{}, classified
	}
	// completed 审计已经持久化，清除与配置同文件提交的 outbox 标记；清理失败会在下次写入时幂等恢复。
	updated.PendingTunnelInvalidationRevision = ""
	if err := a.hosts.UpdateHost(updated); err != nil {
		log.WithErr(err).Warn("Host tunnel 失效审计已完成，但 outbox 标记清理失败")
	}
	log.WithField("changed_fields", changedFields).Info("Host tunnel target 已变更，旧隧道运行态已失效并完成审计")
	log.WithFields(hostSafeLogFields(updated)).Info("Host 连接配置更新完成")
	return updated, nil
}

func (a *remoteNodeMutationApplication) RemoveHost(ctx context.Context, hostID string) error {
	unlock := a.mutationMu.lock(hostID)
	defer unlock()
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "remove_host",
		"host_id":   hostID,
	})
	log.Info("开始删除 Host 连接配置")
	existing, found, err := hostByID(a.hosts, hostID)
	if err != nil {
		log.WithErr(err).Error("读取待删除 Host 连接配置失败")
		return err
	}
	if found {
		if _, err := a.recoverPendingHostInvalidation(ctx, existing); err != nil {
			log.WithErr(err).Error("删除前恢复 Host 待完成 tunnel 失效审计失败")
			return err
		}
	}
	if err := a.recoverDeleteInvalidation(ctx, hostID, tunnelInvalidationTargetHost, !found); err != nil {
		log.WithErr(err).Error("恢复 Host 删除的待完成 tunnel 失效审计失败")
		return err
	}
	if !found {
		log.Info("Host 连接配置已不存在，删除请求幂等完成")
		return nil
	}

	result, applyErr := a.invalidator.Apply(ctx, tunnelRuntimeInvalidation{
		HostID:        hostID,
		Trigger:       tunnelInvalidationTriggerHostRemoved,
		ChangedFields: []string{"host"},
		TargetKind:    tunnelInvalidationTargetHost,
		Mutation:      tunnelInvalidationMutationDelete,
	}, func() error {
		return a.hosts.RemoveHost(hostID)
	})
	if applyErr != nil {
		classified := classifyTunnelInvalidationApplyError(result, applyErr)
		if result.Persisted {
			log.WithErr(classified).Error("Host 连接配置已删除且旧隧道已失效，但完成审计仍待恢复")
		} else {
			log.WithErr(classified).Error("Host 连接配置未删除，tunnel 失效安全链路未完成")
		}
		return classified
	}
	log.Info("Host 连接配置已删除，旧隧道运行态已失效并完成审计")
	return nil
}

func (a *remoteNodeMutationApplication) UpsertAgent(ctx context.Context, agent model.Agent) (model.Agent, error) {
	hostID := agent.HostID
	unlock := a.mutationMu.lock(hostID)
	defer unlock()
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "upsert_agent",
		"host_id":   hostID,
	})
	log.Info("开始持久化 Agent 连接配置")
	existing, found, err := a.agents.AgentByHostID(hostID)
	if err != nil {
		log.WithErr(err).Error("读取现有 Agent 连接配置失败")
		return model.Agent{}, err
	}
	if found {
		existing, err = a.recoverPendingAgentInvalidation(ctx, existing)
		if err != nil {
			log.WithErr(err).Error("恢复 Agent 待完成 tunnel 失效审计失败")
			return existing, err
		}
		agent.PendingTunnelInvalidationRevision = existing.PendingTunnelInvalidationRevision
	} else {
		// 创建请求不能注入持久化内部 outbox 标记。
		agent.PendingTunnelInvalidationRevision = ""
	}
	model.ApplyAgentDefaults(&agent)
	changedFields := changedAgentTunnelTargetFields(existing, agent)
	if !found || len(changedFields) == 0 {
		saved, err := a.agents.UpsertAgent(agent)
		if err != nil {
			log.WithErr(err).Error("Agent 连接配置持久化失败")
			return model.Agent{}, err
		}
		log.WithField("tunnel_configured", agentHasTunnel(saved)).Info("Agent 连接配置持久化完成，tunnel target 未变化")
		return saved, nil
	}

	agent.PendingTunnelInvalidationRevision = uuid.NewString()
	var saved model.Agent
	result, applyErr := a.invalidator.Apply(ctx, tunnelRuntimeInvalidation{
		HostID:           hostID,
		Trigger:          tunnelInvalidationTriggerAgentChanged,
		ChangedFields:    changedFields,
		TargetKind:       tunnelInvalidationTargetAgent,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: agent.PendingTunnelInvalidationRevision,
	}, func() error {
		var persistErr error
		saved, persistErr = a.agents.UpsertAgent(agent)
		return persistErr
	})
	if applyErr != nil {
		classified := classifyTunnelInvalidationApplyError(result, applyErr)
		if result.Persisted {
			log.WithErr(classified).WithField("changed_fields", changedFields).Error("Agent 连接配置已持久化且旧隧道已失效，但完成审计仍待恢复")
			return saved, classified
		}
		log.WithErr(classified).WithField("changed_fields", changedFields).Error("Agent 连接配置未提交，tunnel 失效安全链路未完成")
		return model.Agent{}, classified
	}
	saved.PendingTunnelInvalidationRevision = ""
	if _, err := a.agents.UpsertAgent(saved); err != nil {
		log.WithErr(err).Warn("Agent tunnel 失效审计已完成，但 outbox 标记清理失败")
	}
	log.WithFields(agentTunnelSafeLogFields(saved)).Info("Agent tunnel target 已变更，旧隧道运行态已失效并完成审计")
	log.WithField("tunnel_configured", agentHasTunnel(saved)).Info("Agent 连接配置持久化完成")
	return saved, nil
}

func (a *remoteNodeMutationApplication) RemoveAgent(ctx context.Context, hostID string) error {
	return a.removeAgentWithTrigger(ctx, hostID, tunnelInvalidationTriggerAgentRemoved)
}

// DetachAgentConfig 移除 Detach 场景下的 Agent 连接配置，审计触发器与 Uninstall 区分，
// 保证恢复逻辑不会把 Detach 的部分失败当成远端卸载已完成。
func (a *remoteNodeMutationApplication) DetachAgentConfig(ctx context.Context, hostID string) error {
	return a.removeAgentWithTrigger(ctx, hostID, tunnelInvalidationTriggerAgentDetached)
}

func (a *remoteNodeMutationApplication) removeAgentWithTrigger(ctx context.Context, hostID, trigger string) error {
	unlock := a.mutationMu.lock(hostID)
	defer unlock()
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "remove_agent",
		"host_id":   hostID,
		"trigger":   trigger,
	})
	log.Info("开始删除 Agent 连接配置")
	existing, found, err := a.agents.AgentByHostID(hostID)
	if err != nil {
		log.WithErr(err).Error("读取待删除 Agent 连接配置失败")
		return err
	}
	if found {
		existing, err = a.recoverPendingAgentInvalidation(ctx, existing)
		if err != nil {
			log.WithErr(err).Error("删除前恢复 Agent 待完成 tunnel 失效审计失败")
			return err
		}
	}
	if err := a.recoverDeleteInvalidation(ctx, hostID, tunnelInvalidationTargetAgent, !found); err != nil {
		log.WithErr(err).Error("恢复 Agent 删除的待完成 tunnel 失效审计失败")
		return err
	}
	if !found {
		log.Info("Agent 连接配置已不存在，删除请求幂等完成")
		return nil
	}
	if !agentHasTunnel(existing) {
		if err := a.agents.RemoveAgent(hostID); err != nil {
			log.WithErr(err).Error("Agent 连接配置删除失败")
			return err
		}
		log.Info("无 tunnel 的 Agent 连接配置删除完成")
		return nil
	}

	result, applyErr := a.invalidator.Apply(ctx, tunnelRuntimeInvalidation{
		HostID:        hostID,
		Trigger:       trigger,
		ChangedFields: []string{"agent_tunnel_config"},
		TargetKind:    tunnelInvalidationTargetAgent,
		Mutation:      tunnelInvalidationMutationDelete,
	}, func() error {
		return a.agents.RemoveAgent(hostID)
	})
	if applyErr != nil {
		classified := classifyTunnelInvalidationApplyError(result, applyErr)
		if result.Persisted {
			log.WithErr(classified).Error("Agent 连接配置已删除且旧隧道已失效，但完成审计仍待恢复")
		} else {
			log.WithErr(classified).Error("Agent 连接配置未删除，tunnel 失效安全链路未完成")
		}
		return classified
	}
	log.Info("Agent 连接配置已删除，旧隧道运行态已失效并完成审计")
	return nil
}

func (a *remoteNodeMutationApplication) prepareNewHost(dto hostWriteDTO) (model.Host, error) {
	if err := normalizeHostWriteFingerprint(&dto); err != nil {
		return model.Host{}, err
	}
	host := a.assembler.ToModel(dto)
	if err := importHostPrivateKey(&host, dto.SSHKeyPath); err != nil {
		return model.Host{}, err
	}
	return host, nil
}

func (l *hostMutationLocks) lock(hostID string) func() {
	l.mu.Lock()
	entry := l.locks[hostID]
	if entry == nil {
		entry = &hostMutationLock{}
		l.locks[hostID] = entry
	}
	entry.refs++
	l.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		l.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(l.locks, hostID)
		}
		l.mu.Unlock()
	}
}

func hostByID(store remoteNodeHostStore, hostID string) (model.Host, bool, error) {
	hosts, err := store.ListHosts()
	if err != nil {
		return model.Host{}, false, err
	}
	for _, host := range hosts {
		if host.ID == hostID {
			return host, true, nil
		}
	}
	return model.Host{}, false, nil
}

func isInvalidHostMutation(err error) bool {
	var target *invalidHostMutationError
	return errors.As(err, &target)
}

func isTunnelInvalidationAuditError(err error) bool {
	var target *tunnelInvalidationAuditError
	return errors.As(err, &target)
}

func isTunnelInvalidationAuditUnavailableError(err error) bool {
	var target *tunnelInvalidationAuditUnavailableError
	return errors.As(err, &target)
}

func classifyTunnelInvalidationApplyError(result tunnelRuntimeInvalidationResult, err error) error {
	if err == nil {
		return nil
	}
	if result.Persisted && result.TunnelInvalidated {
		return &tunnelInvalidationAuditError{cause: err}
	}
	if !result.AuditPrepared {
		return &tunnelInvalidationAuditUnavailableError{cause: err}
	}
	return err
}

func classifyTunnelInvalidationRecoveryError(result tunnelRuntimeInvalidationResult, err error) error {
	if err == nil {
		return nil
	}
	if result.Persisted && result.TunnelInvalidated {
		return &tunnelInvalidationAuditError{cause: err}
	}
	return &tunnelInvalidationAuditUnavailableError{cause: err}
}

func (a *remoteNodeMutationApplication) recoverPendingHostInvalidation(ctx context.Context, host model.Host) (model.Host, error) {
	revision := strings.TrimSpace(host.PendingTunnelInvalidationRevision)
	if revision == "" {
		return host, nil
	}
	result, err := a.invalidator.Recover(ctx, tunnelRuntimeInvalidationRecovery{
		HostID:           host.ID,
		TargetKind:       tunnelInvalidationTargetHost,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: revision,
		Persisted:        true,
	})
	if err != nil {
		return host, classifyTunnelInvalidationRecoveryError(result, err)
	}
	if !result.AuditPrepared {
		return host, &tunnelInvalidationAuditUnavailableError{cause: errors.New("pending Host tunnel invalidation audit intent is missing")}
	}
	host.PendingTunnelInvalidationRevision = ""
	if err := a.hosts.UpdateHost(host); err != nil {
		return host, fmt.Errorf("清理 Host tunnel 失效 outbox 标记: %w", err)
	}
	return host, nil
}

func (a *remoteNodeMutationApplication) recoverPendingAgentInvalidation(ctx context.Context, agent model.Agent) (model.Agent, error) {
	revision := strings.TrimSpace(agent.PendingTunnelInvalidationRevision)
	if revision == "" {
		return agent, nil
	}
	result, err := a.invalidator.Recover(ctx, tunnelRuntimeInvalidationRecovery{
		HostID:           agent.HostID,
		TargetKind:       tunnelInvalidationTargetAgent,
		Mutation:         tunnelInvalidationMutationUpdate,
		ExpectedRevision: revision,
		Persisted:        true,
	})
	if err != nil {
		return agent, classifyTunnelInvalidationRecoveryError(result, err)
	}
	if !result.AuditPrepared {
		return agent, &tunnelInvalidationAuditUnavailableError{cause: errors.New("pending Agent tunnel invalidation audit intent is missing")}
	}
	agent.PendingTunnelInvalidationRevision = ""
	saved, err := a.agents.UpsertAgent(agent)
	if err != nil {
		return agent, fmt.Errorf("清理 Agent tunnel 失效 outbox 标记: %w", err)
	}
	return saved, nil
}

func (a *remoteNodeMutationApplication) recoverDeleteInvalidation(ctx context.Context, hostID, targetKind string, persisted bool) error {
	result, err := a.invalidator.Recover(ctx, tunnelRuntimeInvalidationRecovery{
		HostID:     hostID,
		TargetKind: targetKind,
		Mutation:   tunnelInvalidationMutationDelete,
		Persisted:  persisted,
	})
	return classifyTunnelInvalidationRecoveryError(result, err)
}

// RecoverPendingAgentRemoval 仅补偿指定触发器留下的未终态 Agent 删除审计计划；
// trigger 为空时补偿任意触发器的计划。
//
// 返回 true 表示本次调用实际完成了一个半途失败的删除补偿；遇到已完成计划或
// 无匹配计划时返回 false，调用方不得据此宣称远端卸载已执行。
func (a *remoteNodeMutationApplication) RecoverPendingAgentRemoval(ctx context.Context, hostID, trigger string) (bool, error) {
	unlock := a.mutationMu.lock(hostID)
	defer unlock()
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "recover_pending_agent_removal",
		"host_id":   hostID,
		"trigger":   trigger,
	})
	log.Info("开始检查并恢复 Agent 删除的待完成审计")
	result, err := a.invalidator.Recover(ctx, tunnelRuntimeInvalidationRecovery{
		HostID:     hostID,
		TargetKind: tunnelInvalidationTargetAgent,
		Mutation:   tunnelInvalidationMutationDelete,
		Persisted:  true,
		Trigger:    trigger,
	})
	if err != nil {
		log.WithErr(classifyTunnelInvalidationRecoveryError(result, err)).Error("恢复 Agent 删除的待完成审计失败")
		return false, classifyTunnelInvalidationRecoveryError(result, err)
	}
	if !result.RecoveredPending {
		log.Info("无待恢复的 Agent 删除审计计划")
		return false, nil
	}
	log.Info("Agent 删除的待完成审计已补偿完成")
	return true, nil
}

func changedHostTunnelTargetFields(before, after model.Host) []string {
	changed := make([]string, 0, 6)
	if before.SSHHost != after.SSHHost {
		changed = append(changed, "ssh_host")
	}
	if effectiveSSHPort(before.SSHPort) != effectiveSSHPort(after.SSHPort) {
		changed = append(changed, "ssh_port")
	}
	if before.SSHUser != after.SSHUser {
		changed = append(changed, "ssh_user")
	}
	if before.SSHPassword != after.SSHPassword {
		changed = append(changed, "ssh_password")
	}
	if before.SSHPrivateKey != after.SSHPrivateKey {
		changed = append(changed, "ssh_private_key")
	}
	if before.SSHHostKeyFingerprint != after.SSHHostKeyFingerprint {
		changed = append(changed, "ssh_host_key_fingerprint")
	}
	return changed
}

func effectiveSSHPort(port int) int {
	if port == 0 {
		return model.DefaultSSHPort
	}
	return port
}

func changedAgentTunnelTargetFields(before, after model.Agent) []string {
	beforeHasTunnel := agentHasTunnel(before)
	afterHasTunnel := agentHasTunnel(after)
	if beforeHasTunnel != afterHasTunnel {
		return []string{"tunnel_configured"}
	}
	if !beforeHasTunnel {
		return nil
	}
	if effectiveRemoteAgentPort(before) != effectiveRemoteAgentPort(after) {
		return []string{"remote_agent_port"}
	}
	return nil
}

func agentHasTunnel(agent model.Agent) bool {
	_, ok := agent.TunnelParams()
	return ok
}

func effectiveRemoteAgentPort(agent model.Agent) int {
	params, ok := agent.TunnelParams()
	if !ok || params == nil || params.RemoteAgentPort == 0 {
		return model.DefaultRemoteAgentPort
	}
	return params.RemoteAgentPort
}

func agentTunnelSafeLogFields(agent model.Agent) map[string]any {
	fields := map[string]any{"tunnel_configured": agentHasTunnel(agent)}
	if agentHasTunnel(agent) {
		fields["remote_agent_port"] = effectiveRemoteAgentPort(agent)
	}
	return fields
}

func expandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[2:])
}

func importHostPrivateKey(host *model.Host, keyPath string) error {
	if host == nil || strings.TrimSpace(keyPath) == "" {
		return nil
	}
	key, err := tunnel.ReadPrivateKey(expandHome(keyPath))
	if err != nil {
		return fmt.Errorf("读取私钥失败: %w", err)
	}
	host.SSHPrivateKey = string(key)
	return nil
}

func normalizeHostWriteFingerprint(dto *hostWriteDTO) error {
	if dto == nil {
		return nil
	}
	if dto.ClearSSHPassword && strings.TrimSpace(dto.SSHPassword) != "" {
		return errors.New("clear_ssh_password conflicts with ssh_password")
	}
	if dto.ClearSSHPrivateKey && (strings.TrimSpace(dto.SSHPrivateKey) != "" || strings.TrimSpace(dto.SSHKeyPath) != "") {
		return errors.New("clear_ssh_private_key conflicts with ssh_private_key or ssh_key_path")
	}
	if dto.ClearSSHHostKeyFingerprint && strings.TrimSpace(dto.SSHHostKeyFingerprint) != "" {
		return errors.New("clear_ssh_host_key_fingerprint conflicts with ssh_host_key_fingerprint")
	}
	if strings.TrimSpace(dto.SSHHostKeyFingerprint) == "" {
		return nil
	}
	fingerprint, err := tunnel.CanonicalHostKeyFingerprint(dto.SSHHostKeyFingerprint)
	if err != nil {
		return err
	}
	dto.SSHHostKeyFingerprint = fingerprint
	return nil
}

func hostSafeLogFields(host model.Host) map[string]any {
	return map[string]any{
		"host_id":                     host.ID,
		"ssh_credential_configured":   host.SSHPassword != "" || host.SSHPrivateKey != "",
		"ssh_host_key_pin_configured": host.SSHHostKeyFingerprint != "",
	}
}
