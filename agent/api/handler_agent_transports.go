// handler_agent_transports.go 实现桌面侧 agent transport 测试与安全下发。
//
// 职责：
//   - 对单个 chain entry 执行短探活并返回分类结果
//   - 使用 generated-command 保存的 bootstrap token 下发长期 token
//   - 在 bootstrap token 缺失或过期时给出可操作的引导错误
//   - 把本地长期 token 和 TLS 状态写回 AgentStore
//
// 边界：
//   - 不执行远端安装
//   - 不保存 bootstrap token 到 hosts.json
//   - 不绕过 NodeTransport 直接拼网络地址
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/security"
)

type agentTransportTestRequest struct {
	Index int `json:"index"`
}

type agentProvisionRequest struct {
	Index   int    `json:"index"`
	TLSMode string `json:"tls_mode"`
}

func (a *App) testAgentTransport(w http.ResponseWriter, r *http.Request) {
	var req agentTransportTestRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target, entry, ok := a.agentTransportEntryByIndex(w, r.PathValue("host_id"), req.Index)
	if !ok {
		return
	}
	provider := a.nodeTransportProvider(entry.Type)
	jsonOK(w, nodetransport.ProbeEntry(r.Context(), provider, target, req.Index, *entry, 800*time.Millisecond))
}

func (a *App) provisionAgent(w http.ResponseWriter, r *http.Request) {
	hostID := r.PathValue("host_id")
	release, ok := a.acquireAgentLifecycleOperation(w, hostID, "provision")
	if !ok {
		return
	}
	defer release()

	var req agentProvisionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target, entry, ok := a.agentTransportEntryByIndex(w, hostID, req.Index)
	if !ok {
		return
	}
	// bootstrap token 仅存在于进程内存（安全上不落盘明文），因此桌面端重启、
	// token 过期都会让它消失。此时必须引导用户重新执行安装以下发新 token，
	// 否则请求会带空 Authorization 打到 agent，只能拿到含义不明的 401。
	record, found := a.latestInstallTokenForHost(target.Host.ID)
	if !found || strings.TrimSpace(record.BootstrapToken) == "" {
		log.Printf("[SuperDev] provision aborted host=%s reason=bootstrap_token_missing", target.Host.ID)
		jsonError(w, http.StatusConflict, "bootstrap token unavailable (desktop restarted or install token expired); click Install and Start again to push a fresh token")
		return
	}
	if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(time.Now().UTC()) {
		log.Printf("[SuperDev] provision aborted host=%s reason=bootstrap_token_expired expires_at=%s", target.Host.ID, record.ExpiresAt.Format(time.RFC3339))
		jsonError(w, http.StatusConflict, "bootstrap token expired; click Install and Start again to push a fresh token")
		return
	}
	agent := target.Agent
	longToken := strings.TrimSpace(agent.Secret.Token)
	if longToken == "" {
		var err error
		longToken, err = security.GenerateToken()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		// 发请求前先把长期 token 落盘：agent 侧 Provision 是不可逆写操作
		// （改状态 + 焚毁 bootstrap token），只要响应在回程丢失（隧道被
		// restart_required 触发的重启打断就会产生 EOF），agent 已经前进而
		// 桌面端一无所知，重试时又生成一个新 token —— 永远命不中 agent 的
		// 幂等分支，只能拿到 bootstrap 已焚毁的 401，陷入死循环。
		// 先落盘后，重试会复用同一个 token，直接命中幂等分支恢复。
		var persistErr error
		agent, persistErr = a.persistProvisioningToken(r.Context(), agent, longToken)
		if persistErr != nil {
			log.Printf("[SuperDev] provision aborted host=%s reason=persist_token_failed err=%v", target.Host.ID, persistErr)
			jsonError(w, http.StatusInternalServerError, persistErr.Error())
			return
		}
	}
	body, _ := json.Marshal(security.ProvisionRequest{
		Token:   longToken,
		TLSMode: req.TLSMode,
		Hosts:   provisionHostsForChain(agent.Transport.Chain),
	})
	headers := http.Header{"Authorization": []string{"Bearer " + record.BootstrapToken}}
	resp, err := a.nodeTransportProvider(entry.Type).Do(r.Context(), target.Host.ID, nodetransport.NodeRequest{
		Method:      http.MethodPost,
		Path:        "/api/security/provision",
		TLSOverride: bootstrapProvisionTLSOverride(agent),
		Headers:     headers,
		Body:        bytes.NewReader(body),
	})
	if err != nil {
		// 传输层失败（如隧道被 agent 重启打断产生的 EOF）此前完全没有日志，
		// 只能靠前端弹出的一行报文反推，这里补上 transport 与 TLS 上下文。
		log.Printf("[SuperDev] provision transport failed host=%s transport=%s tls_override=%t err=%v",
			target.Host.ID, entry.Type, bootstrapProvisionTLSOverride(agent) != nil, err)
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		message := remoteProvisionErrorMessage(resp.StatusCode, resp.Body)
		log.Printf("[SuperDev] provision rejected by agent host=%s status=%d detail=%s", target.Host.ID, resp.StatusCode, message)
		jsonError(w, http.StatusBadGateway, message)
		return
	}
	var provisionResp security.ProvisionResponse
	_ = json.NewDecoder(resp.Body).Decode(&provisionResp)
	agent.Secret.Token = longToken
	agent.Security.TokenConfigured = true
	agent.Security.ProvisionState = model.AgentProvisionStateProvisioned
	tlsMode := provisionResp.TLSMode
	if strings.TrimSpace(tlsMode) == "" {
		tlsMode = req.TLSMode
	}
	agent.Security.TLS.Mode = model.AgentTLSMode(tlsMode)
	agent.Security.TLS.CACert = provisionResp.CACert
	if agent.Security.TLS.Mode == "" {
		agent.Security.TLS.Mode = model.AgentTLSModeOff
	}
	if _, err := a.remoteNodeMutations.UpsertAgent(r.Context(), agent); err != nil {
		if writeRemoteNodeMutationPartialError(w, err) {
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[SuperDev] provision succeeded host=%s transport=%s tls_mode=%s restart_required=%t",
		target.Host.ID, entry.Type, agent.Security.TLS.Mode, provisionResp.RestartRequired)
	jsonOK(w, map[string]any{"status": "provisioned", "restart_required": provisionResp.RestartRequired})
}

// persistProvisioningToken 在发起 provision 前先把长期 token 落盘。
//
// 参数：
//   - ctx: 请求上下文
//   - agent: 当前 Agent 配置（token 为空）
//   - longToken: 本次准备下发的长期 token
//
// 返回：
//   - 已写入 token 的 Agent 配置
//   - 持久化错误
//
// 注意：
//   - 只写 Secret.Token，不把 ProvisionState 提前改成 provisioned：
//     此刻远端是否成功尚未可知，提前置位会让 UI 谎报成功，也会让
//     bootstrapProvisionTLSOverride 误判而改用 TLS 发下一次请求
//   - TokenConfigured 同样保持不变，它表示「已确认下发成功」
func (a *App) persistProvisioningToken(ctx context.Context, agent model.Agent, longToken string) (model.Agent, error) {
	agent.Secret.Token = longToken
	saved, err := a.remoteNodeMutations.UpsertAgent(ctx, agent)
	if err != nil {
		return agent, err
	}
	return saved, nil
}

func (a *App) agentTransportEntryByIndex(w http.ResponseWriter, hostID string, index int) (nodetransport.NodeTarget, *model.TransportEntry, bool) {
	host, agent, found, err := a.agentByHostID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return nodetransport.NodeTarget{}, nil, false
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return nodetransport.NodeTarget{}, nil, false
	}
	if index < 0 || index >= len(agent.Transport.Chain) {
		jsonError(w, http.StatusBadRequest, "transport index out of range")
		return nodetransport.NodeTarget{}, nil, false
	}
	target := nodetransport.NodeTarget{Host: host, Agent: agent}
	return target, &target.Agent.Transport.Chain[index], true
}

