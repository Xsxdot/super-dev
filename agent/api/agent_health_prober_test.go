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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newAgentHealthProber(staticResolver{base: srv.URL})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.True(t, res.AllEndpointsOK)
}

func TestAgentHealthProberMissingEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tunnels" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newAgentHealthProber(staticResolver{base: srv.URL})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberMissingTemplateEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pipeline/templates/builtin/go-binary-build" {
			assert.Equal(t, "version=1.0.0", r.URL.RawQuery)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newAgentHealthProber(staticResolver{base: srv.URL})
	res, err := p.Probe(context.Background(), "h1")

	require.NoError(t, err)
	assert.False(t, res.AllEndpointsOK)
}

func TestAgentHealthProberUnreachableWhenNoBaseURL(t *testing.T) {
	p := newAgentHealthProber(staticResolver{err: errors.New("no tunnel")})
	_, err := p.Probe(context.Background(), "h1")
	assert.Error(t, err)
}
