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
