// handler_agent_transports.go 实现桌面侧 agent transport 测试与安全下发。
//
// 职责：
//   - 对单个 chain entry 执行短探活并返回分类结果
//   - 使用 generated-command 保存的 bootstrap token 下发长期 token
//   - 把本地长期 token 和自动 TLS CA 写回 Host.Agent
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
	"io"
	"net/http"
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
	host, entry, ok := a.agentTransportEntryByIndex(w, r.PathValue("host_id"), req.Index)
	if !ok {
		return
	}
	result := nodetransport.ProbeResult{
		Index:         req.Index,
		TransportType: entry.Type,
		CheckedAt:     time.Now().UTC(),
	}
	start := time.Now()
	resp, err := a.nodeTransport.Do(r.Context(), host.ID, nodetransport.NodeRequest{Method: http.MethodGet, Path: nodetransport.SecurityHealthPath})
	result.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		result.Status = nodetransport.ProbeStatusUnreachable
		result.Error = err.Error()
		jsonOK(w, result)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		result.Status = nodetransport.ProbeStatusAuthFailed
		result.Error = "token rejected"
		jsonOK(w, result)
		return
	}
	if resp.StatusCode == http.StatusNotFound {
		result.Status = nodetransport.ProbeStatusVersionMismatch
		result.Error = "security health endpoint missing"
		jsonOK(w, result)
		return
	}
	if resp.StatusCode/100 != 2 {
		result.Status = nodetransport.ProbeStatusUnreachable
		result.Reachable = false
		result.Error = "security health failed"
		jsonOK(w, result)
		return
	}
	result.Reachable = true
	var health nodetransport.SecurityHealthResponse
	_ = json.NewDecoder(resp.Body).Decode(&health)
	result.Version = health.Version
	if health.ProvisionState == "pending-bootstrap" {
		result.Status = nodetransport.ProbeStatusPendingBootstrap
		jsonOK(w, result)
		return
	}
	authResp, err := a.nodeTransport.Do(r.Context(), host.ID, nodetransport.NodeRequest{Method: http.MethodGet, Path: nodetransport.SecurityAuthCheckPath})
	if err != nil {
		result.Status = nodetransport.ProbeStatusUnreachable
		result.Reachable = false
		result.Error = err.Error()
		jsonOK(w, result)
		return
	}
	defer authResp.Body.Close()
	if authResp.StatusCode == http.StatusUnauthorized {
		result.Status = nodetransport.ProbeStatusAuthFailed
		result.Reachable = false
		result.Error = "token rejected"
		jsonOK(w, result)
		return
	}
	if authResp.StatusCode/100 != 2 {
		result.Status = nodetransport.ProbeStatusUnreachable
		result.Reachable = false
		result.Error = "auth check failed"
		jsonOK(w, result)
		return
	}
	result.Status = nodetransport.ProbeStatusReachable
	jsonOK(w, result)
}

func (a *App) provisionAgent(w http.ResponseWriter, r *http.Request) {
	var req agentProvisionRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	host, entry, ok := a.agentTransportEntryByIndex(w, r.PathValue("host_id"), req.Index)
	if !ok {
		return
	}
	record, found := a.latestInstallTokenForHost(host.ID)
	if !found || record.BootstrapToken == "" {
		jsonError(w, http.StatusConflict, "bootstrap token unavailable; regenerate install command and reinstall agent")
		return
	}
	longToken, err := security.GenerateToken()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if host.Agent == nil {
		host.Agent = &model.Agent{}
	}
	host.Agent.Token = longToken
	if err := a.remoteStore.UpdateHost(host); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body, _ := json.Marshal(security.ProvisionRequest{Token: longToken, TLSMode: req.TLSMode})
	headers := http.Header{"Authorization": []string{"Bearer " + record.BootstrapToken}}
	resp, err := a.nodeTransport.Do(r.Context(), host.ID, nodetransport.NodeRequest{
		Method:  http.MethodPost,
		Path:    "/api/security/provision",
		Headers: headers,
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		jsonError(w, http.StatusBadGateway, "remote provision failed")
		return
	}
	var provisionResp security.ProvisionResponse
	_ = json.NewDecoder(resp.Body).Decode(&provisionResp)
	if provisionResp.CACert != "" && entry.Direct != nil {
		entry.Direct.CACert = provisionResp.CACert
		entry.Direct.TLS = true
		host.Agent.Transport.Chain[req.Index] = *entry
		if err := a.remoteStore.UpdateHost(host); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	jsonOK(w, map[string]any{"status": "provisioned", "restart_required": provisionResp.RestartRequired})
}

func (a *App) agentTransportEntryByIndex(w http.ResponseWriter, hostID string, index int) (model.Host, *model.TransportEntry, bool) {
	host, found, err := a.remoteHostByID(hostID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return model.Host{}, nil, false
	}
	if !found || host.Agent == nil {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return model.Host{}, nil, false
	}
	if index < 0 || index >= len(host.Agent.Transport.Chain) {
		jsonError(w, http.StatusBadRequest, "transport index out of range")
		return model.Host{}, nil, false
	}
	return host, &host.Agent.Transport.Chain[index], true
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
