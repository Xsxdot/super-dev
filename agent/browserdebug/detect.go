// detect.go 探测本机已安装的 Chromium 兼容调试浏览器。
//
// 职责：
//   - 提供各操作系统常见浏览器可执行文件候选路径
//   - 将存在且可执行的候选转换为 BrowserRecord
//
// 边界：
//   - 不启动浏览器进程
//   - 不写入 agent settings
package browserdebug

import (
	"os"
	"path/filepath"
	"runtime"
)

// BrowserCandidate 描述一个可探测的浏览器安装候选。
type BrowserCandidate struct {
	ID             string
	Name           string
	ExecutablePath string
}

// DefaultBrowserCandidates 返回当前系统常见 Chromium 兼容浏览器候选。
func DefaultBrowserCandidates() []BrowserCandidate {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		candidates := []BrowserCandidate{
			{ID: "arc", Name: "Arc", ExecutablePath: "/Applications/Arc.app/Contents/MacOS/Arc"},
			{ID: "chrome", Name: "Google Chrome", ExecutablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"},
			{ID: "edge", Name: "Microsoft Edge", ExecutablePath: "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"},
			{ID: "brave", Name: "Brave Browser", ExecutablePath: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser"},
			{ID: "chromium", Name: "Chromium", ExecutablePath: "/Applications/Chromium.app/Contents/MacOS/Chromium"},
		}
		if home != "" {
			userApps := filepath.Join(home, "Applications")
			candidates = append(candidates,
				BrowserCandidate{ID: "arc-user", Name: "Arc", ExecutablePath: filepath.Join(userApps, "Arc.app/Contents/MacOS/Arc")},
				BrowserCandidate{ID: "chrome-user", Name: "Google Chrome", ExecutablePath: filepath.Join(userApps, "Google Chrome.app/Contents/MacOS/Google Chrome")},
				BrowserCandidate{ID: "edge-user", Name: "Microsoft Edge", ExecutablePath: filepath.Join(userApps, "Microsoft Edge.app/Contents/MacOS/Microsoft Edge")},
			)
		}
		return candidates
	case "windows":
		return []BrowserCandidate{
			{ID: "chrome", Name: "Google Chrome", ExecutablePath: filepath.Join(os.Getenv("ProgramFiles"), "Google/Chrome/Application/chrome.exe")},
			{ID: "edge", Name: "Microsoft Edge", ExecutablePath: filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft/Edge/Application/msedge.exe")},
		}
	default:
		return []BrowserCandidate{
			{ID: "google-chrome", Name: "Google Chrome", ExecutablePath: "/usr/bin/google-chrome"},
			{ID: "chromium", Name: "Chromium", ExecutablePath: "/usr/bin/chromium"},
			{ID: "microsoft-edge", Name: "Microsoft Edge", ExecutablePath: "/usr/bin/microsoft-edge"},
			{ID: "brave-browser", Name: "Brave Browser", ExecutablePath: "/usr/bin/brave-browser"},
		}
	}
}

// DetectInstalledBrowsers 返回当前机器可用的调试浏览器。
func DetectInstalledBrowsers() []BrowserRecord {
	return DetectBrowsersFromCandidates(DefaultBrowserCandidates())
}

// DetectBrowsersFromCandidates 从候选路径中筛选可执行浏览器。
func DetectBrowsersFromCandidates(candidates []BrowserCandidate) []BrowserRecord {
	seen := map[string]struct{}{}
	records := []BrowserRecord{}
	for _, candidate := range candidates {
		if candidate.ID == "" || candidate.ExecutablePath == "" {
			continue
		}
		if _, ok := seen[candidate.ID]; ok {
			continue
		}
		if !executableAvailable(candidate.ExecutablePath) {
			continue
		}
		seen[candidate.ID] = struct{}{}
		records = append(records, BrowserRecord{
			ID:             candidate.ID,
			Name:           candidate.Name,
			ExecutablePath: candidate.ExecutablePath,
			Available:      true,
		})
	}
	return records
}
