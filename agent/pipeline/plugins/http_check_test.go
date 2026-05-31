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

	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
	"github.com/superdev/agent/pipeline/plugins"
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
