// manager_test.go 验证本机浏览器调试 session 管理器。
//
// 职责：
//   - 覆盖打开、列表、查询、关闭 session 的状态变化
//   - 确认打开参数会传递给 launcher
//
// 边界：
//   - 不启动真实浏览器进程
//   - 不访问项目配置
package browserdebug

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerOpenRejectsMissingBrowser(t *testing.T) {
	mgr := NewManager(ManagerOptions{})
	_, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "browser")
}

func TestManagerStoresAndClosesSession(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Close:       func() error { return nil },
			}, nil
		},
	})

	session, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	})
	require.NoError(t, err)
	assert.Equal(t, "dep-1", session.DeploymentID)
	assert.Equal(t, "arc", session.BrowserID)

	list := mgr.List()
	require.Len(t, list, 1)
	require.NoError(t, mgr.Close(session.ID))
	closed, ok := mgr.Get(session.ID)
	require.True(t, ok)
	assert.True(t, closed.Closed)
	assert.WithinDuration(t, time.Now().UTC(), closed.ClosedAt, time.Second)
}

func TestManagerPassesOpenDevtoolsToLauncher(t *testing.T) {
	var gotOpenDevtools bool
	mgr := NewManager(ManagerOptions{
		Launch: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			gotOpenDevtools = req.OpenDevtools
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Close:       func() error { return nil },
			}, nil
		},
	})

	_, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:      BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:       Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL:    "http://127.0.0.1:3000/",
		OpenDevtools: false,
	})
	require.NoError(t, err)

	assert.False(t, gotOpenDevtools)
}
