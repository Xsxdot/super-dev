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
	"strings"
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
// 取 5 秒：守卫失效的后果是「盲目重装停掉别人正在用的 agent + 全部控制面凭据
// 集体失效」，探测预算必须覆盖真实链路的全价往返——SSH 隧道冷启动（建连 +
// 端口转发）加一次 HTTPS 握手在跨洋链路上轻松超过 1.5 秒，偏紧的超时会让守卫
// 系统性静默 fail-open。5 秒是「交互式操作可容忍的上限」与「给慢链路留足余量」
// 的折中；且超时不再静默放行——无法断定时放行但在安装响应里带
// guard_probe=inconclusive（见 installAgent），桌面端据此提示用户自行确认。
const installGuardProbeTimeout = 5 * time.Second

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
	// Inconclusive 表示探测无法断定目标机状态（超时/握手异常/非 agent 服务）：
	// 安装照常放行，但响应必须带出该事实，不允许静默——静默放行会把「探测
	// 预算不够」直接翻译成「盲目重装别人在用的 agent」。
	Inconclusive bool
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
//   - 探测经 doAgentRequestSchemeAware 先试 HTTPS(不验证证书)再退明文：被纳管
//     目标默认（tls_mode=auto）是自签 HTTPS 监听，只按本地记录的明文口径探测
//     会 100% 探不到既有 agent，守卫形同虚设
//   - 「连接被拒」= 端口上确定没有监听者，静默放行（正常的首次安装路径）；
//     其余失败（超时/握手异常/非 agent 服务/响应不可解析）放行但标记
//     Inconclusive，由调用方带出警示，不允许静默
//   - 探到 agent 但 provision_state 非 provisioned（如 pending-bootstrap 的
//     半成品安装）：放行，重装是合法的收尾手段
func (a *App) guardAgainstExistingProvisionedAgent(ctx context.Context, hostID string) existingAgentGuardResult {
	probeCtx, cancel := context.WithTimeout(ctx, installGuardProbeTimeout)
	defer cancel()
	resp, scheme, verdict, err := a.doAgentRequestSchemeAware(probeCtx, hostID, http.MethodGet, nodetransport.SecurityHealthPath, nil, nil)
	if err != nil {
		if verdict == agentProbeUnreachable {
			// 连接被拒：端口上确定没有监听者，大概率这台机器还没装 agent，
			// 不该拦住正常的首次安装，静默放行。
			return existingAgentGuardResult{}
		}
		log.Printf("[SuperDev] installAgent 守卫探测无法断定 host=%s：%v（放行并要求提示）", hostID, err)
		return existingAgentGuardResult{Inconclusive: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		// 端口上有东西在响应但不是可读的 agent 体检——可能是别的服务占了端口，
		// 也可能是版本极旧的 agent；无法断定，放行但要求提示。
		log.Printf("[SuperDev] installAgent 守卫探测得到非 2xx host=%s scheme=%s status=%d（放行并要求提示）", hostID, scheme, resp.StatusCode)
		return existingAgentGuardResult{Inconclusive: true}
	}
	var body nodetransport.SecurityHealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		log.Printf("[SuperDev] installAgent 守卫探测响应不可解析 host=%s scheme=%s：%v（放行并要求提示）", hostID, scheme, err)
		return existingAgentGuardResult{Inconclusive: true}
	}
	if body.ProvisionState != string(model.AgentProvisionStateProvisioned) {
		return existingAgentGuardResult{}
	}
	log.Printf("[SuperDev] installAgent 守卫探到既有 provisioned agent host=%s scheme=%s version=%s", hostID, scheme, body.Version)
	return existingAgentGuardResult{Blocked: true, Version: body.Version}
}

