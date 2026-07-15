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
//   - 不记录 SSH 凭据、私钥或 Agent token 明文
package api

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/tunnel"
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
	Disconnect(string)
}

type tunnelRuntimeInvalidatorFunc func(string)

func (f tunnelRuntimeInvalidatorFunc) Disconnect(hostID string) {
	f(hostID)
}

type remoteNodeMutationService interface {
	AddHost(hostWriteDTO) (model.Host, error)
	UpdateHost(string, hostWriteDTO) (model.Host, error)
	RemoveHost(string) error
	UpsertAgent(model.Agent) (model.Agent, error)
	RemoveAgent(string) error
}

type remoteNodeMutationApplication struct {
	hosts       remoteNodeHostStore
	agents      remoteNodeAgentStore
	invalidator tunnelRuntimeInvalidator
}

type invalidHostMutationError struct {
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

func newRemoteNodeMutationApplication(hosts remoteNodeHostStore, agents remoteNodeAgentStore, invalidator tunnelRuntimeInvalidator) remoteNodeMutationService {
	return &remoteNodeMutationApplication{hosts: hosts, agents: agents, invalidator: invalidator}
}

func (a *remoteNodeMutationApplication) AddHost(dto hostWriteDTO) (model.Host, error) {
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithField("operation", "add_host")
	log.Info("开始持久化 Host 连接配置")
	host, err := prepareNewHost(dto)
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

func (a *remoteNodeMutationApplication) UpdateHost(hostID string, dto hostWriteDTO) (model.Host, error) {
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
	updated := updatedHostFromWriteDTO(existing, dto)
	if err := importHostPrivateKey(&updated, dto.SSHKeyPath); err != nil {
		log.WithErr(err).Error("Host SSH 私钥导入失败")
		return model.Host{}, &invalidHostMutationError{cause: err}
	}
	if err := a.hosts.UpdateHost(updated); err != nil {
		log.WithErr(err).Error("Host 连接配置更新持久化失败")
		return model.Host{}, err
	}
	changedFields := changedHostTunnelTargetFields(existing, updated)
	if len(changedFields) > 0 {
		// 旧连接及握手证据只属于旧 target；持久化成功后必须立即使运行态过期。
		a.invalidator.Disconnect(hostID)
		log.WithField("changed_fields", changedFields).Info("Host tunnel target 已变更，旧隧道运行态已失效")
	}
	log.WithFields(hostSafeLogFields(updated)).Info("Host 连接配置更新完成")
	return updated, nil
}

func (a *remoteNodeMutationApplication) RemoveHost(hostID string) error {
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "remove_host",
		"host_id":   hostID,
	})
	log.Info("开始删除 Host 连接配置")
	if err := a.hosts.RemoveHost(hostID); err != nil {
		log.WithErr(err).Error("Host 连接配置删除失败")
		return err
	}
	// RemoveHost 是幂等持久化；成功后统一撤销可能残留的连接和在途拨号。
	a.invalidator.Disconnect(hostID)
	log.Info("Host 连接配置已删除，旧隧道运行态已失效")
	return nil
}

func (a *remoteNodeMutationApplication) UpsertAgent(agent model.Agent) (model.Agent, error) {
	hostID := agent.HostID
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
	saved, err := a.agents.UpsertAgent(agent)
	if err != nil {
		log.WithErr(err).Error("Agent 连接配置持久化失败")
		return model.Agent{}, err
	}
	if found && agentTunnelTargetChanged(existing, saved) {
		// Agent 的远端端口或 tunnel 能力改变后，旧转发目标不能继续复用。
		a.invalidator.Disconnect(hostID)
		log.WithFields(agentTunnelSafeLogFields(saved)).Info("Agent tunnel target 已变更，旧隧道运行态已失效")
	}
	log.WithField("tunnel_configured", agentHasTunnel(saved)).Info("Agent 连接配置持久化完成")
	return saved, nil
}

func (a *remoteNodeMutationApplication) RemoveAgent(hostID string) error {
	log := logger.GetLogger().WithEntryName("RemoteNodeMutation").WithFields(map[string]any{
		"operation": "remove_agent",
		"host_id":   hostID,
	})
	log.Info("开始删除 Agent 连接配置")
	existing, found, err := a.agents.AgentByHostID(hostID)
	if err != nil {
		log.WithErr(err).Error("读取待删除 Agent 连接配置失败")
		return err
	}
	if err := a.agents.RemoveAgent(hostID); err != nil {
		log.WithErr(err).Error("Agent 连接配置删除失败")
		return err
	}
	if found && agentHasTunnel(existing) {
		a.invalidator.Disconnect(hostID)
		log.Info("Agent tunnel 配置已删除，旧隧道运行态已失效")
	}
	log.Info("Agent 连接配置删除完成")
	return nil
}

func prepareNewHost(dto hostWriteDTO) (model.Host, error) {
	if err := normalizeHostWriteFingerprint(&dto); err != nil {
		return model.Host{}, err
	}
	host := hostFromWriteDTO(dto)
	if err := importHostPrivateKey(&host, dto.SSHKeyPath); err != nil {
		return model.Host{}, err
	}
	return host, nil
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

func agentTunnelTargetChanged(before, after model.Agent) bool {
	beforeHasTunnel := agentHasTunnel(before)
	afterHasTunnel := agentHasTunnel(after)
	if beforeHasTunnel != afterHasTunnel {
		return true
	}
	if !beforeHasTunnel {
		return false
	}
	return effectiveRemoteAgentPort(before) != effectiveRemoteAgentPort(after)
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
	if dto.ClearSSHHostKeyPin && strings.TrimSpace(dto.SSHHostKeyFingerprint) != "" {
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
