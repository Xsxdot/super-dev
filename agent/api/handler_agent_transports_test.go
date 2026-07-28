package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

	host, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}})
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

	host, err := app.remoteStore.AddHost(model.Host{ID: "h1", Name: "ali", SSHHost: "10.0.0.8", SSHUser: "root"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
		{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
	}}})
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

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeDirect}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"off"}`))
	require.Equal(t, http.StatusOK, resp.Code)

	saved, found, err := app.agentStore.AgentByHostID(host.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.NotEmpty(t, saved.Secret.Token)
	assert.Equal(t, "Bearer "+result.Token.BootstrapToken, tr.authorization)
}

func TestProvisionAgentReusesSavedTokenAndSendsDirectHostForAutoTLS(t *testing.T) {
	tr := &provisionRecorderTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Secret: model.AgentSecret{Token: "saved-long-token"},
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "https://100.64.0.8:57019"}},
		}},
	})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeDirect}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "saved-long-token", tr.provisionToken)
	assert.Equal(t, []string{"100.64.0.8"}, tr.provisionHosts)
}

func TestProvisionAgentSendsLoopbackHostForTunnelAutoTLS(t *testing.T) {
	tr := &provisionRecorderTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali", SSHHost: "10.0.0.8", SSHUser: "root"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
		}},
	})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeTunnel}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []string{"127.0.0.1"}, tr.provisionHosts)
}

func TestProvisionAgentSendsAllChainHostsForAutoTLS(t *testing.T) {
	tr := &provisionRecorderTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali", SSHHost: "10.0.0.8", SSHUser: "root"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "https://100.90.99.61:57017"}},
			{Type: model.TransportTypeTunnel, Tunnel: &model.TunnelParams{RemoteAgentPort: 57017}},
		}},
	})
	require.NoError(t, err)
	result, err := generateAgentInstallCommand(host.ID, agentInstallCommandRequest{ControllerURL: "http://127.0.0.1:57017", TransportType: model.TransportTypeDirect}, time.Now().UTC())
	require.NoError(t, err)
	app.rememberAgentInstallToken(result.Token)

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []string{"100.90.99.61", "127.0.0.1"}, tr.provisionHosts)
}

func TestProvisionAgentUsesPlainHTTPBeforeAutoTLSIsProvisioned(t *testing.T) {
	var provisionCalled bool
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/security/provision" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		provisionCalled = true
		assert.Equal(t, "Bearer bootstrap", r.Header.Get("Authorization"))
		var body struct {
			TLSMode string `json:"tls_mode"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "auto", body.TLSMode)
		jsonOK(w, map[string]any{
			"provision_state":  "provisioned",
			"tls_mode":         "auto",
			"ca_cert":          "PEM",
			"restart_required": true,
		})
	}))
	defer remote.Close()

	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: strings.TrimPrefix(remote.URL, "http://")}},
		}},
		Security: model.AgentSecurity{
			ProvisionState: model.AgentProvisionStatePendingBootstrap,
			TLS:            model.AgentTLSSpec{Mode: model.AgentTLSModeAuto},
		},
	})
	require.NoError(t, err)
	app.rememberAgentInstallToken(agentInstallTokenRecord{
		HostID:         host.ID,
		BootstrapToken: "bootstrap",
		ExpiresAt:      time.Now().Add(time.Minute),
	})

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusOK, resp.Code)
	assert.True(t, provisionCalled)
	assert.Contains(t, resp.Body.String(), `"restart_required":true`)
}

