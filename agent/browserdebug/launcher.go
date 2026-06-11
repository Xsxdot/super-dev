// launcher.go 启动 Chromium 兼容浏览器并发现 CDP endpoint。
//
// 职责：
//   - 创建临时浏览器 profile
//   - 传入 Chromium remote debugging 参数启动浏览器
//   - 读取 DevToolsActivePort 并查询 CDP metadata
//
// 边界：
//   - 不解析 SuperDev deployment
//   - 不持久化 session
package browserdebug

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const devToolsPortTimeout = 15 * time.Second

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
		profileDir, err := os.MkdirTemp(profileRoot, "session-*")
		if err != nil {
			return LaunchResult{}, fmt.Errorf("create browser profile: %w", err)
		}
		args := []string{
			"--remote-debugging-port=0",
			"--user-data-dir=" + profileDir,
		}
		if req.OpenDevtools {
			args = append(args, "--auto-open-devtools-for-tabs")
		}
		args = append(args, req.TargetURL)
		cmd := exec.Command(req.Browser.ExecutablePath, args...)
		if err := cmd.Start(); err != nil {
			_ = os.RemoveAll(profileDir)
			return LaunchResult{}, fmt.Errorf("launch browser: %w", err)
		}
		port, err := waitDevToolsPort(ctx, filepath.Join(profileDir, "DevToolsActivePort"))
		if err != nil {
			cleanupStartedBrowser(cmd, profileDir)
			return LaunchResult{}, err
		}
		result, err := discoverCDP(ctx, httpClient, port, req.TargetURL)
		if err != nil {
			cleanupStartedBrowser(cmd, profileDir)
			return LaunchResult{}, err
		}
		result.ProcessID = cmd.Process.Pid
		result.Close = func() error {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return os.RemoveAll(profileDir)
		}
		return result, nil
	}
}

func cleanupStartedBrowser(cmd *exec.Cmd, profileDir string) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	_ = os.RemoveAll(profileDir)
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
	var targets []targetResponse
	if err := getJSON(ctx, client, base+"/json/list", &targets); err != nil {
		return LaunchResult{}, err
	}
	for _, target := range targets {
		if target.Type != "page" || target.WebSocketDebuggerURL == "" {
			continue
		}
		if target.URL == targetURL {
			return launchResultForTarget(base, port, version, target), nil
		}
	}
	for _, target := range targets {
		if target.Type == "page" && target.WebSocketDebuggerURL != "" {
			return launchResultForTarget(base, port, version, target), nil
		}
	}
	return LaunchResult{}, fmt.Errorf("page target not found")
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
