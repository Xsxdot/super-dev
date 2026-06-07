// agent_health_prober_test.go 验证远端 agent 探活器的 endpoint 兼容性判定。
//
// 职责：
//   - 用 httptest 模拟远端 agent 的必需接口响应
//   - 验证接口齐全、接口缺失、baseURL 不可达三类探活结果
//
// 边界：
//   - 不建立真实 SSH 隧道
//   - 不测试 Monitor 的状态归类逻辑
package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type agentHealthTestTransport struct {
	roundTrip apiRoundTripFunc
	err       error
}

func (t agentHealthTestTransport) Do(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if t.err != nil {
		return nodetransport.NodeResponse{}, t.err
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, "http://agent.local"+req.Path, req.Body)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	resp, err := t.roundTrip(httpReq)
	if err != nil {
		return nodetransport.NodeResponse{}, err
	}
	return nodetransport.NodeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, Body: resp.Body}, nil
}

func (t agentHealthTestTransport) Stream(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t agentHealthTestTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t agentHealthTestTransport) Covers() []string {
	return []string{"h1"}
}

func TestAgentHealthProberAllEndpointsOK(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == nodetransport.SecurityHealthPath:
			return agentHealthProbeJSONResponse(http.StatusOK, `{"version":"0.1.0","provision_state":"provisioned"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/hosts":
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/tunnels":
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/pipeline/templates/builtin/go-binary-build":
			assert.Equal(t, "version=1.0.0", r.URL.RawQuery)
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/exec/health":
			return agentHealthProbeJSONResponse(http.StatusOK, `{"version":"0.1.0"}`), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/managed-deployments/status":
			return agentHealthProbeJSONResponse(http.StatusOK, `{"deployment_count":0,"collector_count":0,"collectors":[]}`), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/transfer":
			return agentHealthProbeResponse(http.StatusBadRequest), nil
		default:
			return agentHealthProbeResponse(http.StatusNotFound), nil
		}
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.True(t, res.AllEndpointsOK)
	assert.Equal(t, "0.1.0", res.Version)
}

func TestAgentHealthProberReturnsPendingBootstrapFromSecurityHealth(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == nodetransport.SecurityHealthPath {
			return agentHealthProbeJSONResponse(http.StatusOK, `{"version":"0.1.0","provision_state":"pending-bootstrap"}`), nil
		}
		return agentHealthProbeResponse(http.StatusOK), nil
	})

	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.Equal(t, agenthealth.StatusPendingBootstrap, res.Status)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberReturnsAuthFailedOnProtectedEndpoint401(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == nodetransport.SecurityHealthPath {
			return agentHealthProbeJSONResponse(http.StatusOK, `{"version":"0.1.0","provision_state":"provisioned"}`), nil
		}
		return agentHealthProbeResponse(http.StatusUnauthorized), nil
	})

	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.Equal(t, agenthealth.StatusAuthFailed, res.Status)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberMissingEndpoint(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/tunnels" && r.Method == http.MethodGet {
			return agentHealthProbeResponse(http.StatusNotFound), nil
		}
		return agentHealthProbeResponse(statusForKnownAgentHealthEndpoint(r)), nil
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberMissingTemplateEndpoint(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/pipeline/templates/builtin/go-binary-build" {
			assert.Equal(t, "version=1.0.0", r.URL.RawQuery)
			return agentHealthProbeResponse(http.StatusNotFound), nil
		}
		return agentHealthProbeResponse(statusForKnownAgentHealthEndpoint(r)), nil
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberMissingExecEndpointIsVersionMismatch(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/exec/health" && r.Method == http.MethodGet {
			return agentHealthProbeResponse(http.StatusNotFound), nil
		}
		return agentHealthProbeResponse(statusForKnownAgentHealthEndpoint(r)), nil
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberAcceptsOldNoContentExecHealth(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/exec/health" && r.Method == http.MethodGet {
			return agentHealthProbeResponse(http.StatusNoContent), nil
		}
		return agentHealthProbeResponse(statusForKnownAgentHealthEndpoint(r)), nil
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.True(t, res.AllEndpointsOK)
	assert.Empty(t, res.Version)
}

func TestAgentHealthProberUnreachableWhenTransportCannotReachHost(t *testing.T) {
	p := newAgentHealthProber(agentHealthTestTransport{err: errors.New("no tunnel")})
	_, err := p.Probe(context.Background(), "h1")
	assert.Error(t, err)
}

type apiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f apiRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func agentHealthProberWithRoundTrip(fn apiRoundTripFunc) *agentHealthProber {
	return newAgentHealthProber(agentHealthTestTransport{roundTrip: fn})
}

func agentHealthProbeResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

func agentHealthProbeJSONResponse(status int, body string) *http.Response {
	resp := agentHealthProbeResponse(status)
	resp.Body = io.NopCloser(bytes.NewReader([]byte(body)))
	resp.Header.Set("Content-Type", "application/json")
	return resp
}

func statusForKnownAgentHealthEndpoint(r *http.Request) int {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/hosts":
		return http.StatusOK
	case r.Method == http.MethodGet && r.URL.Path == "/api/tunnels":
		return http.StatusOK
	case r.Method == http.MethodGet && r.URL.Path == "/api/pipeline/templates/builtin/go-binary-build":
		return http.StatusOK
	case r.Method == http.MethodGet && r.URL.Path == "/api/exec/health":
		return http.StatusNoContent
	case r.Method == http.MethodGet && r.URL.Path == "/api/managed-deployments/status":
		return http.StatusOK
	case r.Method == http.MethodPost && r.URL.Path == "/api/transfer":
		return http.StatusBadRequest
	default:
		return http.StatusNotFound
	}
}
