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
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionClick(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.ClickRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Click(r.Context(), session, req)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionType(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.TypeRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Type(r.Context(), session, req)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
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
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) browserSessionEvaluate(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserControlSession(w, r)
	if !ok {
		return
	}
	var req browsercontrol.EvaluateRequest
	if err := decodeBrowserControlBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := a.browserControl.Evaluate(r.Context(), session, req)
	if err != nil {
		jsonError(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, result)
}

func (a *App) browserControlSession(w http.ResponseWriter, r *http.Request) (browsercontrol.SessionRef, bool) {
	record, ok := a.browserDebug.Get(r.PathValue("id"))
	if !ok || record.Session.Closed {
		jsonError(w, http.StatusNotFound, "browser session not found")
		return browsercontrol.SessionRef{}, false
	}
	return browsercontrol.SessionRef{
		ID:        record.Session.ID,
		TargetURL: record.Session.TargetURL,
		BrowserWS: record.Session.BrowserWS,
		PageWS:    record.Session.PageWS,
	}, true
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
