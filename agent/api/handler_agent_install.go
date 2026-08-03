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
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/installer"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

const (
	agentInstallMethodPushOverSSH = "push_over_ssh"
)

// installGuardProbeTimeout 限定安装前「既有 agent 探测守卫」的最长等待时间。
//
// 取 1.5 秒：留出一次跨网 HTTP 往返的余量，同时不能让「添加主机」这种交互式
// 操作被一台无响应的目标机拖住几十秒——探测失败本就直接放行（见下方
// guardAgainstExistingProvisionedAgent 的注释），拖长超时只会拖慢正常的
// “这台机器确实还没装 agent”路径，对守卫本身的正确性没有帮助。
const installGuardProbeTimeout = 1500 * time.Millisecond

// existingAgentDetectedErrorCode 是安装预探测守卫拦截时返回的稳定错误码，
// 供桌面端识别并引导用户改走纳管（接入请求）流程。
const existingAgentDetectedErrorCode = "existing_agent_detected"

type agentInstallRequest struct {
	Method string `json:"method"`
	// ForceReinstall 为 true 时跳过「既有 agent 探测守卫」，用户显式确认要
	// 盲目重装（例如确实要接管一台失联旧控制面的机器）。
	ForceReinstall bool `json:"force_reinstall"`
}

// existingAgentGuardResult 是安装预探测守卫一次探测的结论。
type existingAgentGuardResult struct {
	Blocked bool
	Version string
}

// guardAgainstExistingProvisionedAgent 在真正执行安装前，直接探测目标机的
// /api/security/health，判断它是否已经被某个（也许是别人的）控制面完整纳管过。
//
// 参数：
//   - ctx: 上层请求上下文；本方法会派生出带 installGuardProbeTimeout 短超时的
//     子 ctx，探测本身绝不拖住调用方太久
//   - hostID: 待探测的目标 host，经 App.nodeTransport 按 host_id 寻址
//
// 返回：
//   - existingAgentGuardResult.Blocked 为 true 时表示探到既有 provisioned
//     agent，应阻断本次盲目重装；Version 是探到的远端 agent 版本
//
// 注意：
//   - 探测走不通、超时或响应不含 provisioned 状态，一律返回 Blocked=false
//     放行安装：这条守卫是尽力而为的安全网，不是安装的前置门。连不通目标机，
//     大概率就是这台机器还没装 agent，不该反而拦住正常的首次安装。
func (a *App) guardAgainstExistingProvisionedAgent(ctx context.Context, hostID string) existingAgentGuardResult {
	probeCtx, cancel := context.WithTimeout(ctx, installGuardProbeTimeout)
	defer cancel()
	resp, err := a.nodeTransport.Do(probeCtx, hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   nodetransport.SecurityHealthPath,
	})
	if err != nil {
		// 探不通（未装 agent / 网络不可达 / 超时）：尽力而为，直接放行。
		return existingAgentGuardResult{}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return existingAgentGuardResult{}
	}
	var body nodetransport.SecurityHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return existingAgentGuardResult{}
	}
	if body.ProvisionState != string(model.AgentProvisionStateProvisioned) {
		return existingAgentGuardResult{}
	}
	return existingAgentGuardResult{Blocked: true, Version: body.Version}
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

	if req.ForceReinstall {
		// 用户显式确认要盲目重装（例如确实要接管一台失联旧控制面的机器），
		// 跳过探测守卫，直接走原安装路径。
		log.Printf("[SuperDev] installAgent 用户强制重装：host=%s 跳过既有 agent 探测守卫", host.ID)
	} else if guard := a.guardAgainstExistingProvisionedAgent(r.Context(), host.ID); guard.Blocked {
		log.Printf("[SuperDev] installAgent 守卫拦截：探到既有 provisioned agent host=%s version=%s，阻断盲目重装，请走纳管或显式 force_reinstall", host.ID, guard.Version)
		jsonWrite(w, http.StatusConflict, map[string]string{
			"code":    existingAgentDetectedErrorCode,
			"version": guard.Version,
		})
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