func (a *App) nodeTransportProvider(typ model.TransportType) nodetransport.NodeTransport {
	if a.nodeTransportProviders != nil {
		if provider := a.nodeTransportProviders[typ]; provider != nil {
			return provider
		}
	}
	return a.nodeTransport
}

func provisionHosts(entry *model.TransportEntry) []string {
	if entry == nil {
		return nil
	}
	switch entry.Type {
	case model.TransportTypeTunnel:
		// tunnel 客户端固定访问本机转发端口，auto TLS 证书必须覆盖该校验名。
		return []string{"127.0.0.1"}
	case model.TransportTypeDirect:
		return directProvisionHosts(entry)
	default:
		return nil
	}
}

func provisionHostsForChain(chain []model.TransportEntry) []string {
	if len(chain) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(chain))
	seen := make(map[string]struct{}, len(chain))
	for i := range chain {
		for _, host := range provisionHosts(&chain[i]) {
			host = strings.TrimSpace(host)
			if host == "" {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			hosts = append(hosts, host)
		}
	}
	return hosts
}

func directProvisionHosts(entry *model.TransportEntry) []string {
	if entry == nil || entry.Direct == nil || strings.TrimSpace(entry.Direct.Address) == "" {
		return nil
	}
	address := strings.TrimSpace(entry.Direct.Address)
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	u, err := url.Parse(address)
	if err != nil {
		return nil
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return nil
	}
	return []string{host}
}

func bootstrapProvisionTLSOverride(agent model.Agent) *model.AgentTLSSpec {
	if agent.Security.ProvisionState == model.AgentProvisionStateProvisioned {
		return nil
	}
	return &model.AgentTLSSpec{Mode: model.AgentTLSModeOff}
}

func remoteProvisionErrorMessage(status int, body io.Reader) string {
	data, _ := io.ReadAll(body)
	detail := strings.TrimSpace(string(data))
	if detail != "" {
		var parsed struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &parsed) == nil && strings.TrimSpace(parsed.Error) != "" {
			detail = strings.TrimSpace(parsed.Error)
		}
		return fmt.Sprintf("remote provision failed (%d): %s", status, detail)
	}
	return fmt.Sprintf("remote provision failed (%d)", status)
}

func (a *App) latestInstallTokenForHost(hostID string) (agentInstallTokenRecord, bool) {
	a.agentInstallTokenMu.Lock()
	defer a.agentInstallTokenMu.Unlock()
	var newest agentInstallTokenRecord
	found := false
	for _, record := range a.agentInstallTokens {
		if record.HostID != hostID {
			continue
		}
		if !found || record.ExpiresAt.After(newest.ExpiresAt) {
			newest = record
			found = true
		}
	}
	return newest, found
}

func decodeJSONBody(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}
