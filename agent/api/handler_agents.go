// handler_agents.go 实现 Agent 独立管理 API。
//
// 职责：
//   - 列出 Host 上已配置的 Agent
//   - 读取、更新、删除单个 Host 的 Agent 连接配置
//   - 触发一次 Agent 探活并返回运行态快照
//
// 边界：
//   - 不执行安装动作，安装命令与直推安装由独立 handler 承载
//   - 不直接管理 SSH 隧道，探活通过 NodeTransport/agenthealth 完成
//   - 不持久化运行态，Runtime 来自内存探活或 NodeRegistry 快照
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// listAgents 处理 GET /api/agents。
func (a *App) listAgents(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	agents, err := a.agentStore.ListAgents()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hostsByID := make(map[string]model.Host, len(hosts))
	for _, host := range hosts {
		hostsByID[host.ID] = host
	}
	out := make([]agentDTO, 0, len(agents))
	for _, agent := range agents {
		host, ok := hostsByID[agent.HostID]
		if !ok {
			continue
		}
		out = append(out, toAgentDTO(host, agent, a.nodeSnapshotOf(host.ID)))
	}
	jsonOK(w, out)
}

// getAgent 处理 GET /api/agents/{host_id}。
func (a *App) getAgent(w http.ResponseWriter, r *http.Request) {
	host, agent, found, err := a.agentByHostID(r.PathValue("host_id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	jsonOK(w, toAgentDTO(host, agent, a.nodeSnapshotOf(host.ID)))
}

// createAgent 处理 POST /api/agents。
func (a *App) createAgent(w http.ResponseWriter, r *http.Request) {
	var dto agentCreateDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	release, ok := a.acquireAgentLifecycleOperation(w, dto.HostID, "create_config")
	if !ok {
		return
	}
	defer release()

	host, found, err := a.remoteHostByID(dto.HostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}
	if _, exists, err := a.agentStore.AgentByHostID(host.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	} else if exists {
		jsonError(w, http.StatusConflict, "agent already exists")
		return
	}
	if !validAgentTransport(dto.Transport) {
		jsonError(w, http.StatusBadRequest, "invalid agent transport: empty chain, unsupported type, missing params, or duplicate transport")
		return
	}
	agent := model.Agent{
		HostID:    host.ID,
		Transport: dto.Transport,
		Config:    dto.Config,
		Security:  dto.Security,
	}
	saved, err := a.agentStore.UpsertAgent(agent)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toAgentDTO(host, saved, a.nodeSnapshotOf(host.ID)))
}

// updateAgent 处理旧版 PUT /api/agents/{host_id}，兼容为更新 transport。
func (a *App) updateAgent(w http.ResponseWriter, r *http.Request) {
	a.updateAgentTransport(w, r)
}

// updateAgentTransport 处理 PUT /api/agents/{host_id}/transport。
func (a *App) updateAgentTransport(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "update_transport")
	if !ok {
		return
	}
	defer release()

	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	var dto agentTransportUpdateDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validAgentTransport(dto.Transport) {
		jsonError(w, http.StatusBadRequest, "invalid agent transport: empty chain, unsupported type, missing params, or duplicate transport")
		return
	}
	agent.Transport = dto.Transport
	saved, err := a.agentStore.UpsertAgent(agent)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toAgentDTO(host, saved, a.nodeSnapshotOf(host.ID)))
}

// updateAgentConfig 处理 PUT /api/agents/{host_id}/config。
func (a *App) updateAgentConfig(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "update_config")
	if !ok {
		return
	}
	defer release()

	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	var dto agentConfigUpdateDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	secret := agent.Secret
	agent.Config = dto.Config
	agent.Security = dto.Security
	agent.Secret = secret
	saved, err := a.agentStore.UpsertAgent(agent)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, toAgentDTO(host, saved, a.nodeSnapshotOf(host.ID)))
}

// deleteAgent 处理 DELETE /api/agents/{host_id}。
func (a *App) deleteAgent(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	// 旧 DELETE 曾静默留下远端 Agent；必须强制调用方选择卸载或显式 Detach。
	logger.GetLogger().WithEntryName("AgentLifecycle").WithFields(map[string]any{
		"host_id":   hostID,
		"operation": "legacy_delete",
	}).Info("拒绝旧 Agent 配置删除旁路")
	jsonErrorCode(w, http.StatusConflict, "decommission_required", "remote Agent uninstall or explicit detach is required", map[string]string{
		"host_id": hostID,
	})
}

// checkAgent 处理 POST /api/agents/{host_id}/check。
func (a *App) checkAgent(w http.ResponseWriter, r *http.Request) {
	host, agent, found, err := a.agentByHostID(r.PathValue("host_id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	info := a.agentHealth.ProbeOnce(r.Context(), host.ID)
	agent.Runtime = agentRuntimeFromInfo(info)
	jsonOK(w, toAgentDTO(host, agent, a.nodeSnapshotOf(host.ID)))
}

func (a *App) agentByHostID(hostID string) (model.Host, model.Agent, bool, error) {
	host, found, err := a.remoteHostByID(hostID)
	if err != nil {
		return model.Host{}, model.Agent{}, false, err
	}
	if !found {
		return model.Host{}, model.Agent{}, false, nil
	}
	agent, ok, err := a.agentStore.AgentByHostID(host.ID)
	if err != nil {
		return model.Host{}, model.Agent{}, false, err
	}
	if !ok {
		return model.Host{}, model.Agent{}, false, nil
	}
	return host, agent, true, nil
}

func (a *App) nodeSnapshotOf(hostID string) *nodetransport.NodeStatus {
	if a.nodeRegistry == nil {
		return nil
	}
	status, ok := a.nodeRegistry.SnapshotOf(hostID)
	if !ok {
		return nil
	}
	return &status
}

func validAgentTransportType(typ model.TransportType) bool {
	switch typ {
	case model.TransportTypeTunnel, model.TransportTypeDirect:
		return true
	default:
		return false
	}
}

func validAgentTransport(cfg model.TransportConfig) bool {
	if len(cfg.Chain) == 0 {
		return false
	}
	seen := map[model.TransportType]struct{}{}
	for _, entry := range cfg.Chain {
		if !validAgentTransportType(entry.Type) {
			return false
		}
		switch entry.Type {
		case model.TransportTypeTunnel:
			if entry.Tunnel == nil {
				return false
			}
		case model.TransportTypeDirect:
			if entry.Direct == nil || strings.TrimSpace(entry.Direct.Address) == "" {
				return false
			}
		}
		if _, exists := seen[entry.Type]; exists {
			return false
		}
		seen[entry.Type] = struct{}{}
	}
	return true
}
