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
	"os"
	"path/filepath"
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
	_, ok := mgr.Get(session.ID)
	assert.False(t, ok)
}

func TestManagerOpenReusesMatchingAliveSession(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	launches := 0
	mgr := NewManager(ManagerOptions{
		Now: func() time.Time { return now },
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			launches++
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Alive:       func() bool { return true },
				Close:       func() error { return nil },
			}, nil
		},
	})
	req := OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	}
	first, err := mgr.Open(context.Background(), req)
	require.NoError(t, err)

	now = now.Add(time.Minute)
	second, err := mgr.Open(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, 1, launches)
	assert.Equal(t, now, second.LastUsedAt)
}

func TestManagerOpenUsesNewSessionForDifferentTargetURL(t *testing.T) {
	launches := 0
	mgr := NewManager(ManagerOptions{
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			launches++
			return LaunchResult{
				ProcessID:   123 + launches,
				DebugPort:   9222 + launches,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Alive:       func() bool { return true },
				Close:       func() error { return nil },
			}, nil
		},
	})
	base := OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	}
	first, err := mgr.Open(context.Background(), base)
	require.NoError(t, err)
	next := base
	next.TargetURL = "http://127.0.0.1:3000/users"

	second, err := mgr.Open(context.Background(), next)

	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID)
	assert.Equal(t, 2, launches)
}

func TestManagerFindReusableMatchesLiveSessionByDeploymentAndBrowser(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	mgr := NewManager(ManagerOptions{
		Now: func() time.Time { return now },
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Alive:       func() bool { return true },
				Close:       func() error { return nil },
			}, nil
		},
	})
	first, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	})
	require.NoError(t, err)

	now = now.Add(time.Minute)
	reused, ok := mgr.FindReusable(OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/settings",
	})

	require.True(t, ok)
	assert.Equal(t, first.ID, reused.ID)
	assert.Equal(t, now, reused.LastUsedAt)
}

func TestManagerListPrunesExpiredSessions(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	closed := false
	mgr := NewManager(ManagerOptions{
		SessionTTL: time.Minute,
		Now:        func() time.Time { return now },
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Close: func() error {
					closed = true
					return nil
				},
			}, nil
		},
	})
	session, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:    Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL: "http://127.0.0.1:3000/",
	})
	require.NoError(t, err)

	now = now.Add(2 * time.Minute)
	list := mgr.List()
	_, ok := mgr.Get(session.ID)

	assert.Empty(t, list)
	assert.False(t, ok)
	assert.True(t, closed)
}

func TestManagerReportsDeadBrowserProcess(t *testing.T) {
	alive := true
	mgr := NewManager(ManagerOptions{
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{
				ProcessID:   123,
				DebugPort:   9222,
				BrowserWS:   "ws://127.0.0.1:9222/devtools/browser/abc",
				PageWS:      "ws://127.0.0.1:9222/devtools/page/page-1",
				DevtoolsURL: "http://127.0.0.1:9222/devtools/inspector.html?ws=127.0.0.1:9222/devtools/page/page-1",
				Alive:       func() bool { return alive },
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

	alive = false
	status, ok := mgr.Status(session.ID)

	require.True(t, ok)
	assert.False(t, status.Alive)
	assert.Equal(t, "browser process is not alive", status.Error)
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

func TestManagerPassesViewportToLauncher(t *testing.T) {
	var gotWidth int
	var gotHeight int
	mgr := NewManager(ManagerOptions{
		Launch: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			gotWidth = req.ViewportWidth
			gotHeight = req.ViewportHeight
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
		Browser:        BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc", Available: true},
		Target:         Target{DeploymentID: "dep-1", BaseURL: "http://127.0.0.1:3000", DefaultPath: "/"},
		TargetURL:      "http://127.0.0.1:3000/",
		ViewportWidth:  1478,
		ViewportHeight: 1000,
	})
	require.NoError(t, err)

	assert.Equal(t, 1478, gotWidth)
	assert.Equal(t, 1000, gotHeight)
}

func TestManagerTouchExtendsIdleTTL(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	closed := false
	mgr := NewManager(ManagerOptions{
		SessionTTL: time.Minute,
		Now:        func() time.Time { return now },
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{DebugPort: 9222, BrowserWS: "ws://browser", PageWS: "ws://page", Close: func() error {
				closed = true
				return nil
			}}, nil
		},
	})
	session, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/bin/arc", Available: true},
		Target:    Target{DeploymentID: "dep-1"},
		TargetURL: "http://127.0.0.1:5173/",
	})
	require.NoError(t, err)

	now = now.Add(45 * time.Second)
	require.True(t, mgr.Touch(session.ID))
	now = now.Add(45 * time.Second)

	_, ok := mgr.Get(session.ID)
	assert.True(t, ok)
	assert.False(t, closed)
}

func TestManagerStatusReportsDeadSession(t *testing.T) {
	alive := true
	mgr := NewManager(ManagerOptions{
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{DebugPort: 9222, BrowserWS: "ws://browser", PageWS: "ws://page", Alive: func() bool { return alive }}, nil
		},
	})
	session, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/bin/arc", Available: true},
		Target:    Target{DeploymentID: "dep-1"},
		TargetURL: "http://127.0.0.1:5173/",
	})
	require.NoError(t, err)

	alive = false
	status, ok := mgr.Status(session.ID)

	require.True(t, ok)
	assert.False(t, status.Alive)
	assert.Equal(t, "browser process is not alive", status.Error)
}

func TestManagerCloseIsIdempotent(t *testing.T) {
	closeCount := 0
	mgr := NewManager(ManagerOptions{
		Launch: func(context.Context, LaunchRequest) (LaunchResult, error) {
			return LaunchResult{DebugPort: 9222, BrowserWS: "ws://browser", PageWS: "ws://page", Close: func() error {
				closeCount++
				return nil
			}}, nil
		},
	})
	session, err := mgr.Open(context.Background(), OpenResolvedRequest{
		Browser:   BrowserRecord{ID: "arc", Name: "Arc", ExecutablePath: "/bin/arc", Available: true},
		Target:    Target{DeploymentID: "dep-1"},
		TargetURL: "http://127.0.0.1:5173/",
	})
	require.NoError(t, err)

	require.NoError(t, mgr.Close(session.ID))
	require.NoError(t, mgr.Close(session.ID))
	assert.Equal(t, 1, closeCount)
}

func TestCleanupStaleProfilesOnlyRemovesSuperDevProfiles(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, "session-stale")
	fresh := filepath.Join(root, "session-fresh")
	foreign := filepath.Join(root, "Chrome")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.MkdirAll(fresh, 0o755))
	require.NoError(t, os.MkdirAll(foreign, 0o755))
	old := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(stale, old, old))

	removed, err := CleanupStaleProfiles(root, time.Hour, time.Now())

	require.NoError(t, err)
	assert.Equal(t, []string{stale}, removed)
	assert.NoDirExists(t, stale)
	assert.DirExists(t, fresh)
	assert.DirExists(t, foreign)
}
