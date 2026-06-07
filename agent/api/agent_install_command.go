// agent_install_command.go 生成 Agent 自助安装命令。
//
// 职责：
//   - 校验 generated-command 安装参数
//   - 生成绑定 Host 与安装参数的短期 token
//   - 返回可复制执行的 curl | bash 命令
//
// 边界：
//   - 不执行远端安装
//   - 不实现 token 兑换安装脚本
//   - 不把 token 明文写入持久化文件或普通响应字段
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	agentInstallMethodGeneratedCommand = "generated_command"
	defaultAgentInstallBindAddress     = "127.0.0.1"
	defaultAgentInstallPort            = 57017
	defaultAgentInstallTokenTTLMinutes = 30
)

type agentInstallCommandRequest struct {
	Method          string              `json:"method"`
	ControllerURL   string              `json:"controller_url"`
	BindAddress     string              `json:"bind_address"`
	RemoteAgentPort int                 `json:"remote_agent_port"`
	TransportType   model.TransportType `json:"transport_type"`
	TokenTTLMinutes int                 `json:"token_ttl_minutes"`
}

type agentInstallCommandResponse struct {
	Command   string `json:"command"`
	ExpiresAt string `json:"expires_at"`
	TokenID   string `json:"token_id"`
}

type agentInstallTokenRecord struct {
	TokenID         string
	TokenHash       string
	BootstrapToken  string
	HostID          string
	TransportType   model.TransportType
	BindAddress     string
	RemoteAgentPort int
	ExpiresAt       time.Time
}

type agentInstallCommandResult struct {
	Response agentInstallCommandResponse
	Token    agentInstallTokenRecord
}

// generateAgentInstallCommand 生成安装命令和仅供服务端保存的 token 记录。
func generateAgentInstallCommand(hostID string, req agentInstallCommandRequest, now time.Time) (agentInstallCommandResult, error) {
	normalized, err := normalizeAgentInstallCommandRequest(req)
	if err != nil {
		return agentInstallCommandResult{}, err
	}
	tokenID := uuid.NewString()
	token := uuid.NewString()
	bootstrapToken := uuid.NewString()
	expiresAt := now.Add(time.Duration(normalized.TokenTTLMinutes) * time.Minute).UTC()
	scriptURL := fmt.Sprintf(
		"%s/api/agents/install.sh?token=%s",
		strings.TrimRight(normalized.ControllerURL, "/"),
		url.QueryEscape(token),
	)
	command := fmt.Sprintf(
		"curl -fsSL %s | bash -s -- --host-id %s --transport %s --bind-address %s --port %d --bootstrap-token %s --require-auth",
		shellQuote(scriptURL),
		shellArg(hostID),
		shellArg(string(normalized.TransportType)),
		shellArg(normalized.BindAddress),
		normalized.RemoteAgentPort,
		shellArg(bootstrapToken),
	)
	return agentInstallCommandResult{
		Response: agentInstallCommandResponse{
			Command:   command,
			ExpiresAt: expiresAt.Format(time.RFC3339),
			TokenID:   tokenID,
		},
		Token: agentInstallTokenRecord{
			TokenID:         tokenID,
			TokenHash:       hashAgentInstallToken(token),
			BootstrapToken:  bootstrapToken,
			HostID:          hostID,
			TransportType:   normalized.TransportType,
			BindAddress:     normalized.BindAddress,
			RemoteAgentPort: normalized.RemoteAgentPort,
			ExpiresAt:       expiresAt,
		},
	}, nil
}

// generateAgentInstallCommand 处理 POST /api/agents/{host_id}/install-command。
func (a *App) generateAgentInstallCommand(w http.ResponseWriter, r *http.Request) {
	host, found, err := a.remoteHostByID(r.PathValue("host_id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "host not found")
		return
	}

	var req agentInstallCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := generateAgentInstallCommand(host.ID, req, time.Now().UTC())
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.rememberAgentInstallToken(result.Token)
	jsonOK(w, result.Response)
}

func normalizeAgentInstallCommandRequest(req agentInstallCommandRequest) (agentInstallCommandRequest, error) {
	req.Method = strings.TrimSpace(req.Method)
	if req.Method == "" {
		req.Method = agentInstallMethodGeneratedCommand
	}
	if req.Method != agentInstallMethodGeneratedCommand {
		return agentInstallCommandRequest{}, errors.New("unsupported install method")
	}
	req.ControllerURL = strings.TrimSpace(req.ControllerURL)
	if req.ControllerURL == "" {
		return agentInstallCommandRequest{}, errors.New("controller_url is required")
	}
	parsedURL, err := url.Parse(req.ControllerURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return agentInstallCommandRequest{}, errors.New("controller_url must be a valid absolute URL")
	}
	req.BindAddress = strings.TrimSpace(req.BindAddress)
	if req.BindAddress == "" {
		req.BindAddress = defaultAgentInstallBindAddress
	}
	if req.RemoteAgentPort <= 0 {
		req.RemoteAgentPort = defaultAgentInstallPort
	}
	if req.TransportType == "" {
		req.TransportType = model.TransportTypeTunnel
	}
	if !validAgentTransportType(req.TransportType) {
		return agentInstallCommandRequest{}, errors.New("invalid transport_type")
	}
	if req.TokenTTLMinutes <= 0 {
		req.TokenTTLMinutes = defaultAgentInstallTokenTTLMinutes
	}
	return req, nil
}

func (a *App) rememberAgentInstallToken(record agentInstallTokenRecord) {
	a.agentInstallTokenMu.Lock()
	defer a.agentInstallTokenMu.Unlock()
	if a.agentInstallTokens == nil {
		a.agentInstallTokens = map[string]agentInstallTokenRecord{}
	}
	a.agentInstallTokens[record.TokenID] = record
}

func hashAgentInstallToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func shellArg(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == ':' || r == '/' ||
			(r >= '0' && r <= '9') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= 'a' && r <= 'z'))
	}) == -1 {
		return value
	}
	return shellQuote(value)
}
