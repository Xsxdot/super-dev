package api_test

import (
	"testing"

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
