// launcher.go 启动 Chromium 兼容浏览器并发现 CDP endpoint。
//
// 职责：
//   - 按配置创建临时或持久浏览器 profile
//   - 传入 Chromium remote debugging 参数启动浏览器
//   - 读取 DevToolsActivePort 并查询 CDP metadata
//
// 边界：
//   - 不解析 SuperDev deployment
//   - 不复用用户真实浏览器 profile
package browserdebug

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const devToolsPortTimeout = 15 * time.Second
const cdpTargetTimeout = 5 * time.Second
const browserCleanupTimeout = 500 * time.Millisecond

const (
	// ProfileModeEphemeral 表示每个调试 session 使用新的临时 Chromium user data dir。
	ProfileModeEphemeral = "ephemeral"
	// ProfileModePersistent 表示复用 SuperDev 数据目录下的隔离 Chromium user data dir。
	ProfileModePersistent = "persistent"
)

type versionResponse struct {
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type targetResponse struct {
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	DevtoolsFrontendURL  string `json:"devtoolsFrontendUrl"`
}

// NewChromiumLauncher 创建真实 Chromium 启动器。
func NewChromiumLauncher(profileRoot string, httpClient *http.Client) Launcher {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 2 * time.Second}
	}
	return func(ctx context.Context, req LaunchRequest) (LaunchResult, error) {
		if req.Browser.ExecutablePath == "" {
			return LaunchResult{}, fmt.Errorf("browser executable path is required")
		}
		if err := os.MkdirAll(profileRoot, 0o755); err != nil {
			return LaunchResult{}, fmt.Errorf("create browser profile root: %w", err)
		}
		profileDir, removeProfileOnClose, err := profileDirForLaunch(profileRoot, req)
		if err != nil {
			return LaunchResult{}, err
		}
		args := []string{
			"--remote-debugging-port=0",
			"--user-data-dir=" + profileDir,
			// 新 profile 首次启动时，Chrome 可能先打开登录/默认浏览器/搜索引擎选择页。
			// 这些页面会抢占 CDP page target，导致 SuperDev 找不到目标前端页面。
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-search-engine-choice-screen",
			"--disable-extensions",
		}
		if req.OpenDevtools {
			args = append(args, "--auto-open-devtools-for-tabs")
		}
		if req.ViewportWidth > 0 && req.ViewportHeight > 0 {
			args = append(args, fmt.Sprintf("--window-size=%d,%d", req.ViewportWidth, req.ViewportHeight))
		}
		args = append(args, "--new-window")
		args = append(args, req.TargetURL)
		log.Printf("[SuperDev] opening debug browser target=%s browser=%s profile_mode=%s profile_dir=%s viewport=%dx%d", req.TargetURL, req.Browser.ID, profileModeForLaunch(req.ProfileMode), profileDir, req.ViewportWidth, req.ViewportHeight)
		cmd := exec.Command(req.Browser.ExecutablePath, args...)
		if err := cmd.Start(); err != nil {
			cleanupProfileDir(profileDir, removeProfileOnClose)
			log.Printf("[SuperDev] debug browser launch failed target=%s browser=%s error=%v", req.TargetURL, req.Browser.ID, err)
			return LaunchResult{}, fmt.Errorf("launch browser: %w", err)
		}
		var exited atomic.Bool
		done := make(chan error, 1)
		go func() {
			err := cmd.Wait()
			exited.Store(true)
			done <- err
		}()
		port, err := waitDevToolsPort(ctx, filepath.Join(profileDir, "DevToolsActivePort"))
		if err != nil {
			cleanupStartedBrowser(cmd, done, profileDir, removeProfileOnClose)
			log.Printf("[SuperDev] debug browser did not expose DevTools target=%s browser=%s error=%v", req.TargetURL, req.Browser.ID, err)
			return LaunchResult{}, err
		}
		result, err := discoverCDP(ctx, httpClient, port, req.TargetURL)
		if err != nil {
			cleanupStartedBrowser(cmd, done, profileDir, removeProfileOnClose)
			log.Printf("[SuperDev] debug browser CDP discovery failed target=%s browser=%s error=%v", req.TargetURL, req.Browser.ID, err)
			return LaunchResult{}, err
		}
		result.ProcessID = cmd.Process.Pid
		result.ProfileDir = profileDir
		result.Alive = func() bool {
			return !exited.Load()
		}
		log.Printf("[SuperDev] debug browser opened target=%s browser=%s pid=%d port=%d profile_mode=%s viewport=%dx%d", req.TargetURL, req.Browser.ID, result.ProcessID, result.DebugPort, profileModeForLaunch(req.ProfileMode), req.ViewportWidth, req.ViewportHeight)
		result.Close = func() error {
			select {
			case <-done:
			default:
				if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					cleanupProfileDir(profileDir, removeProfileOnClose)
					log.Printf("[SuperDev] debug browser close failed target=%s browser=%s pid=%d error=%v", req.TargetURL, req.Browser.ID, result.ProcessID, err)
					return err
				}
				<-done
			}
			cleanupProfileDir(profileDir, removeProfileOnClose)
			log.Printf("[SuperDev] debug browser closed target=%s browser=%s pid=%d profile_mode=%s", req.TargetURL, req.Browser.ID, result.ProcessID, profileModeForLaunch(req.ProfileMode))
			return nil
		}
		return result, nil
	}
}

