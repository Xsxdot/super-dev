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

	body := `{"method":"generated_command","controller_url":"http://100.64.0.10:57017","bind_address":"127.0.0.1","remote_agent_port":57017,"transport_type":"tunnel","token_ttl_minutes":30}`
	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+hostID+"/install-command", bytes.NewBufferString(body))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "curl -fsSL")
	assert.Contains(t, resp.Body.String(), "--host-id "+hostID)
	assert.Contains(t, resp.Body.String(), "--bind-address 127.0.0.1")
	assert.Contains(t, resp.Body.String(), "--port 57017")
	assert.Contains(t, resp.Body.String(), `"token_id"`)
	assert.Contains(t, resp.Body.String(), `"expires_at"`)
	assert.NotContains(t, resp.Body.String(), `"token":"`)
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

	token := extractInstallToken(t, result.Response.Command)
	assert.NotEmpty(t, token)
	assert.Equal(t, hashAgentInstallToken(token), result.Token.TokenHash)
	assert.NotEqual(t, token, result.Token.TokenHash)
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
