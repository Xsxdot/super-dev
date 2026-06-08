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

// installAgent 处理 POST /api/agents/{host_id}/install。
func (a *App) installAgent(w http.ResponseWriter, r *http.Request) {
	host, agent, found, err := a.agentByHostID(r.PathValue("host_id"))
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
	jsonOK(w, result)
}

func agentServiceOptionsFromSession(session agentInstallSession) installer.ServiceOptions {
	return installer.ServiceOptions{
		BindAddress:    session.Token.BindAddress,
		Port:           session.Token.RemoteAgentPort,
		RequireAuth:    session.Token.BootstrapToken != "",
		BootstrapToken: session.Token.BootstrapToken,
	}
}

func firstAgentTransportType(agent model.Agent) model.TransportType {
	model.ApplyAgentDefaults(&agent)
	if len(agent.Transport.Chain) == 0 {
		return model.TransportTypeTunnel
	}
	return agent.Transport.Chain[0].Type
}
