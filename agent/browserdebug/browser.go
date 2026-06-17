// Package browserdebug 管理本机前端浏览器调试会话。
//
// 职责：
//   - 从项目配置中提取可调试的本机 Web entrypoint
//   - 启动隔离浏览器并发现 CDP WebSocket endpoint
//   - 维护由 SuperDev 创建的短生命周期浏览器 session
//
// 边界：
//   - 不启动或停止业务 deployment
//   - 不访问远端主机或创建 tunnel
//   - 不复用用户真实浏览器 profile
package browserdebug

import (
	"os"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/config"
)

// BrowserRecord 描述一个本机可选调试浏览器。
type BrowserRecord struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	ExecutablePath string `json:"executable_path"`
	Available      bool   `json:"available"`
}

// BrowsersFromSettings 将 agent 设置转换为运行时可展示的浏览器记录。
func BrowsersFromSettings(settings config.DebugBrowserSettings) []BrowserRecord {
	records := make([]BrowserRecord, 0, len(settings.Browsers))
	for _, browser := range settings.Browsers {
		path := strings.TrimSpace(browser.ExecutablePath)
		records = append(records, BrowserRecord{
			ID:             strings.TrimSpace(browser.ID),
			Name:           strings.TrimSpace(browser.Name),
			ExecutablePath: path,
			Available:      executableAvailable(path),
		})
	}
	return records
}

func executableAvailable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0o111 != 0
}

// Target 描述一个可被浏览器打开的本机前端 deployment。
type Target struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	ServiceID    string `json:"service_id"`
	ServiceName  string `json:"service_name"`
	DeploymentID string `json:"deployment_id"`
	EnvName      string `json:"env_name"`
	BaseURL      string `json:"base_url"`
	DefaultPath  string `json:"default_path"`
}

// OpenRequest 描述创建浏览器调试会话的请求。
type OpenRequest struct {
	DeploymentID   string `json:"deployment_id"`
	BrowserID      string `json:"browser_id,omitempty"`
	Path           string `json:"path,omitempty"`
	OpenDevtools   *bool  `json:"open_devtools,omitempty"`
	ViewportWidth  int    `json:"viewport_width,omitempty"`
	ViewportHeight int    `json:"viewport_height,omitempty"`
	ApprovalToken  string `json:"-"`
}

// Session 描述由 SuperDev 创建的浏览器调试会话。
type Session struct {
	ID           string    `json:"session_id"`
	DeploymentID string    `json:"deployment_id"`
	TargetURL    string    `json:"target_url"`
	BrowserID    string    `json:"browser_id"`
	DebugPort    int       `json:"debug_port"`
	BrowserWS    string    `json:"browser_ws"`
	PageWS       string    `json:"page_ws"`
	DevtoolsURL  string    `json:"devtools_url"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsedAt   time.Time `json:"last_used_at"`
	Alive        bool      `json:"alive"`
	Error        string    `json:"error,omitempty"`
	Closed       bool      `json:"closed,omitempty"`
	ProfileDir   string    `json:"-"`
}
