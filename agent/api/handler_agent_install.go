// handler_agent_install.go 实现 Agent SSH 直推安装 API。
//
// 职责：
//   - 将一等 Agent 配置转成远端 agent 服务启动参数
//   - 通过 Host SSH 凭据调用 installer 执行安装或重装
//   - 将 installer 的分阶段错误转换成 HTTP 响应
//
// 边界：
//   - 不生成自助安装命令，该能力由 agent_install_command.go 负责
//   - 不直接执行 SSH 命令，全部委托 installer
//   - 不修改 Host 身份字段
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
)

const agentInstallMethodPushOverSSH = "push_over_ssh"

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

	result, err := a.hostAgentInstaller.Install(r.Context(), host, agentServiceOptions(agent, a.cfg.BootstrapToken))
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

func agentServiceOptions(agent model.Agent, bootstrapToken string) installer.ServiceOptions {
	bindAddress := strings.TrimSpace(agent.Config.ListenAddress)
	if bindAddress == "" {
		bindAddress = defaultAgentInstallBindAddress
	}
	port := agent.Config.ListenPort
	if port <= 0 {
		port = defaultAgentInstallPort
	}
	return installer.ServiceOptions{
		BindAddress:    bindAddress,
		Port:           port,
		RequireAuth:    strings.TrimSpace(bootstrapToken) != "",
		BootstrapToken: bootstrapToken,
	}
}
