// Package plugins_test 验证 HTTP 健康检查插件。
//
// 职责：
//   - 验证 http_check 校验 with.url
//   - 验证 expected_status 可通过
//
// 边界：
//   - 不测试真实外部 HTTP 服务
//   - 不调度 DAG
package plugins_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/pipeline/plugins"
)

func TestHTTPCheckRequiresURL(t *testing.T) {
	err := plugins.NewHTTPCheck(nil).Validate(model.Step{With: map[string]interface{}{}})
	require.ErrorContains(t, err, "with.url")
}

func TestHTTPCheckAcceptsExpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	plugin := plugins.NewHTTPCheck(http.DefaultClient)
	step := model.Step{With: map[string]interface{}{"url": server.URL, "expected_status": 204}}
	err := plugin.Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, nil)
	require.NoError(t, err)
}

func TestHTTPCheckPollsUntilReady(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			http.Error(w, "warming", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	check := plugins.NewHTTPCheck(srv.Client())
	step := model.Step{Type: "http_check", With: map[string]interface{}{
		"url": srv.URL, "expected_status": 200, "timeout": "500ms", "interval": "10ms",
	}}
	err := check.Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, nil)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, hits, 3)
}

func TestHTTPCheckUsesTargetAddressForHostPlaceholder(t *testing.T) {
	var gotHost string
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotHost = req.URL.Host
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       http.NoBody,
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}
	check := plugins.NewHTTPCheck(client)
	step := model.Step{Type: "http_check", With: map[string]interface{}{
		"url": "http://${host}:9100/health",
	}}

	err := check.Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, []pipeline.Target{{
		HostID: "host-uuid", HostName: "local-01", HostAddress: "127.0.0.1",
	}})

	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9100", gotHost)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
