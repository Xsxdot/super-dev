// package_test_helpers_test.go 提供 api 包内部测试共享 helper。
//
// 职责：
//   - 创建 package api 测试用 App
//   - 创建绑定 App.Handler 的 httptest.Server
//
// 边界：
//   - 不提供 package api_test 的黑盒测试 helper
//   - 不注入业务数据，调用方自行准备项目、host、日志等状态
package api

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func newTestAppForPackage(t *testing.T) *App {
	t.Helper()
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
}

func newHTTPServerForPackage(t *testing.T, app *App) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}
