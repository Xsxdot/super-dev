// readiness_test.go 验证本机 Web 入口就绪探测错误语义。
//
// 职责：
//   - 覆盖 readiness timeout 时保留最后一次探测失败原因
//
// 边界：
//   - 不启动真实前端服务
//   - 不访问外部网络
package browserdebug

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestWaitForReadinessReturnsLastProbeReason(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	t.Cleanup(web.Close)

	err := WaitForReadiness(context.Background(), web.URL, model.WebReadinessConfig{Type: model.WebReadinessHTTP, TimeoutSeconds: 1}, web.Client())

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrReadinessTimeout))
	assert.Contains(t, err.Error(), "http status 503")
}

// TestWaitForReadinessProbeSendsAcceptHTML 复现并锁定探针缺陷：
// 前端 dev server 的 SPA history-fallback 只对带 Accept: text/html 的请求把未知深链
// 重写到 index.html 返回 200，否则按静态资源缺失返回 404。探针若不带 Accept 头，
// 会对任意前端路由深链永久探到 404，误判 web_entrypoint_not_ready。
// 该用例用一个模拟此行为的 server：探针必须带 Accept: text/html 才能被判就绪。
func TestWaitForReadinessProbeSendsAcceptHTML(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 仅当请求头 Accept 含 text/html（浏览器整页导航语义）时才回 200，模拟 SPA fallback。
		if strings.Contains(r.Header.Get("Accept"), "text/html") {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(web.Close)

	// 探一条不存在物理文件的前端深链；只有带上 Accept: text/html 的探针才能过。
	targetURL := web.URL + "/nova/replications/v2/rep_v2_regression"
	err := WaitForReadiness(context.Background(), targetURL, model.WebReadinessConfig{Type: model.WebReadinessHTTP, TimeoutSeconds: 1}, web.Client())

	require.NoError(t, err)
}
