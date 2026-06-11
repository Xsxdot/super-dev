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

import "context"

// SessionRef 描述一次控制动作需要连接的浏览器调试会话。
type SessionRef struct {
	ID        string
	TargetURL string
	BrowserWS string
	PageWS    string
}

// SnapshotRequest 描述页面快照请求。
type SnapshotRequest struct {
	Selector string `json:"selector,omitempty"`
	MaxText  int    `json:"max_text,omitempty"`
}

// Snapshot 描述 AI 可读的页面状态快照。
type Snapshot struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title"`
	Text      string `json:"text"`
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
}

// Screenshot 描述截图结果。
type Screenshot struct {
	SessionID  string `json:"session_id"`
	MimeType   string `json:"mime_type"`
	DataBase64 string `json:"data_base64"`
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
	Evaluate(context.Context, SessionRef, EvaluateRequest) (EvaluateResult, error)
}
