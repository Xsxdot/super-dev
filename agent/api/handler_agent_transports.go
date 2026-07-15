// handler_agent_transports.go 实现桌面侧 agent transport 测试与安全下发。
//
// 职责：
//   - 对单个 chain entry 执行短探活并返回分类结果
//   - 使用 generated-command 保存的 bootstrap token 下发长期 token
//   - 把本地长期 token 和 TLS 状态写回 AgentStore
//
// 边界：
//   - 不执行远端安装
//   - 不保存 bootstrap token 到 hosts.json
//   - 不绕过 NodeTransport 直接拼网络地址
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	var req agentProvisionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	target, entry, ok := a.agentTransportEntryByIndex(w, r.PathValue("host_id"), req.Index)
	if !ok {
		return
	}
	record, found := a.latestInstallTokenForHost(target.Host.ID)
	if !found || record.BootstrapToken == "" {
		jsonError(w, http.StatusConflict, "bootstrap token unavailable; regenerate install command and reinstall agent")
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
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		jsonError(w, http.StatusBadGateway, remoteProvisionErrorMessage(resp.StatusCode, resp.Body))
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
	jsonOK(w, map[string]any{"status": "provisioned", "restart_required": provisionResp.RestartRequired})
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
