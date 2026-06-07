package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestTestAgentTransportReturnsProbeResult(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, nodetransport.SecurityHealthPath, r.URL.Path)
		jsonOK(w, nodetransport.SecurityHealthResponse{Version: "0.1.0", ProvisionState: "pending-bootstrap"})
	}))
	defer remote.Close()
	tr := testNodeTransport{table: map[string]string{"h1": remote.URL}}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "ali", Agent: &model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}}})
	require.NoError(t, err)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/transports/test", bytes.NewBufferString(`{"index":0}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"status"`)
	assert.Contains(t, resp.Body.String(), `"transport_type":"direct"`)
}

func TestTestAgentTransportUsesRequestedEntryProvider(t *testing.T) {
	direct := &namedProbeTransport{healthState: "pending-bootstrap"}
	tunnel := &namedProbeTransport{healthState: "provisioned"}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tunnel})
	require.NoError(t, err)
	defer app.Close()
	app.nodeTransportProviders = map[model.TransportType]nodetransport.NodeTransport{
		model.TransportTypeDirect: direct,
		model.TransportTypeTunnel: tunnel,
	}

	host, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "ali", Agent: &model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{SSHHost: "10.0.0.8", SSHUser: "root", RemoteAgentPort: 57017}},
	}}}})
	require.NoError(t, err)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/transports/test", bytes.NewBufferString(`{"index":0}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"status":"pending-bootstrap"`)
	assert.Equal(t, 1, direct.calls)
	assert.Equal(t, 0, tunnel.calls)
}

func TestProvisionAgentPersistsGeneratedTokenBeforeRemoteCall(t *testing.T) {
	tr := &provisionRecorderTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali", Agent: &model.Agent{Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}}})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeDirect}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"off"}`))
	require.Equal(t, http.StatusOK, resp.Code)

	saved, found, err := app.remoteHostByID(host.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, saved.Agent)
	assert.NotEmpty(t, saved.Agent.Token)
	assert.Equal(t, "Bearer "+result.Token.BootstrapToken, tr.authorization)
}

func TestProvisionAgentReusesSavedTokenAndSendsDirectHostForAutoTLS(t *testing.T) {
	tr := &provisionRecorderTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali", Agent: &model.Agent{
		Token: "saved-long-token",
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "https://100.64.0.8:57019"}},
		}},
	}})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeDirect}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "saved-long-token", tr.provisionToken)
	assert.Equal(t, []string{"100.64.0.8"}, tr.provisionHosts)
}

type namedProbeTransport struct {
	healthState string
	calls       int
}

func (p *namedProbeTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	p.calls++
	if req.Path == nodetransport.SecurityHealthPath {
		return nodetransport.NodeResponse{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"version":"0.1.0","provision_state":"` + p.healthState + `"}`)),
		}, nil
	}
	return nodetransport.NodeResponse{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"version":"0.1.0"}`)),
	}, nil
}

func (p *namedProbeTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (p *namedProbeTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (p *namedProbeTransport) Covers() []string { return []string{"h1"} }

type provisionRecorderTransport struct {
	authorization  string
	provisionToken string
	provisionHosts []string
}

func (p *provisionRecorderTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if req.Path == "/api/security/provision" {
		p.authorization = req.Headers.Get("Authorization")
		var body struct {
			Token string   `json:"token"`
			Hosts []string `json:"hosts"`
		}
		_ = json.NewDecoder(req.Body).Decode(&body)
		p.provisionToken = body.Token
		p.provisionHosts = body.Hosts
		return nodetransport.NodeResponse{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"provision_state":"provisioned","tls_mode":"off"}`)),
		}, nil
	}
	return nodetransport.NodeResponse{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"version":"0.1.0","provision_state":"pending-bootstrap"}`)),
	}, nil
}

func (p *provisionRecorderTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (p *provisionRecorderTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (p *provisionRecorderTransport) Covers() []string { return []string{"h1"} }
