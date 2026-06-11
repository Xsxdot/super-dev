// Package browsercontrol 提供浏览器页面控制的类型化错误。
//
// 职责：
//   - 给 API/MCP 暴露稳定错误码
//   - 保留少量脱敏结构化数据，便于 AI 判断失败原因
//
// 边界：
//   - 不记录持久审计
//   - 不包含 cookie、token、password、localStorage 或输入框明文
package browsercontrol

import "fmt"

const (
	// CodeSessionNotFound 表示浏览器调试 session 不存在或不可用。
	CodeSessionNotFound = "browser_session_not_found"
	// CodeSessionBusy 表示同一 session 正在执行另一个控制动作。
	CodeSessionBusy = "browser_session_busy"
	// CodeEvaluateDisabled 表示 settings 未开启 browser_evaluate。
	CodeEvaluateDisabled = "browser_evaluate_disabled"
	// CodeInvalidArgument 表示工具参数不合法。
	CodeInvalidArgument = "invalid_arguments"
	// CodeSelectorNotFound 表示页面中找不到目标 selector。
	CodeSelectorNotFound = "browser_selector_not_found"
	// CodeActionTimeout 表示页面动作等待超时。
	CodeActionTimeout = "browser_action_timeout"
	// CodeCDPConnectionFailed 表示无法连接浏览器 CDP endpoint。
	CodeCDPConnectionFailed = "browser_cdp_connection_failed"
	// CodeScreenshotTooLarge 表示截图体积超过 MCP/上下文安全上限。
	CodeScreenshotTooLarge = "browser_screenshot_too_large"
	// CodeNavigationDenied 表示导航目标超出本机前端安全边界。
	CodeNavigationDenied = "browser_navigation_denied"
)

// ControlError 是 browsercontrol 向 API/MCP 暴露的稳定错误契约。
type ControlError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data,omitempty"`
	Err     error          `json:"-"`
}

// Error 返回适合日志展示的错误摘要。
func (e ControlError) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

// Unwrap 返回底层错误，供 errors.Is/As 继续匹配。
func (e ControlError) Unwrap() error {
	return e.Err
}

// NewControlError 创建不包裹底层错误的浏览器控制错误。
func NewControlError(code string, message string, data map[string]any) error {
	return ControlError{Code: code, Message: message, Data: data}
}

// WrapControlError 创建包裹底层错误的浏览器控制错误。
func WrapControlError(code string, message string, err error) error {
	return ControlError{Code: code, Message: message, Err: err}
}
