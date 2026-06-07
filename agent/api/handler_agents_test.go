// handler_agents_test.go 验证 Agent 一等公民 API 的 Host/Agent 边界。
//
// 职责：
//   - 证明 Host DTO 只暴露身份字段
//   - 证明 Agent transport 可以独立于 Host CRUD 更新
//   - 证明 Agent 列表携带 NodeRegistry 运行态快照
//
// 边界：
//   - 不测试具体 transport 连通性
//   - 不覆盖安装动作，安装命令在独立测试文件中验证
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestHostDTOIsIdentityOnly(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	body := bytes.NewBufferString(`{"name":"ali-01","public_ip":"203.0.113.8","private_ip":"10.0.0.8","tags":["prod"]}`)
	resp := httptestDo(t, app, http.MethodPost, "/api/hosts", body)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.NotContains(t, resp.Body.String(), "ssh"+"_host")
	assert.NotContains(t, resp.Body.String(), "remote"+"_agent_port")
	assert.Contains(t, resp.Body.String(), `"public_ip":"203.0.113.8"`)
}

func TestAgentAPIUpdatesTransportForHost(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	var host hostDTO
	require.NoError(t, json.Unmarshal(hostResp.Body.Bytes(), &host))
	require.NotEmpty(t, host.ID)

	payload := `{"transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.8:57017","tls":false}}]}}`
	agentResp := httptestDo(t, app, http.MethodPut, "/api/agents/"+host.ID, bytes.NewBufferString(payload))
	require.Equal(t, http.StatusOK, agentResp.Code)
	assert.Contains(t, agentResp.Body.String(), `"type":"direct"`)
	assert.Contains(t, agentResp.Body.String(), `"address":"100.64.0.8:57017"`)
}

func TestUpdateAgentAcceptsChainAndPreservesToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())

	host, found, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	require.True(t, found)
	host.Agent = &model.Agent{Token: "secret-token", Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHUser: "root", SSHPort: 22, RemoteAgentPort: 57017}},
	}}}
	require.NoError(t, app.remoteStore.UpdateHost(host))

	payload := `{"transport":{"chain":[
	  {"type":"direct","direct":{"address":"100.64.0.8:57017","tls":true,"ca_cert":"PEM"}},
	  {"type":"tunnel","tunnel":{"ssh_host":"10.0.0.8","ssh_port":22,"ssh_user":"root","remote_agent_port":57017}}
	]}}`
	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID, bytes.NewBufferString(payload))
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"chain"`)
	assert.Contains(t, resp.Body.String(), `"token_configured":true`)
	assert.NotContains(t, resp.Body.String(), "secret-token")

	saved, _, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	require.NotNil(t, saved.Agent)
	assert.Equal(t, "secret-token", saved.Agent.Token)
	require.Len(t, saved.Agent.Transport.Chain, 2)
}

func TestUpdateAgentRejectsDuplicateTransportTypes(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	hostID := decodeHostID(t, hostResp.Body.Bytes())
	payload := `{"transport":{"chain":[
	  {"type":"direct","direct":{"address":"100.64.0.8:57017"}},
	  {"type":"direct","direct":{"address":"100.64.0.9:57017"}}
	]}}`

	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID, bytes.NewBufferString(payload))

	require.Equal(t, http.StatusBadRequest, resp.Code)
	assert.Contains(t, resp.Body.String(), "duplicate transport")
}

func TestListAgentsIncludesNodeRuntimeSnapshot(t *testing.T) {
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{})
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeRegistryOverride: reg})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())

	payload := `{"transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.8:57017","tls":false}}]}}`
	agentResp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID, bytes.NewBufferString(payload))
	require.Equal(t, http.StatusOK, agentResp.Code)
	reg.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    hostID,
		Name:      "ali-01",
		Reachable: true,
		Agent: model.AgentRuntime{
			Installed: true,
			Health:    model.AgentHealthHealthy,
			Reachable: true,
		},
	}})

	resp := httptestDo(t, app, http.MethodGet, "/api/agents", nil)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"host_id":"`+hostID+`"`)
	assert.Contains(t, resp.Body.String(), `"health":"healthy"`)
}

func TestLegacyHostAgentRoutesAreNotRegistered(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())

	cases := []struct {
		name string
		path string
		body io.Reader
	}{
		{name: "install", path: "/api/hosts/" + hostID + "/agent/install"},
		{name: "check", path: "/api/hosts/" + hostID + "/agent/check"},
		{name: "uninstall", path: "/api/hosts/" + hostID + "/agent/uninstall", body: bytes.NewBufferString(`{"remove_data":false}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := httptestDo(t, app, http.MethodPost, tc.path, tc.body)
			require.Equal(t, http.StatusNotFound, resp.Code)
		})
	}
}

func httptestDo(t *testing.T, app *App, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	return rr
}

func decodeHostID(t *testing.T, data []byte) string {
	t.Helper()
	var dto hostDTO
	require.NoError(t, json.Unmarshal(data, &dto))
	require.NotEmpty(t, dto.ID)
	return dto.ID
}