// 真机故障复现：agent 侧 provision 成功（状态已改、bootstrap 已焚毁），
// 但响应在回程丢失（隧道被 restart_required 触发的重启打断 → EOF）。
// 桌面端必须已经把长期 token 落盘，重试时复用同一个 token 命中 agent 的幂等分支；
// 否则重试会换一个新 token，只能拿到 bootstrap 已焚毁的 401，陷入死循环。
func TestProvisionAgentReusesPersistedTokenAfterLostResponse(t *testing.T) {
	var mu sync.Mutex
	var attempts int
	var seenTokens []string
	var provisionedToken string

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/security/provision" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Token string `json:"token"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		mu.Lock()
		attempts++
		current := attempts
		seenTokens = append(seenTokens, body.Token)
		if current == 1 {
			// 第一次：agent 侧成功落库并焚毁 bootstrap，但响应丢失。
			provisionedToken = body.Token
		}
		alreadyProvisioned := provisionedToken != "" && body.Token == provisionedToken
		mu.Unlock()

		if current == 1 {
			// 模拟隧道在响应回程中断：直接劫持连接关掉，客户端得到 EOF。
			conn, _, err := w.(http.Hijacker).Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		if !alreadyProvisioned {
			// token 变了 → bootstrap 已焚毁，agent 只能拒绝。这就是死循环。
			jsonError(w, http.StatusUnauthorized, "bootstrap token rejected")
			return
		}
		// 幂等分支：同一个 token 重放，返回与首次相同的结果。
		jsonOK(w, map[string]any{
			"provision_state":  "provisioned",
			"tls_mode":         "auto",
			"ca_cert":          "PEM",
			"restart_required": true,
		})
	}))
	defer remote.Close()

	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "sing-box-01"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{
		HostID: host.ID,
		Transport: model.TransportConfig{Chain: []model.TransportEntry{
			{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: strings.TrimPrefix(remote.URL, "http://")}},
		}},
		Security: model.AgentSecurity{
			ProvisionState: model.AgentProvisionStatePendingBootstrap,
			TLS:            model.AgentTLSSpec{Mode: model.AgentTLSModeAuto},
		},
	})
	require.NoError(t, err)
	app.rememberAgentInstallToken(agentInstallTokenRecord{
		HostID:         host.ID,
		BootstrapToken: "bootstrap",
		ExpiresAt:      time.Now().Add(time.Minute),
	})

	// 第一次：响应丢失，桌面端看到传输层错误。
	first := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))
	assert.Equal(t, http.StatusBadGateway, first.Code)

	// 关键断言：即便响应丢了，长期 token 也必须已经落盘。
	stored, ok, err := app.agentStore.AgentByHostID(host.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEmpty(t, stored.Secret.Token, "长期 token 必须在发请求前落盘，否则重试无法命中幂等分支")
	// 此时还不能声称已完成下发，否则 UI 谎报成功、后续请求还会误用 TLS。
	assert.Equal(t, model.AgentProvisionStatePendingBootstrap, stored.Security.ProvisionState)
	assert.False(t, stored.Security.TokenConfigured)

	// 第二次：重试必须复用同一 token 并成功恢复。
	second := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))
	require.Equal(t, http.StatusOK, second.Code)
	assert.Contains(t, second.Body.String(), `"restart_required":true`)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, seenTokens, 2)
	assert.Equal(t, seenTokens[0], seenTokens[1], "重试必须复用同一个长期 token")

	final, ok, err := app.agentStore.AgentByHostID(host.ID)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, model.AgentProvisionStateProvisioned, final.Security.ProvisionState)
	assert.True(t, final.Security.TokenConfigured)
	assert.Equal(t, seenTokens[0], final.Secret.Token)
	assert.Equal(t, "PEM", final.Security.TLS.CACert)
}

func TestProvisionAgentIncludesRemoteProvisionErrorBody(t *testing.T) {
	tr := &provisionFailureTransport{}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	defer app.Close()

	host, err := app.remoteStore.AddHost(model.Host{Name: "ali"})
	require.NoError(t, err)
	_, err = app.agentStore.UpsertAgent(model.Agent{HostID: host.ID, Transport: model.TransportConfig{Chain: []model.TransportEntry{
		{Type: model.TransportTypeDirect, Direct: &model.DirectParams{Address: "100.64.0.8:57017"}},
	}}})
	require.NoError(t, err)
	app.rememberAgentInstallToken(agentInstallTokenRecord{
		HostID:         host.ID,
		BootstrapToken: "bootstrap",
		ExpiresAt:      time.Now().Add(time.Minute),
	})

	resp := httptestDo(t, app, http.MethodPost, "/api/agents/"+host.ID+"/provision", bytes.NewBufferString(`{"index":0,"tls_mode":"auto"}`))

	require.Equal(t, http.StatusBadGateway, resp.Code)
	assert.Contains(t, resp.Body.String(), "remote provision failed (401): bootstrap token rejected")
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

type provisionFailureTransport struct{}

func (p *provisionFailureTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	return nodetransport.NodeResponse{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(`{"error":"bootstrap token rejected"}`)),
	}, nil
}

func (p *provisionFailureTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (p *provisionFailureTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (p *provisionFailureTransport) Covers() []string { return []string{"h1"} }
