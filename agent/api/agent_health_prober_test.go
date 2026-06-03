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
)

// staticResolver 让 prober 指向一个本地 httptest server。
type staticResolver struct {
	base string
	err  error
}

func (s staticResolver) BaseURL(hostID string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.base, nil
}

func TestAgentHealthProberAllEndpointsOK(t *testing.T) {
	p := agentHealthProberWithRoundTrip(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/hosts":
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/tunnels":
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/pipeline/templates/builtin/go-binary-build":
			assert.Equal(t, "version=1.0.0", r.URL.RawQuery)
			return agentHealthProbeResponse(http.StatusOK), nil
		case r.Method == http.MethodGet && r.URL.Path == "/api/exec/health":
			return agentHealthProbeResponse(http.StatusNoContent), nil
		case r.Method == http.MethodPost && r.URL.Path == "/api/transfer":
			return agentHealthProbeResponse(http.StatusBadRequest), nil
		default:
			return agentHealthProbeResponse(http.StatusNotFound), nil
		}
	})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.True(t, res.AllEndpointsOK)
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

func TestAgentHealthProberUnreachableWhenNoBaseURL(t *testing.T) {
	p := newAgentHealthProber(staticResolver{err: errors.New("no tunnel")})
	_, err := p.Probe(context.Background(), "h1")
	assert.Error(t, err)
}

type apiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f apiRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func agentHealthProberWithRoundTrip(fn apiRoundTripFunc) *agentHealthProber {
	p := newAgentHealthProber(staticResolver{base: "http://agent.local"})
	p.client = &http.Client{Transport: fn}
	return p
}

func agentHealthProbeResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
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
	case r.Method == http.MethodPost && r.URL.Path == "/api/transfer":
		return http.StatusBadRequest
	default:
		return http.StatusNotFound
	}
}
