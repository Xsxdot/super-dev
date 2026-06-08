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
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strconv"
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
	Command        string `json:"command"`
	RestartCommand string `json:"restart_command"`
	ExpiresAt      string `json:"expires_at"`
	TokenID        string `json:"token_id"`
}

type agentInstallTokenRecord struct {
	TokenID         string
	TokenHash       string
	BootstrapToken  string
	HostID          string
	ControllerURL   string
	TransportType   model.TransportType
	BindAddress     string
	RemoteAgentPort int
	ExpiresAt       time.Time
}

type agentInstallCommandResult struct {
	Response agentInstallCommandResponse
	Token    agentInstallTokenRecord
}

type agentInstallSession struct {
	Request      agentInstallCommandRequest
	InstallToken string
	Token        agentInstallTokenRecord
}

func prepareAgentInstallSessionRequest(agent model.Agent, req agentInstallCommandRequest) (agentInstallCommandRequest, error) {
	applyAgentInstallDefaultsFromAgent(&req, agent)
	req.ControllerURL = strings.TrimSpace(req.ControllerURL)
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

func newAgentInstallSession(hostID string, req agentInstallCommandRequest, now time.Time) agentInstallSession {
	tokenID := uuid.NewString()
	installToken := uuid.NewString()
	bootstrapToken := uuid.NewString()
	expiresAt := now.Add(time.Duration(req.TokenTTLMinutes) * time.Minute).UTC()
	return agentInstallSession{
		Request:      req,
		InstallToken: installToken,
		Token: agentInstallTokenRecord{
			TokenID:         tokenID,
			TokenHash:       hashAgentInstallToken(installToken),
			BootstrapToken:  bootstrapToken,
			HostID:          hostID,
			ControllerURL:   req.ControllerURL,
			TransportType:   req.TransportType,
			BindAddress:     req.BindAddress,
			RemoteAgentPort: req.RemoteAgentPort,
			ExpiresAt:       expiresAt,
		},
	}
}

func agentInstallCommandResultFromSession(hostID string, session agentInstallSession) agentInstallCommandResult {
	scriptURL := fmt.Sprintf(
		"%s/api/agents/install.sh?token=%s",
		strings.TrimRight(session.Request.ControllerURL, "/"),
		url.QueryEscape(session.InstallToken),
	)
	command := fmt.Sprintf(
		"curl -fsSL %s | bash -s -- --host-id %s --transport %s --bind-address %s --port %d --bootstrap-token %s --require-auth",
		shellQuote(scriptURL),
		shellArg(hostID),
		shellArg(string(session.Request.TransportType)),
		shellArg(session.Request.BindAddress),
		session.Request.RemoteAgentPort,
		shellArg(session.Token.BootstrapToken),
	)
	return agentInstallCommandResult{
		Response: agentInstallCommandResponse{
			Command:        command,
			RestartCommand: agentRestartCommand(),
			ExpiresAt:      session.Token.ExpiresAt.Format(time.RFC3339),
			TokenID:        session.Token.TokenID,
		},
		Token: session.Token,
	}
}

func agentRestartCommand() string {
	return "if command -v systemctl >/dev/null 2>&1; then sudo -n systemctl restart superdev-agent.service; elif command -v launchctl >/dev/null 2>&1; then sudo -n launchctl kickstart -k system/dev.superdev.agent || launchctl kickstart -k gui/$(id -u)/dev.superdev.agent; else echo 'unsupported service manager for superdev-agent restart' >&2; exit 64; fi"
}

// generateAgentInstallCommand 生成安装命令和仅供服务端保存的 token 记录。
func generateAgentInstallCommand(hostID string, req agentInstallCommandRequest, now time.Time) (agentInstallCommandResult, error) {
	prepared, err := prepareAgentInstallSessionRequest(model.Agent{}, req)
	if err != nil {
		return agentInstallCommandResult{}, err
	}
	normalized, err := normalizeAgentInstallCommandRequest(prepared)
	if err != nil {
		return agentInstallCommandResult{}, err
	}
	session := newAgentInstallSession(hostID, normalized, now)
	return agentInstallCommandResultFromSession(hostID, session), nil
}

func generateAgentInstallCommandForAgent(hostID string, agent model.Agent, req agentInstallCommandRequest, now time.Time) (agentInstallCommandResult, error) {
	prepared, err := prepareAgentInstallSessionRequest(agent, req)
	if err != nil {
		return agentInstallCommandResult{}, err
	}
	if !agentHasTransport(agent, prepared.TransportType) {
		return agentInstallCommandResult{}, errors.New("transport_type is not configured for agent")
	}
	normalized, err := normalizeAgentInstallCommandRequest(prepared)
	if err != nil {
		return agentInstallCommandResult{}, err
	}
	session := newAgentInstallSession(hostID, normalized, now)
	return agentInstallCommandResultFromSession(hostID, session), nil
}

// generateAgentInstallCommand 处理 POST /api/agents/{host_id}/install-command。
func (a *App) generateAgentInstallCommand(w http.ResponseWriter, r *http.Request) {
	host, agent, found, err := a.agentByHostID(r.PathValue("host_id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !found {
		jsonError(w, http.StatusNotFound, "agent not configured")
		return
	}

	var req agentInstallCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := generateAgentInstallCommandForAgent(host.ID, agent, req, time.Now().UTC())
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.rememberAgentInstallToken(result.Token)
	jsonOK(w, result.Response)
}

func applyAgentInstallDefaultsFromAgent(req *agentInstallCommandRequest, agent model.Agent) {
	model.ApplyAgentDefaults(&agent)
	if req.TransportType == "" && len(agent.Transport.Chain) > 0 {
		req.TransportType = agent.Transport.Chain[0].Type
	}
	if strings.TrimSpace(req.BindAddress) == "" {
		req.BindAddress = agent.ResolveBindAddress()
	}
	if req.RemoteAgentPort <= 0 {
		if req.TransportType == model.TransportTypeTunnel {
			if params, ok := agent.TunnelParams(); ok && params.RemoteAgentPort > 0 {
				req.RemoteAgentPort = params.RemoteAgentPort
				return
			}
		}
		if agent.Config.ListenPort > 0 {
			req.RemoteAgentPort = agent.Config.ListenPort
		}
	}
}

func agentHasTransport(agent model.Agent, typ model.TransportType) bool {
	if typ == "" {
		return false
	}
	for _, entry := range agent.Transport.Chain {
		if entry.Type == typ {
			return true
		}
	}
	return false
}

func (a *App) serveAgentInstallScript(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	record, ok := a.installTokenRecordForToken(token, time.Now().UTC())
	if !ok {
		jsonError(w, http.StatusUnauthorized, "invalid or expired install token")
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	fmt.Fprint(w, agentInstallScript(record, token))
}

func (a *App) serveAgentInstallBinary(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.installTokenRecordForToken(r.URL.Query().Get("token"), time.Now().UTC()); !ok {
		jsonError(w, http.StatusUnauthorized, "invalid or expired install token")
		return
	}
	exe, err := os.Executable()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-SuperDev-Agent-OS", runtime.GOOS)
	w.Header().Set("X-SuperDev-Agent-Arch", runtime.GOARCH)
	http.ServeFile(w, r, exe)
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

func (a *App) installTokenRecordForToken(token string, now time.Time) (agentInstallTokenRecord, bool) {
	if strings.TrimSpace(token) == "" {
		return agentInstallTokenRecord{}, false
	}
	tokenHash := hashAgentInstallToken(token)
	a.agentInstallTokenMu.Lock()
	defer a.agentInstallTokenMu.Unlock()
	for _, record := range a.agentInstallTokens {
		if !record.ExpiresAt.After(now) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(tokenHash)) == 1 {
			return record, true
		}
	}
	return agentInstallTokenRecord{}, false
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

func agentInstallScript(record agentInstallTokenRecord, installToken string) string {
	port := strconv.Itoa(record.RemoteAgentPort)
	defaultBinaryURL := strings.TrimRight(record.ControllerURL, "/") + "/api/agents/install-binary?token=" + url.QueryEscape(installToken)
	return fmt.Sprintf(`#!/usr/bin/env sh
set -eu

usage() {
  echo "Usage: install.sh --host-id <host-id> --transport <transport> --bind-address <address> --port <port> --bootstrap-token <token> --require-auth" >&2
}

HOST_ID=""
TRANSPORT=""
BIND_ADDRESS=""
PORT=""
BOOTSTRAP_TOKEN=""
REQUIRE_AUTH="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --host-id) HOST_ID="${2:-}"; shift 2 ;;
    --transport) TRANSPORT="${2:-}"; shift 2 ;;
    --bind-address) BIND_ADDRESS="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --bootstrap-token) BOOTSTRAP_TOKEN="${2:-}"; shift 2 ;;
    --require-auth) REQUIRE_AUTH="true"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage; exit 2 ;;
  esac
done

if [ "$HOST_ID" != %s ] || [ "$TRANSPORT" != %s ] || [ "$BIND_ADDRESS" != %s ] || [ "$PORT" != %s ] || [ "$BOOTSTRAP_TOKEN" != %s ]; then
  echo "install token does not match requested host, transport, bind address, port, or bootstrap token" >&2
  exit 64
fi
if [ -z "$BIND_ADDRESS" ] || [ -z "$PORT" ] || [ -z "$BOOTSTRAP_TOKEN" ] || [ "$REQUIRE_AUTH" != "true" ]; then
  usage
  exit 64
fi
target_os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$target_os" in
  linux) target_os="linux" ;;
  darwin) target_os="darwin" ;;
  *) echo "unsupported target OS: $target_os" >&2; exit 64 ;;
esac
target_arch="$(uname -m)"
case "$target_arch" in
  x86_64|amd64) target_arch="amd64" ;;
  arm64|aarch64) target_arch="arm64" ;;
  *) echo "unsupported target arch: $target_arch" >&2; exit 64 ;;
esac
DEFAULT_BINARY_URL=%s
binary_url="${SUPERDEV_AGENT_BINARY_URL:-$DEFAULT_BINARY_URL}"
if [ -z "${SUPERDEV_AGENT_BINARY_URL:-}" ] && [ "${target_os}/${target_arch}" != "%s/%s" ]; then
  echo "controller binary is %s/%s but target is ${target_os}/${target_arch}; set SUPERDEV_AGENT_BINARY_URL to a matching superdev-agent binary." >&2
  exit 64
fi

tmp="$(mktemp)"
cleanup() { rm -f "$tmp"; }
trap cleanup EXIT
curl -fsSL "$binary_url" -o "$tmp"
chmod +x "$tmp"
if command -v sudo >/dev/null 2>&1; then
  sudo -n install -m 0755 "$tmp" /usr/local/bin/superdev-agent
else
  install -m 0755 "$tmp" /usr/local/bin/superdev-agent
fi
echo "superdev-agent binary installed. Configure your service manager to run:"
echo "  /usr/local/bin/superdev-agent --addr ${BIND_ADDRESS}:${PORT} --require-auth --bootstrap-token ${BOOTSTRAP_TOKEN}"
`, shellQuote(record.HostID), shellQuote(string(record.TransportType)), shellQuote(record.BindAddress), shellQuote(port), shellQuote(record.BootstrapToken), shellQuote(defaultBinaryURL), runtime.GOOS, runtime.GOARCH, runtime.GOOS, runtime.GOARCH)
}