func profileDirForLaunch(profileRoot string, req LaunchRequest) (string, bool, error) {
	switch profileModeForLaunch(req.ProfileMode) {
	case ProfileModeEphemeral:
		profileDir, err := os.MkdirTemp(profileRoot, "session-*")
		if err != nil {
			return "", false, fmt.Errorf("create browser profile: %w", err)
		}
		return profileDir, true, nil
	case ProfileModePersistent:
		profileDir := filepath.Join(profileRoot, "persistent", safeProfileComponent(req.Browser.ID), safeProfileComponent(req.ProfileScope))
		if err := os.MkdirAll(profileDir, 0o755); err != nil {
			return "", false, fmt.Errorf("create persistent browser profile: %w", err)
		}
		return profileDir, false, nil
	default:
		return "", false, fmt.Errorf("unsupported browser profile mode %q", req.ProfileMode)
	}
}

func profileModeForLaunch(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return ProfileModeEphemeral
	}
	return mode
}

func safeProfileComponent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "default"
	}
	return out
}

func cleanupStartedBrowser(cmd *exec.Cmd, done <-chan error, profileDir string, removeProfile bool) {
	if cmd.Process != nil {
		pid := cmd.Process.Pid
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Printf("[SuperDev] debug browser kill failed pid=%d profile_dir=%s error=%v", pid, profileDir, err)
		}
		waitStartedBrowserExit(done, pid, profileDir)
	}
	cleanupProfileDir(profileDir, removeProfile)
}

func waitStartedBrowserExit(done <-chan error, pid int, profileDir string) {
	if done == nil {
		return
	}
	timer := time.NewTimer(browserCleanupTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		log.Printf("[SuperDev] debug browser cleanup timed out pid=%d profile_dir=%s timeout=%s", pid, profileDir, browserCleanupTimeout)
	}
}

func cleanupProfileDir(profileDir string, removeProfile bool) {
	if removeProfile {
		_ = os.RemoveAll(profileDir)
	}
}

func waitDevToolsPort(ctx context.Context, path string) (int, error) {
	deadline := time.NewTimer(devToolsPortTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		port, err := readDevToolsPort(path)
		if err == nil {
			return port, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-deadline.C:
			return 0, fmt.Errorf("devtools active port not produced")
		case <-ticker.C:
		}
	}
}

func readDevToolsPort(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("empty DevToolsActivePort")
	}
	return strconv.Atoi(strings.TrimSpace(scanner.Text()))
}

func discoverCDP(ctx context.Context, client *http.Client, port int, targetURL string) (LaunchResult, error) {
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	var version versionResponse
	if err := getJSON(ctx, client, base+"/json/version", &version); err != nil {
		return LaunchResult{}, err
	}
	target, err := waitCDPTarget(ctx, client, base, targetURL)
	if err != nil {
		return LaunchResult{}, err
	}
	return launchResultForTarget(base, port, version, target), nil
}

func waitCDPTarget(ctx context.Context, client *http.Client, base string, targetURL string) (targetResponse, error) {
	deadline := time.NewTimer(cdpTargetTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var fallback targetResponse
	for {
		var targets []targetResponse
		if err := getJSON(ctx, client, base+"/json/list", &targets); err != nil {
			return targetResponse{}, err
		}
		for _, target := range targets {
			if !isPageTarget(target) {
				continue
			}
			if targetURLMatches(target.URL, targetURL) {
				return target, nil
			}
			if fallback.WebSocketDebuggerURL == "" && isUsableFallbackPage(target.URL) {
				fallback = target
			}
		}
		if strings.TrimSpace(targetURL) == "" && fallback.WebSocketDebuggerURL != "" {
			return fallback, nil
		}
		select {
		case <-ctx.Done():
			return targetResponse{}, ctx.Err()
		case <-deadline.C:
			if fallback.WebSocketDebuggerURL != "" {
				return fallback, nil
			}
			return targetResponse{}, fmt.Errorf("page target not found")
		case <-ticker.C:
		}
	}
}

func isPageTarget(target targetResponse) bool {
	return target.Type == "page" && target.WebSocketDebuggerURL != ""
}

func isUsableFallbackPage(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "http", "https", "file":
		return true
	default:
		return false
	}
}

// TargetURLMatches 比较两个目标 URL 是否指向同一个页面。
//
// 参数：
//   - candidate: 已有 session 记录中的 URL
//   - expected: 本次请求解析出的 URL
//
// 返回：
//   - 规范化后是否相同
//
// 注意：
//   - 空 path 会按 "/" 处理，fragment 不参与比较
func TargetURLMatches(candidate string, expected string) bool {
	return targetURLMatches(candidate, expected)
}

func targetURLMatches(candidate string, expected string) bool {
	candidate = strings.TrimSpace(candidate)
	expected = strings.TrimSpace(expected)
	if candidate == "" || expected == "" {
		return false
	}
	if candidate == expected {
		return true
	}
	candidateURL, candidateErr := url.Parse(candidate)
	expectedURL, expectedErr := url.Parse(expected)
	if candidateErr != nil || expectedErr != nil {
		return false
	}
	normalizeURLForCompare(candidateURL)
	normalizeURLForCompare(expectedURL)
	return candidateURL.String() == expectedURL.String()
}

func normalizeURLForCompare(value *url.URL) {
	if value.Path == "" {
		value.Path = "/"
	}
	value.Fragment = ""
}

func launchResultForTarget(base string, port int, version versionResponse, target targetResponse) LaunchResult {
	devtoolsURL := base + target.DevtoolsFrontendURL
	if target.DevtoolsFrontendURL == "" {
		devtoolsURL = base + "/devtools/inspector.html?ws=" + strings.TrimPrefix(target.WebSocketDebuggerURL, "ws://")
	}
	return LaunchResult{
		DebugPort:   port,
		BrowserWS:   version.WebSocketDebuggerURL,
		PageWS:      target.WebSocketDebuggerURL,
		DevtoolsURL: devtoolsURL,
	}
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cdp metadata request failed: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
