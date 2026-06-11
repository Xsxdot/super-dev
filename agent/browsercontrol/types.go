// Package browsercontrol 提供基于 Playwright 的本机浏览器调试控制能力。
//
// 职责：
//   - 将 SuperDev browser session 转换为可执行的页面控制动作
//   - 提供 snapshot、click、type、screenshot、evaluate 等最小 AI 调试原语
//   - 隐藏 Playwright/CDP 细节，向 API/MCP 暴露稳定 DTO
//
// 边界：
//   - 不创建或关闭浏览器调试 session
//   - 不放宽 browserdebug 的本机 loopback 安全边界
//   - 不实现完整 Playwright API，只暴露产品化的最小控制集合
package browsercontrol

import (
	"context"
	"time"
)

const (
	// DefaultScreenshotMaxBytes 是截图响应默认允许的 PNG 原始字节上限。
	DefaultScreenshotMaxBytes = 1536 * 1024
)

// SessionRef 描述一次控制动作需要连接的浏览器调试会话。
type SessionRef struct {
	ID        string
	TargetURL string
	BrowserWS string
}

// SnapshotRequest 描述页面快照请求。
type SnapshotRequest struct {
	Selector    string `json:"selector,omitempty"`
	MaxText     int    `json:"max_text,omitempty"`
	MaxElements int    `json:"max_elements,omitempty"`
}

// Snapshot 描述 AI 可读的页面状态快照。
type Snapshot struct {
	SessionID string            `json:"session_id"`
	URL       string            `json:"url"`
	Title     string            `json:"title"`
	Text      string            `json:"text"`
	Elements  []SnapshotElement `json:"elements,omitempty"`
	Focused   *SnapshotElement  `json:"focused,omitempty"`
}

// SnapshotElement 描述页面中 AI 可操作的可见元素摘要。
type SnapshotElement struct {
	Role     string          `json:"role"`
	Name     string          `json:"name,omitempty"`
	Selector string          `json:"selector"`
	Visible  bool            `json:"visible"`
	Enabled  bool            `json:"enabled"`
	Bounds   *SnapshotBounds `json:"bounds,omitempty"`
}

// SnapshotBounds 描述页面元素在 viewport 中的位置和尺寸。
type SnapshotBounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// SnapshotCapability 描述当前 snapshot 结构化来源能力。
type SnapshotCapability struct {
	AccessibilityTree bool   `json:"accessibility_tree"`
	DOMFallback       bool   `json:"dom_fallback"`
	Message           string `json:"message"`
}

// ClickRequest 描述点击请求。
type ClickRequest struct {
	Selector string `json:"selector"`
}

// TypeRequest 描述输入请求。
type TypeRequest struct {
	Selector string `json:"selector"`
	Text     string `json:"text"`
	Fill     bool   `json:"fill,omitempty"`
}

// ScreenshotRequest 描述截图请求。
type ScreenshotRequest struct {
	FullPage bool `json:"full_page,omitempty"`
	MaxBytes int  `json:"max_bytes,omitempty"`
}

// Screenshot 描述截图结果。
type Screenshot struct {
	SessionID  string `json:"session_id"`
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
}

// NavigateRequest 描述同源整页导航请求。
type NavigateRequest struct {
	URL       string `json:"url,omitempty"`
	Path      string `json:"path,omitempty"`
	WaitUntil string `json:"wait_until,omitempty"`
}

// ReloadRequest 描述页面刷新请求。
type ReloadRequest struct {
	WaitUntil string `json:"wait_until,omitempty"`
}

// NavigationResult 描述页面导航后的状态。
type NavigationResult struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
}

// WaitForSelectorRequest 描述等待页面元素出现或进入指定状态的请求。
type WaitForSelectorRequest struct {
	Selector  string `json:"selector"`
	State     string `json:"state,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

// WaitForSelectorResult 描述等待 selector 的结果。
type WaitForSelectorResult struct {
	SessionID string `json:"session_id"`
	Matched   bool   `json:"matched"`
	Text      string `json:"text,omitempty"`
}

// PressKeyRequest 描述键盘按键请求。
type PressKeyRequest struct {
	Selector string `json:"selector,omitempty"`
	Key      string `json:"key"`
}

// SelectOptionRequest 描述选择下拉选项请求。
type SelectOptionRequest struct {
	Selector string `json:"selector"`
	Value    string `json:"value,omitempty"`
	Label    string `json:"label,omitempty"`
}

// ConsoleLog 描述页面 console 日志摘要。
type ConsoleLog struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Text  string    `json:"text"`
}

// NetworkRequest 描述页面网络请求摘要。
type NetworkRequest struct {
	Time   time.Time `json:"time"`
	Method string    `json:"method"`
	URL    string    `json:"url"`
	Status int       `json:"status,omitempty"`
	Error  string    `json:"error,omitempty"`
}

// ConsoleLogsRequest 描述读取 console 日志请求。
type ConsoleLogsRequest struct {
	Level string `json:"level,omitempty"`
	Limit int    `json:"limit,omitempty"`
}

// ConsoleLogsResult 描述 console 日志读取结果。
type ConsoleLogsResult struct {
	SessionID string       `json:"session_id"`
	Logs      []ConsoleLog `json:"logs"`
}

// NetworkRequestsRequest 描述读取网络请求摘要请求。
type NetworkRequestsRequest struct {
	Filter string `json:"filter,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// NetworkRequestsResult 描述网络请求摘要读取结果。
type NetworkRequestsResult struct {
	SessionID string           `json:"session_id"`
	Requests  []NetworkRequest `json:"requests"`
}

// EvaluateRequest 描述页面脚本执行请求。
type EvaluateRequest struct {
	Expression string `json:"expression"`
}

// EvaluateResult 描述脚本执行结果。
type EvaluateResult struct {
	SessionID string `json:"session_id"`
	Result    any    `json:"result"`
}

// ActionResult 描述无额外负载的浏览器控制动作结果。
type ActionResult struct {
	SessionID string `json:"session_id"`
	OK        bool   `json:"ok"`
}

// Controller 是 API/MCP 依赖的浏览器控制接口。
type Controller interface {
	Snapshot(context.Context, SessionRef, SnapshotRequest) (Snapshot, error)
	Click(context.Context, SessionRef, ClickRequest) (ActionResult, error)
	Type(context.Context, SessionRef, TypeRequest) (ActionResult, error)
	Screenshot(context.Context, SessionRef, ScreenshotRequest) (Screenshot, error)
	Navigate(context.Context, SessionRef, NavigateRequest) (NavigationResult, error)
	Reload(context.Context, SessionRef, ReloadRequest) (NavigationResult, error)
	WaitForSelector(context.Context, SessionRef, WaitForSelectorRequest) (WaitForSelectorResult, error)
	PressKey(context.Context, SessionRef, PressKeyRequest) (ActionResult, error)
	SelectOption(context.Context, SessionRef, SelectOptionRequest) (ActionResult, error)
	ConsoleLogs(context.Context, SessionRef, ConsoleLogsRequest) (ConsoleLogsResult, error)
	NetworkRequests(context.Context, SessionRef, NetworkRequestsRequest) (NetworkRequestsResult, error)
	Evaluate(context.Context, SessionRef, EvaluateRequest) (EvaluateResult, error)
}
