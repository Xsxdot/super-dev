// handler_browser_control.go 实现本机浏览器调试会话的页面控制 HTTP API。
//
// 职责：
//   - 将已审批创建的 browser session 解析为 Playwright 控制目标
//   - 暴露 snapshot、click、type、screenshot、evaluate 控制动作
//
// 边界：
//   - 不创建 browser session
//   - 不改变 browserdebug 的本机安全边界
//   - 不向外暴露 Playwright 原始协议
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/xsxdot/super-dev/agent/browsercontrol"
)

func (a *App) browserSessionSnapshot(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.SnapshotRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Snapshot(r.Context(), session, req)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionClick(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.ClickRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Click(r.Context(), session, req)
	a.auditBrowserControl(r.Context(), "click", session.ID, deploymentID, err, map[string]any{"selector": req.Selector})
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionType(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.TypeRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Type(r.Context(), session, req)
	// 只记录 selector、fill 模式和输入长度，绝不记录输入框明文，避免泄漏密码/token。
	a.auditBrowserControl(r.Context(), "type", session.ID, deploymentID, err, map[string]any{
		"selector":    req.Selector,
		"fill":        req.Fill,
		"text_length": len(req.Text),
	})
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionScreenshot(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.ScreenshotRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Screenshot(r.Context(), session, req)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionNavigate(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.NavigateRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Navigate(r.Context(), session, req)
	a.auditBrowserControl(r.Context(), "navigate", session.ID, deploymentID, err, map[string]any{
		"url":  req.URL,
		"path": req.Path,
	})
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionReload(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.ReloadRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Reload(r.Context(), session, req)
	a.auditBrowserControl(r.Context(), "reload", session.ID, deploymentID, err, nil)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionWaitForSelector(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.WaitForSelectorRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.WaitForSelector(r.Context(), session, req)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionPressKey(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.PressKeyRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.PressKey(r.Context(), session, req)
	a.auditBrowserControl(r.Context(), "press_key", session.ID, deploymentID, err, map[string]any{
		"selector": req.Selector,
		"key":      req.Key,
	})
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionSelectOption(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.SelectOptionRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.SelectOption(r.Context(), session, req)
	a.auditBrowserControl(r.Context(), "select_option", session.ID, deploymentID, err, map[string]any{
		"selector": req.Selector,
		"value":    req.Value,
		"label":    req.Label,
	})
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionConsoleLogs(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.ConsoleLogsRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.ConsoleLogs(r.Context(), session, req)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionNetworkRequests(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.NetworkRequestsRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.NetworkRequests(r.Context(), session, req)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionEvaluate(w http.ResponseWriter, r *http.Request) {
	session, deploymentID, ok := a.browserControlSessionContext(w, r)
	if !ok {
		return
	}
	var req browsercontrol.EvaluateRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	settings, err := a.settings.Load()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to load settings: "+err.Error())
		return
	}
	// evaluate 是唯一能读取页面凭证的工具，无论放行与否都必须留痕。
	// 审计只记录表达式 hash 和长度，绝不记录表达式明文或返回值。
	evaluateAuditData := map[string]any{
		"expression_sha256": hashExpression(req.Expression),
		"expression_length": len(req.Expression),
	}
	if !settings.DebugBrowser.AllowEvaluate {
		disabledErr := browsercontrol.NewControlError(browsercontrol.CodeEvaluateDisabled, "browser_evaluate is disabled in settings", nil)
		a.auditBrowserControl(r.Context(), "evaluate", session.ID, deploymentID, disabledErr, evaluateAuditData)
		jsonErrorCode(w, http.StatusForbidden, browsercontrol.CodeEvaluateDisabled, "browser_evaluate is disabled in settings", nil)
		return
	}
	result, err := a.browserControl.Evaluate(r.Context(), session, req)
	if err == nil {
		evaluateAuditData["result_type"] = evaluateResultType(result.Result)
	}
	a.auditBrowserControl(r.Context(), "evaluate", session.ID, deploymentID, err, evaluateAuditData)
	if err != nil {
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, result)
}

// evaluateResultType 返回 evaluate 结果的粗粒度类型，用于审计但不暴露结果值。
func evaluateResultType(result any) string {
	switch result.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32, int, int64, int32:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func (a *App) browserControlSession(w http.ResponseWriter, r *http.Request) (browsercontrol.SessionRef, bool) {
	session, _, ok := a.browserControlSessionContext(w, r)
	return session, ok
}

// browserControlSessionContext 解析控制目标，并附带审计所需的 deployment 上下文。
//
// 返回：
//   - 控制层使用的 SessionRef（不含 page_ws，控制层用 browser_ws 连接）
//   - 审计用的 deployment id，用于在审计事件中定位被调试的本机前端
//   - 会话是否存在且未关闭
func (a *App) browserControlSessionContext(w http.ResponseWriter, r *http.Request) (browsercontrol.SessionRef, string, bool) {
	record, ok := a.browserDebug.Get(r.PathValue("id"))
	if !ok || record.Session.Closed {
		jsonError(w, http.StatusNotFound, "browser session not found")
		return browsercontrol.SessionRef{}, "", false
	}
	a.browserDebug.Touch(record.Session.ID)
	return browsercontrol.SessionRef{
		ID:        record.Session.ID,
		TargetURL: record.Session.TargetURL,
		BrowserWS: record.Session.BrowserWS,
	}, record.Session.DeploymentID, true
}

func decodeBrowserControlBody(r *http.Request, out any) error {
	if r.Body == nil {
		return nil
	}
	if err := json.NewDecoder(r.Body).Decode(out); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func jsonBrowserControlError(w http.ResponseWriter, err error) {
	var controlErr browsercontrol.ControlError
	if errors.As(err, &controlErr) {
		status := http.StatusBadGateway
		switch controlErr.Code {
		case browsercontrol.CodeInvalidArgument:
			status = http.StatusBadRequest
		case browsercontrol.CodeEvaluateDisabled, browsercontrol.CodeNavigationDenied:
			status = http.StatusForbidden
		case browsercontrol.CodeSessionNotFound, browsercontrol.CodeSelectorNotFound:
			status = http.StatusNotFound
		case browsercontrol.CodeActionTimeout, browsercontrol.CodeScreenshotTooLarge:
			status = http.StatusRequestTimeout
		}
		jsonErrorCode(w, status, controlErr.Code, controlErr.Message, controlErr.Data)
		return
	}
	jsonError(w, http.StatusBadGateway, err.Error())
}
