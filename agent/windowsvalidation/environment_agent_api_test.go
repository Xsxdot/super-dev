// environment_agent_api_test.go 锁定 preflight 对现有 Agent HTTP API 的安全只读投影。
//
// 职责：
//   - 证明正式 /api/agents 与 /api/tunnels schema 能产生 Agent/tunnel readiness
//   - 拒绝 URL 凭据、query/fragment 与跨 origin redirect
//
// 边界：
//   - 仅使用 httptest，不访问真实 Agent、不建立 tunnel，也不保存响应中的敏感字段
package windowsvalidation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPEnvironmentAgentAPIReaderProjectsOfficialReadOnlySchemas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		assert.Equal(t, http.MethodGet, request.Method)
		switch request.URL.Path {
		case "/api/agents":
			_, _ = response.Write([]byte(`[{"host_id":"linux-01","transport":{"chain":[{"type":"tunnel","tunnel":{"remote_agent_port":57017}}]},"config":{"listen_address":"127.0.0.1","listen_port":57017},"runtime":{"installed":true,"version":"0.2.1","health":"healthy","reachable":true},"security":{"provision_state":"provisioned","token_configured":true,"tls":{"mode":"auto","ca_cert":"must-not-project"},"token":"must-not-project"}}]`))
		case "/api/tunnels":
			_, _ = response.Write([]byte(`[{"host_id":"linux-01","state":"open","local_port":49231,"error":"must-not-project"}]`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	reader, err := NewHTTPEnvironmentAgentAPIReader(server.URL, server.Client())
	require.NoError(t, err)
	agents, err := reader.ListEnvironmentAgents(context.Background())
	require.NoError(t, err)
	tunnels, err := reader.ListEnvironmentTunnels(context.Background())
	require.NoError(t, err)

	require.Len(t, agents, 1)
	assert.Equal(t, EnvironmentAgentObservation{
		HostID: "linux-01", Installed: true, Reachable: true, Health: "healthy",
		Version: "0.2.1", ProvisionState: "provisioned", ListenAddress: "127.0.0.1", ListenPort: 57017,
		TokenConfigured: true, TLSMode: "auto", Transports: []string{"tunnel"}, TunnelRemoteAgentPort: 57017,
	}, agents[0])
	require.Len(t, tunnels, 1)
	assert.Equal(t, EnvironmentTunnelObservation{HostID: "linux-01", State: "open"}, tunnels[0])
	projected := CanonicalJSON(map[string]any{"agents": agents, "tunnels": tunnels})
	assert.NotContains(t, projected, "must-not-project")
	assert.NotContains(t, projected, "49231")
}

func TestHTTPEnvironmentAgentAPIReaderRejectsCredentialBearingURLAndCrossOriginRedirect(t *testing.T) {
	for _, value := range []string{
		"http://user:secret@127.0.0.1:57017",
		"http://127.0.0.1:57017?token=secret",
		"http://127.0.0.1:57017#secret",
	} {
		_, err := NewHTTPEnvironmentAgentAPIReader(value, nil)
		assert.Error(t, err)
	}

	target := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`[]`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Redirect(response, request, target.URL+"/api/agents", http.StatusFound)
	}))
	defer origin.Close()

	reader, err := NewHTTPEnvironmentAgentAPIReader(origin.URL, origin.Client())
	require.NoError(t, err)
	_, err = reader.ListEnvironmentAgents(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redirects are forbidden")
}
