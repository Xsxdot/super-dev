package api_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/logbuf"
	"github.com/superdev/agent/store"
)

// newTestAppInstance 创建一个直接返回 *api.App 的测试实例，供需要直接操作 App 的测试使用。
// 与 newTestApp 不同，此函数不启动 HTTP Server，由调用方自行 wrap。
func newTestAppInstance(t *testing.T) *api.App {
	t.Helper()
	app, err := api.NewApp(api.AppConfig{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	return app
}

// newTestAppWithConfig 创建使用指定配置的测试 App 和 HTTP server。
//
// 参数：
//   - t: 测试上下文
//   - cfg: App 配置，调用方负责传入 DataDir
//
// 返回：
//   - httptest server
//   - 底层 App 实例
func newTestAppWithConfig(t *testing.T, cfg api.AppConfig) (*httptest.Server, *api.App) {
	t.Helper()
	app, err := api.NewApp(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv, app
}

// addTestDeploymentBackend 直接向 app 注入一个 SQLiteBackend，返回 deployment ID。
// 比创建真实 project 更简单，直接测试 handler 行为。
func addTestDeploymentBackend(t *testing.T, app *api.App) string {
	t.Helper()
	depID := "test-dep-" + t.Name()
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	buf := logbuf.New(s, 100, "")
	t.Cleanup(buf.Close)
	backend := logbackend.NewSQLiteBackend(s, buf)
	app.SetBackendForTest(depID, backend)
	return depID
}
