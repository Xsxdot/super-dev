// agent_install_command_test.go 验证 Agent generated-command 安装动作。
//
// 职责：
//   - 证明安装命令由后端生成并绑定 host/端口/监听地址
//   - 证明响应只暴露 token_id 与过期时间，不回传明文 token 字段
//
// 边界：
//   - 不执行 curl/bash 命令
//   - 不测试安装脚本兑换 token 的后续流程
package api

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestGenerateAgentInstallCommandBindsHostAndParameters(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: hostID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeTunnel,
			Tunnel: &model.TunnelParams{RemoteAgentPort: 57017},
		}}},
		Config:   model.AgentConfig{ListenAddress: "127.0.0.1", ListenPort: 57017},
		Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeOff}},
	})
	require.NoError(t, err)

	body := `{"method":"generated_command","controller_url":"http://100.64.0.10:57017","transport_type":"tunnel","token_ttl_minutes":30}`
	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install-command", bytes.NewBufferString(body))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "curl -fsSL")
	assert.Contains(t, resp.Body.String(), "--host-id "+hostID)
	assert.Contains(t, resp.Body.String(), "--bind-address 127.0.0.1")
	assert.Contains(t, resp.Body.String(), "--port 57017")
	assert.Contains(t, resp.Body.String(), "--bootstrap-token")
	assert.Contains(t, resp.Body.String(), "--require-auth")
	assert.Contains(t, resp.Body.String(), `"token_id"`)
	assert.Contains(t, resp.Body.String(), `"expires_at"`)
	assert.NotContains(t, resp.Body.String(), `"token":"`)
	assert.NotContains(t, resp.Body.String(), `"bootstrap_token"`)
}

func TestGenerateAgentInstallCommandDefaultsBindAddressFromDirectChain(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: hostID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeDirect,
			Direct: &model.DirectParams{Address: "100.117.127.123:57021"},
		}}},
		Config:   model.AgentConfig{ListenAddress: "100.117.127.123", ListenPort: 57021},
		Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeAuto}},
	})
	require.NoError(t, err)

	body := `{"method":"generated_command","controller_url":"http://100.64.0.10:57017","transport_type":"direct","token_ttl_minutes":30}`
	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install-command", bytes.NewBufferString(body))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "--bind-address 0.0.0.0")
	assert.Contains(t, resp.Body.String(), "--port 57021")
	assert.NotContains(t, resp.Body.String(), "--bind-address 100.117.127.123")
}

func TestGenerateAgentInstallCommandDefaultsBindAddressFromTunnelOnlyChain(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: hostID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{{
			Type:   model.TransportTypeTunnel,
			Tunnel: &model.TunnelParams{RemoteAgentPort: 57021},
		}}},
		Config:   model.AgentConfig{ListenAddress: "100.117.127.123", ListenPort: 57021},
		Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeAuto}},
	})
	require.NoError(t, err)

	body := `{"method":"generated_command","controller_url":"http://100.64.0.10:57017","transport_type":"tunnel","token_ttl_minutes":30}`
	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install-command", bytes.NewBufferString(body))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "--bind-address 127.0.0.1")
	assert.Contains(t, resp.Body.String(), "--port 57021")
	assert.NotContains(t, resp.Body.String(), "--bind-address 100.117.127.123")
}

func TestAgentInstallCommandTokenRecordBindsHostAndTTL(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)

	result, err := generateAgentInstallCommand("h1", agentInstallCommandRequest{
		ControllerURL: "http://100.64.0.10:57017",
		TransportType: model.TransportTypeTunnel,
	}, now)

	require.NoError(t, err)
	assert.Equal(t, now.Add(30*time.Minute).Format(time.RFC3339), result.Response.ExpiresAt)
	assert.Equal(t, "h1", result.Token.HostID)
	assert.Equal(t, model.TransportTypeTunnel, result.Token.TransportType)
	assert.Equal(t, "127.0.0.1", result.Token.BindAddress)
	assert.Equal(t, 57017, result.Token.RemoteAgentPort)
	assert.Equal(t, now.Add(30*time.Minute), result.Token.ExpiresAt)
	assert.NotEmpty(t, result.Token.BootstrapToken)
	assert.NotContains(t, result.Response.Command, result.Token.TokenHash)

	token := extractInstallToken(t, result.Response.Command)
	assert.NotEmpty(t, token)
	assert.Equal(t, hashAgentInstallToken(token), result.Token.TokenHash)
	assert.NotEqual(t, token, result.Token.TokenHash)
}

func TestGenerateAgentInstallCommandAcceptsTransportTypeAfterChainValidation(t *testing.T) {
	now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)

	result, err := generateAgentInstallCommand("h1", agentInstallCommandRequest{
		ControllerURL: "http://100.64.0.10:57017",
		TransportType: model.TransportTypeDirect,
	}, now)

	require.NoError(t, err)
	assert.Equal(t, model.TransportTypeDirect, result.Token.TransportType)
	assert.Contains(t, result.Response.Command, "--host-id h1")
}

func TestAgentInstallScriptRouteValidatesInstallToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()
	result, err := generateAgentInstallCommand("h1", agentInstallCommandRequest{
		ControllerURL: "http://100.64.0.10:57017",
		TransportType: model.TransportTypeDirect,
	}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)
	token := extractInstallToken(t, result.Response.Command)

	okResp := httptestDo(t, app, http.MethodGet, "/api/agents/install.sh?token="+url.QueryEscape(token), nil)
	require.Equal(t, http.StatusOK, okResp.Code)
	assert.Contains(t, okResp.Body.String(), "--require-auth")
	assert.Contains(t, okResp.Body.String(), "--bootstrap-token")
	assert.Contains(t, okResp.Body.String(), "/api/agents/install-binary?token=")

	badResp := httptestDo(t, app, http.MethodGet, "/api/agents/install.sh?token=wrong", nil)
	assert.Equal(t, http.StatusUnauthorized, badResp.Code)
}

func TestAgentInstallBinaryRouteValidatesInstallToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), RequireAuth: true})
	require.NoError(t, err)
	defer app.Close()
	result, err := generateAgentInstallCommand("h1", agentInstallCommandRequest{
		ControllerURL: "http://100.64.0.10:57017",
		TransportType: model.TransportTypeDirect,
	}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)
	token := extractInstallToken(t, result.Response.Command)

	okResp := httptestDo(t, app, http.MethodGet, "/api/agents/install-binary?token="+url.QueryEscape(token), nil)
	require.Equal(t, http.StatusOK, okResp.Code)
	assert.Equal(t, "application/octet-stream", okResp.Header().Get("Content-Type"))
	assert.NotEmpty(t, okResp.Body.Bytes())

	badResp := httptestDo(t, app, http.MethodGet, "/api/agents/install-binary?token=wrong", nil)
	assert.Equal(t, http.StatusUnauthorized, badResp.Code)
}

func extractInstallToken(t *testing.T, command string) string {
	t.Helper()
	const marker = "install.sh?token="
	start := strings.Index(command, marker)
	require.NotEqual(t, -1, start)
	rest := command[start+len(marker):]
	end := strings.Index(rest, "'")
	require.NotEqual(t, -1, end)
	token, err := url.QueryUnescape(rest[:end])
	require.NoError(t, err)
	return token
}
