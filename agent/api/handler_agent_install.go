// handler_agent_install.go 实现 Agent SSH 直推安装 API。
//
// 职责：
//   - 将一等 Agent 配置转成远端 agent 服务启动参数
//   - 通过 Host SSH 凭据调用 installer 执行安装或重装
//   - 将 installer 的分阶段错误转换成 HTTP 响应
//
// 边界：
//   - 不生成用户可复制的自助安装命令，该能力由 agent_install_command.go 负责
//   - 不直接执行 SSH 命令，全部委托 installer
//   - 不修改 Host 身份字段
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	agentInstallMethodPushOverSSH = "push_over_ssh"
)

type agentInstallRequest struct {
	Method string `json:"method"`
}

type agentUpdateTargetResponse struct {
	Version            string `json:"version"`
	Source             string `json:"source"`
	ConcurrencyDefault int    `json:"concurrency_default"`
}

type agentUpdateBinaryResponse struct {
	OK        bool   `json:"ok"`
	HostID    string `json:"host_id"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updated_at"`
}

// installAgent 处理 POST /api/agents/{host_id}/install。
func (a *App) installAgent(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "install")
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

	var req agentInstallRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Method == "" {
		req.Method = agentInstallMethodPushOverSSH
	}
	if req.Method != agentInstallMethodPushOverSSH {
		jsonError(w, http.StatusBadRequest, "unsupported install method")
		return
	}

	sessionReq, err := prepareAgentInstallSessionRequest(agent, agentInstallCommandRequest{
		TransportType:   firstAgentTransportType(agent),
		TokenTTLMinutes: defaultAgentInstallTokenTTLMinutes,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !agentHasTransport(agent, sessionReq.TransportType) {
		jsonError(w, http.StatusBadRequest, "transport_type is not configured for agent")
		return
	}
	session := newAgentInstallSession(host.ID, sessionReq, time.Now().UTC())
	a.rememberAgentInstallToken(session.Token)
	opts := agentServiceOptionsFromSession(session)
	result, err := a.hostAgentInstaller.Install(r.Context(), host, opts)
	if err != nil {
		var installErr *installer.InstallError
		if errors.As(err, &installErr) {
			jsonWrite(w, http.StatusBadGateway, map[string]string{
				"error": installErr.Error(),
				"stage": installErr.Stage,
			})
			return
		}
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	agent = resetAgentSecurityForBootstrap(agent)
	if _, err := a.remoteNodeMutations.UpsertAgent(r.Context(), agent); err != nil {
		if writeRemoteNodeMutationPartialError(w, err) {
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, result)
}

// restartAgent 处理 POST /api/agents/{host_id}/restart。
func (a *App) restartAgent(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "restart")
	if !ok {
		return
	}
	defer release()

	host, _, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}
	result, err := a.hostAgentInstaller.Restart(r.Context(), host)
	if err != nil {
		var installErr *installer.InstallError
		if errors.As(err, &installErr) {
			jsonWrite(w, http.StatusBadGateway, map[string]string{
				"error": installErr.Error(),
				"stage": installErr.Stage,
			})
			return
		}
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

// getAgentUpdateTarget 处理 GET /api/agents/update-target。
func (a *App) getAgentUpdateTarget(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, agentUpdateTargetResponse{
		Version:            agentAPIVersion,
		Source:             "bundled",
		ConcurrencyDefault: 3,
	})
}

// updateAgentBinary 处理 POST /api/agents/{host_id}/update-binary。
func (a *App) updateAgentBinary(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "update_binary")
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
	if !toAgentDTO(host, agent, a.nodeSnapshotOf(host.ID)).Runtime.Installed {
		jsonError(w, http.StatusBadRequest, "agent is not installed")
		return
	}

	result, err := a.hostAgentInstaller.UpdateBinary(r.Context(), host)
	if err != nil {
		var installErr *installer.InstallError
		if errors.As(err, &installErr) {
			jsonWrite(w, http.StatusBadGateway, map[string]string{
				"error": installErr.Error(),
				"stage": installErr.Stage,
			})
			return
		}
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, agentUpdateBinaryResponse{
		OK:        result.OK,
		HostID:    result.HostID,
		Platform:  result.Platform,
		Version:   agentAPIVersion,
		Message:   result.Message,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}

func agentServiceOptionsFromSession(session agentInstallSession) installer.ServiceOptions {
	return installer.ServiceOptions{
		BindAddress:    session.Token.BindAddress,
		Port:           session.Token.RemoteAgentPort,
		RequireAuth:    session.Token.BootstrapToken != "",
		BootstrapToken: session.Token.BootstrapToken,
	}
}

func resetAgentSecurityForBootstrap(agent model.Agent) model.Agent {
	mode := agent.Security.TLS.Mode
	if mode == "" {
		mode = model.AgentTLSModeAuto
	}
	serverName := agent.Security.TLS.ServerName
	agent.Secret.Token = ""
	agent.Security = model.AgentSecurity{
		ProvisionState:  model.AgentProvisionStatePendingBootstrap,
		TokenConfigured: false,
		TLS: model.AgentTLSSpec{
			Mode:       mode,
			ServerName: serverName,
		},
	}
	return agent
}

func firstAgentTransportType(agent model.Agent) model.TransportType {
	model.ApplyAgentDefaults(&agent)
	if len(agent.Transport.Chain) == 0 {
		return model.TransportTypeTunnel
	}
	return agent.Transport.Chain[0].Type
}
