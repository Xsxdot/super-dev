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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestHostDTOIncludesSSHFieldsButNoAgent(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	body := bytes.NewBufferString(`{
	  "name":"ali-01",
	  "ssh_host":"10.0.0.8",
	  "ssh_port":22,
	  "ssh_user":"root",
	  "ssh_private_key":"KEY",
	  "tags":[]
	}`)
	resp := httptestDo(t, app, http.MethodPost, "/api/hosts", body)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"ssh_private_key":"KEY"`)
	assert.NotContains(t, resp.Body.String(), `"agent"`)
}

func TestCreateAgentForHostPersistsAgentsJSONAndLeavesHostClean(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{
	  "name":"ali-01",
	  "ssh_host":"10.0.0.8",
	  "ssh_port":22,
	  "ssh_user":"root",
	  "ssh_private_key":"KEY",
	  "tags":[]
	}`))
	require.Equal(t, http.StatusOK, hostResp.Code)
	hostID := decodeHostID(t, hostResp.Body.Bytes())

	body := bytes.NewBufferString(`{
	  "host_id":"` + hostID + `",
	  "transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57017}}]},
	  "config":{"listen_address":"0.0.0.0","listen_port":57017},
	  "security":{"tls":{"mode":"auto"}}
	}`)
	agentResp := httptestDo(t, app, http.MethodPost, "/api/agents", body)
	require.Equal(t, http.StatusOK, agentResp.Code)
	assert.Contains(t, agentResp.Body.String(), `"host_id":"`+hostID+`"`)
	assert.Contains(t, agentResp.Body.String(), `"mode":"auto"`)
	assert.NotContains(t, agentResp.Body.String(), "KEY")

	rawHosts, err := os.ReadFile(filepath.Join(dataDir, "hosts.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(rawHosts), `"agent"`)

	rawAgents, err := os.ReadFile(filepath.Join(dataDir, "agents.json"))
	require.NoError(t, err)
	assert.Contains(t, string(rawAgents), `"host_id": "`+hostID+`"`)
}

func TestUpdateAgentTransportDoesNotOverwriteUnifiedConfigOrSecret(t *testing.T) {
	dataDir := t.TempDir()
	app, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID:   host.ID,
		Config:   model.AgentConfig{ListenAddress: "0.0.0.0", ListenPort: 57017},
		Security: model.AgentSecurity{TLS: model.AgentTLSSpec{Mode: model.AgentTLSModeManual, CACert: "PEM"}},
		Secret:   model.AgentSecret{Token: "secret-token"},
	})
	require.NoError(t, err)

	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+host.ID+"/transport", bytes.NewBufferString(`{
	  "transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.8:57017"}}]}
	}`))
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"token_configured":true`)
	assert.Contains(t, resp.Body.String(), `"mode":"manual"`)
	assert.NotContains(t, resp.Body.String(), "secret-token")
}

func TestUpdateAgentRejectsDuplicateTransportTypes(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	hostResp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{"name":"ali-01","tags":[]}`))
	hostID := decodeHostID(t, hostResp.Body.Bytes())
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: hostID, Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}})
	require.NoError(t, err)
	payload := `{"transport":{"chain":[
	  {"type":"direct","direct":{"address":"100.64.0.8:57017"}},
	  {"type":"direct","direct":{"address":"100.64.0.9:57017"}}
	]}}`

	resp := httptestDo(t, app, http.MethodPut, "/api/agents/"+hostID+"/transport", bytes.NewBufferString(payload))

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

	payload := `{"host_id":"` + hostID + `","transport":{"chain":[{"type":"direct","direct":{"address":"100.64.0.8:57017"}}]},"security":{"tls":{"mode":"off"}}}`
	agentResp := httptestDo(t, app, http.MethodPost, "/api/agents", bytes.NewBufferString(payload))
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