// existingAgentDirectAddress 返回既有 agent 探测守卫拦截时可回给桌面端的权威
// 直连地址。
//
// 参数：
//   - agent: 触发 409 的这条本机 Agent 配置（installAgent 已经查过、在作用域内）
//
// 返回：
//   - agent.Transport.Chain 中 direct 链项的 host:port（用户在"添加主机"连接链
//     步骤里填写、agent.DirectParams() 已封装的取值逻辑）；链上没有 direct 项
//     （纯 tunnel）时返回空字符串
//
// 注意：
//   - 只有 direct 地址对浏览器发起的裸 fetch 有意义——tunnel 项是走 SSH 转发，
//     桌面端纳管三端点的直连请求够不到它，所以不尝试从 tunnel 参数拼地址
//   - 不使用本控制面当前正在编辑的监听端口（securityForm.listenPort 对应的
//     agent.Config.ListenPort）：那是"这次准备装的新 agent 打算监听的端口"，
//     与目标机既有 agent 实际监听的端口毫无关系，用它拼地址是纯粹的误导
func existingAgentDirectAddress(agent model.Agent) string {
	direct, ok := agent.DirectParams()
	if !ok || direct == nil {
		return ""
	}
	return strings.TrimSpace(direct.Address)
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

	guardInconclusive := false
	if req.ForceReinstall {
		// 用户显式确认要盲目重装（例如确实要接管一台失联旧控制面的机器），
		// 跳过探测守卫，直接走原安装路径。
		log.Printf("[SuperDev] installAgent 用户强制重装：host=%s 跳过既有 agent 探测守卫", host.ID)
	} else if guard := a.guardAgainstExistingProvisionedAgent(r.Context(), host.ID); guard.Blocked {
		address := existingAgentDirectAddress(agent)
		log.Printf("[SuperDev] installAgent 守卫拦截：探到既有 provisioned agent host=%s version=%s，阻断盲目重装，请走纳管或显式 force_reinstall", host.ID, guard.Version)
		jsonWrite(w, http.StatusConflict, map[string]string{
			"code":    existingAgentDetectedErrorCode,
			"version": guard.Version,
			// address 是权威目标地址（agent.Transport.Chain 里配置的 direct
			// 直连地址，不是这次安装表单里正在编辑的本控制面自身监听端口）：
			// 桌面端纳管流程据此直连目标机，不再靠"目标机端口 == 我方配置的
			// 端口"这种猜测——两者本来就毫无关系。链上没有 direct 项（纯
			// tunnel）时留空，桌面端据此判断"当前配置下无法直连纳管"。
			"address": address,
		})
		return
	} else if guard.Inconclusive {
		// 探测无法断定目标机状态：放行安装，但必须把这个事实带到响应里
		// （见 existingAgentGuardResult.Inconclusive 注释），不允许静默。
		guardInconclusive = true
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
	opts := agentServiceOptionsFromSession(session, host)
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
	if guardInconclusive {
		jsonOK(w, withGuardProbeInconclusive(result))
		return
	}
	jsonOK(w, result)
}

// withGuardProbeInconclusive 在安装成功响应上附加 guard_probe=inconclusive 标记。
//
// 参数：
//   - result: 安装器返回的原始结果（任意可 JSON 序列化的结构）
//
// 返回：
//   - 展平后的 map，额外带 "guard_probe":"inconclusive"；序列化失败时退化为
//     只含标记的 map（宁可丢结果细节也不丢安全警示）
//
// 注意：
//   - 只在守卫探测无法断定时使用；正常路径保持原响应结构不动，避免无谓的
//     响应形状漂移
func withGuardProbeInconclusive(result any) map[string]any {
	flat := map[string]any{}
	if raw, err := json.Marshal(result); err == nil {
		_ = json.Unmarshal(raw, &flat)
	}
	flat["guard_probe"] = "inconclusive"
	return flat
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

func agentServiceOptionsFromSession(session agentInstallSession, host model.Host) installer.ServiceOptions {
	return installer.ServiceOptions{
		BindAddress:    session.Token.BindAddress,
		Port:           session.Token.RemoteAgentPort,
		RequireAuth:    session.Token.BootstrapToken != "",
		BootstrapToken: session.Token.BootstrapToken,
		User:           host.SSHUser,
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
