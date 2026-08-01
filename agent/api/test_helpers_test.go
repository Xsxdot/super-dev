package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

type testStoreWriter struct {
	s *store.Store
}

func (w testStoreWriter) AppendBatch(_ context.Context, entries []model.LogEntry) error {
	return w.s.AppendBatch(entries)
}

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
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	return srv, app
}

// testServerHandler 包一层"未带 Authorization 时默认注入本机 token"，供本包内
// 真实 httptest.Server 网络往返测试使用。语义同 package api 内部同名 helper：
// 真实 http.Client 请求没有"显式传空值"的概念，没带就视为需要默认凭据；
// 想验证拒绝路径的用例需自行设置一个非空但无效的 Authorization 覆盖默认值。
func testServerHandler(app *api.App) http.Handler {
	next := app.Handler()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
		}
		next.ServeHTTP(w, r)
	})
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
	buf := logbuf.New(testStoreWriter{s: s}, 100, "", nil)
	t.Cleanup(buf.Close)
	backend := logbackend.NewSQLiteBackend(s, buf)
	app.SetBackendForTest(depID, backend)
	return depID
}
