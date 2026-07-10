// launcher_test.go 验证 Chromium launcher 的进程生命周期和参数。
//
// 职责：
//   - 使用 fake browser 覆盖 DevToolsActivePort 发现流程
//   - 确认启动上下文取消不会误杀浏览器进程
//
// 边界：
//   - 不启动真实浏览器
//   - 不依赖真实 CDP 服务
package browserdebug

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChromiumLauncherKeepsBrowserAliveAfterStartupContextCancellation(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	browserPath := filepath.Join(t.TempDir(), "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), "")

	ctx, cancel := context.WithCancel(context.Background())
	launcher := NewChromiumLauncher(t.TempDir(), cdp.Client())
	result, err := launcher(ctx, LaunchRequest{
		Browser:   BrowserRecord{ID: "fake", Name: "Fake Browser", ExecutablePath: browserPath, Available: true},
		TargetURL: targetURL,
	})
	require.NoError(t, err)
	require.NotZero(t, result.ProcessID)

	cancel()

	proc, err := os.FindProcess(result.ProcessID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return proc.Signal(syscall.Signal(0)) == nil
	}, 500*time.Millisecond, 20*time.Millisecond)

	require.NoError(t, result.Close())
}

func TestChromiumLauncherHonorsOpenDevtoolsFlag(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	browserPath := filepath.Join(dir, "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), argsPath)

	launcher := NewChromiumLauncher(t.TempDir(), cdp.Client())
	result, err := launcher(context.Background(), LaunchRequest{
		Browser:      BrowserRecord{ID: "fake", Name: "Fake Browser", ExecutablePath: browserPath, Available: true},
		TargetURL:    targetURL,
		OpenDevtools: false,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, result.Close()) }()

	args, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	assert.NotContains(t, string(args), "--auto-open-devtools-for-tabs")
}

func TestChromiumLauncherUsesFreshProfileSafeStartupFlags(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	browserPath := filepath.Join(dir, "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), argsPath)

	launcher := NewChromiumLauncher(t.TempDir(), cdp.Client())
	result, err := launcher(context.Background(), LaunchRequest{
		Browser:      BrowserRecord{ID: "fake", Name: "Fake Browser", ExecutablePath: browserPath, Available: true},
		TargetURL:    targetURL,
		OpenDevtools: true,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, result.Close()) }()

	argsBytes, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	assert.Contains(t, args, "--no-first-run")
	assert.Contains(t, args, "--no-default-browser-check")
	assert.Contains(t, args, "--disable-search-engine-choice-screen")
	assert.Contains(t, args, "--disable-extensions")
	assert.Contains(t, args, "--new-window")
	assert.Equal(t, targetURL, args[len(args)-1])
}

func TestChromiumLauncherAddsWindowSizeWhenViewportRequested(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	browserPath := filepath.Join(dir, "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), argsPath)

	launcher := NewChromiumLauncher(t.TempDir(), cdp.Client())
	result, err := launcher(context.Background(), LaunchRequest{
		Browser:        BrowserRecord{ID: "fake", Name: "Fake Browser", ExecutablePath: browserPath, Available: true},
		TargetURL:      targetURL,
		ViewportWidth:  1478,
		ViewportHeight: 1000,
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, result.Close()) }()

	argsBytes, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	assert.Contains(t, args, "--window-size=1478,1000")
}

func TestChromiumLauncherPersistentProfileReusesStableDirectory(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	browserPath := filepath.Join(dir, "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), argsPath)

	profileRoot := filepath.Join(t.TempDir(), "profiles")
	launcher := NewChromiumLauncher(profileRoot, cdp.Client())
	result, err := launcher(context.Background(), LaunchRequest{
		Browser:      BrowserRecord{ID: "arc/browser", Name: "Arc", ExecutablePath: browserPath, Available: true},
		TargetURL:    targetURL,
		ProfileMode:  ProfileModePersistent,
		ProfileScope: "dep/admin dev",
	})
	require.NoError(t, err)

	argsBytes, err := os.ReadFile(argsPath)
	require.NoError(t, err)
	args := strings.Split(strings.TrimSpace(string(argsBytes)), "\n")
	persistentDir := filepath.Join(profileRoot, "persistent", "arc-browser", "dep-admin-dev")
	assert.Contains(t, args, "--user-data-dir="+persistentDir)
	assert.Equal(t, persistentDir, result.ProfileDir)

	require.NoError(t, result.Close())
	_, err = os.Stat(persistentDir)
	require.NoError(t, err)
}

// TestChromiumLauncherPersistentProfileRejectsStaleDevToolsPort 复现持久化 profile 复用的核心 bug：
// 上一次会话在 profile 里残留了指向已死端口的 DevToolsActivePort，本次新进程因 singleton handoff
// 立即退出、不写新文件。launcher 必须识别这是死端口并报错，而不是把旧端口当成功返回。
func TestChromiumLauncherPersistentProfileRejectsStaleDevToolsPort(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	// 立刻关掉，模拟旧端口对应的浏览器已经死亡：连接一定被拒。
	stalePort := serverPort(t, cdp.URL)
	cdp.Close()

	dir := t.TempDir()
	browserPath := filepath.Join(dir, "fake-browser")
	// 这个 fake 模拟 singleton handoff：一旦 profile 里已存在 DevToolsActivePort 就直接退出，不写新文件。
	writeHandoffChromium(t, browserPath)

	profileRoot := filepath.Join(t.TempDir(), "profiles")
	persistentDir := filepath.Join(profileRoot, "persistent", "chrome", "dep-admin-dev")
	require.NoError(t, os.MkdirAll(persistentDir, 0o755))
	// 预置一份上一次会话残留的、指向死端口的 DevToolsActivePort。
	require.NoError(t, os.WriteFile(filepath.Join(persistentDir, "DevToolsActivePort"),
		fmt.Appendf(nil, "%d\n/devtools/browser/stale\n", stalePort), 0o644))

	launcher := NewChromiumLauncher(profileRoot, &http.Client{Timeout: time.Second})
	_, err := launcher(context.Background(), LaunchRequest{
		Browser:      BrowserRecord{ID: "chrome", Name: "Chrome", ExecutablePath: browserPath, Available: true},
		TargetURL:    targetURL,
		ProfileMode:  ProfileModePersistent,
		ProfileScope: "dep/admin dev",
	})
	require.Error(t, err, "launcher 不能把残留的死端口当成功返回")
}

// TestChromiumLauncherClearsStaleSingletonState 验证持久化 profile 启动前会清掉残留的
// DevToolsActivePort 与 Singleton* 文件，否则新 Chrome 会 handoff 给死进程后退出。
func TestChromiumLauncherClearsStaleSingletonState(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/page-1"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	dir := t.TempDir()
	browserPath := filepath.Join(dir, "fake-browser")
	writeFakeChromium(t, browserPath, serverPort(t, cdp.URL), "")

	profileRoot := filepath.Join(t.TempDir(), "profiles")
	persistentDir := filepath.Join(profileRoot, "persistent", "chrome", "dep-admin-dev")
	require.NoError(t, os.MkdirAll(persistentDir, 0o755))
	stale := []string{"DevToolsActivePort", "SingletonLock", "SingletonSocket", "SingletonCookie"}
	for _, name := range stale {
		require.NoError(t, os.WriteFile(filepath.Join(persistentDir, name), []byte("stale"), 0o644))
	}

	launcher := NewChromiumLauncher(profileRoot, cdp.Client())
	result, err := launcher(context.Background(), LaunchRequest{
		Browser:      BrowserRecord{ID: "chrome", Name: "Chrome", ExecutablePath: browserPath, Available: true},
		TargetURL:    targetURL,
		ProfileMode:  ProfileModePersistent,
		ProfileScope: "dep/admin dev",
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, result.Close()) }()

	// Singleton* 应在启动前被清理；DevToolsActivePort 会被新进程重写为有效端口。
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		_, statErr := os.Stat(filepath.Join(persistentDir, name))
		assert.True(t, os.IsNotExist(statErr), "%s 应在启动前被清理", name)
	}
}

func TestCleanupStartedBrowserDoesNotBlockWhenWaitChannelStalls(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	profileDir := filepath.Join(t.TempDir(), "profile")
	require.NoError(t, os.MkdirAll(profileDir, 0o755))
	stalledWait := make(chan error)
	returned := make(chan struct{})

	go func() {
		cleanupStartedBrowser(cmd, stalledWait, profileDir, true)
		close(returned)
	}()

	select {
	case <-returned:
		_, err := os.Stat(profileDir)
		assert.True(t, os.IsNotExist(err))
	case <-time.After(time.Second):
		t.Fatal("cleanupStartedBrowser blocked waiting for browser process waiter")
	}
}

func TestDiscoverCDPPrefersEquivalentTargetURLOverFirstPage(t *testing.T) {
	targetURL := "http://127.0.0.1:3000"
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			_, _ = fmt.Fprintf(w, `[
				{"type":"page","url":"devtools://devtools/bundled/inspector.html","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/devtools"},
				{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/app"}
			]`, targetURL+"/")
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	result, err := discoverCDP(context.Background(), cdp.Client(), serverPort(t, cdp.URL), targetURL)

	require.NoError(t, err)
	assert.Equal(t, "ws://127.0.0.1:9222/devtools/page/app", result.PageWS)
}

func TestDiscoverCDPWaitsForTargetPageBeforeFallback(t *testing.T) {
	targetURL := "http://127.0.0.1:3000/"
	listCalls := 0
	cdp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			_, _ = fmt.Fprint(w, `{"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/browser/browser-1"}`)
		case "/json/list":
			listCalls++
			if listCalls == 1 {
				_, _ = fmt.Fprint(w, `[{"type":"page","url":"about:blank","webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/blank"}]`)
				return
			}
			_, _ = fmt.Fprintf(w, `[{"type":"page","url":%q,"webSocketDebuggerUrl":"ws://127.0.0.1:9222/devtools/page/app"}]`, targetURL)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cdp.Close()

	result, err := discoverCDP(context.Background(), cdp.Client(), serverPort(t, cdp.URL), targetURL)

	require.NoError(t, err)
	assert.Equal(t, "ws://127.0.0.1:9222/devtools/page/app", result.PageWS)
	assert.GreaterOrEqual(t, listCalls, 2)
}

func writeFakeChromium(t *testing.T, path string, cdpPort int, argsPath string) {
	t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile="${arg#--user-data-dir=}" ;;
  esac
done
if [ -z "$profile" ]; then
  exit 2
fi
if [ -n %q ]; then
  printf "%%s\n" "$@" > %q
fi
printf "%%s\n" "%d" "unused" > "$profile/DevToolsActivePort"
trap 'exit 0' TERM INT
while true; do
  sleep 1
done
`, argsPath, argsPath, cdpPort)
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

// writeHandoffChromium 写一个模拟 Chromium singleton handoff 的 fake：
// 如果 profile 里已存在 DevToolsActivePort（说明有其它实例持有该 profile），就直接退出、不写新文件，
// 复现真实 Chrome 检测到 singleton 后 handoff 给旧实例并立即退出的行为。
func writeHandoffChromium(t *testing.T, path string) {
	t.Helper()
	script := `#!/bin/sh
profile=""
for arg in "$@"; do
  case "$arg" in
    --user-data-dir=*) profile="${arg#--user-data-dir=}" ;;
  esac
done
if [ -z "$profile" ]; then
  exit 2
fi
if [ -f "$profile/DevToolsActivePort" ]; then
  # 检测到已有 DevToolsActivePort：模拟 handoff 给旧实例后立即退出。
  exit 0
fi
printf "%s\n" "12345" "unused" > "$profile/DevToolsActivePort"
trap 'exit 0' TERM INT
while true; do
  sleep 1
done
`
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
}

func serverPort(t *testing.T, rawURL string) int {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	port, err := strconv.Atoi(parsed.Port())
	require.NoError(t, err)
	return port
}
