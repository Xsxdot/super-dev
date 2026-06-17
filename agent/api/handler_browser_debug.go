// handler_browser_debug.go 实现本机前端浏览器调试 HTTP API。
//
// 职责：
//   - 列出本机可用调试浏览器和 Web entrypoint
//   - 创建和关闭由 SuperDev 管理的浏览器调试会话
//
// 边界：
//   - 不启动业务 deployment
//   - 不支持远端 deployment 或 tunnel
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/browserdebug"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func (a *App) listDebugBrowsers(w http.ResponseWriter, r *http.Request) {
	settings, err := a.settings.Load()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, browserdebug.BrowsersFromSettings(settings.DebugBrowser))
}

func (a *App) detectDebugBrowsers(w http.ResponseWriter, r *http.Request) {
	candidates := a.debugBrowserCandidates
	if len(candidates) == 0 {
		candidates = browserdebug.DefaultBrowserCandidates()
	}
	jsonOK(w, browserdebug.DetectBrowsersFromCandidates(candidates))
}

func (a *App) listBrowserTargets(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	projects := append([]model.Project(nil), a.projects...)
	a.mu.RUnlock()
	jsonOK(w, browserdebug.ListTargets(projects))
}

func (a *App) openBrowserSession(w http.ResponseWriter, r *http.Request) {
	var req browserdebug.OpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := validateOptionalBrowserViewport(req.ViewportWidth, req.ViewportHeight); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	dep, svc, project, ok := a.findDeploymentWithService(strings.TrimSpace(req.DeploymentID))
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	target, targetURL, status, msg := a.resolveBrowserDebugTarget(dep, svc, project, req.Path)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}
	browser, profileMode, status, msg := a.resolveDebugBrowser(req.BrowserID)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}
	openDevtools := true
	if req.OpenDevtools != nil {
		openDevtools = *req.OpenDevtools
	}
	openReq := browserdebug.OpenResolvedRequest{
		Browser:        browser,
		Target:         target,
		TargetURL:      targetURL,
		OpenDevtools:   openDevtools,
		ProfileMode:    profileMode,
		ViewportWidth:  req.ViewportWidth,
		ViewportHeight: req.ViewportHeight,
	}
	if session, ok := a.browserDebug.FindReusable(openReq); ok {
		session, handled := a.reuseBrowserSession(w, r, session, targetURL, req.Path, req.ViewportWidth, req.ViewportHeight)
		if !handled {
			return
		}
		jsonOK(w, session)
		return
	}
	plan, err := operation.PlanBrowserDebugOpen(project, svc, dep, targetURL)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, _ := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	if err := browserdebug.WaitForReadiness(r.Context(), targetURL, dep.Web.Readiness, http.DefaultClient); err != nil {
		if errors.Is(err, browserdebug.ErrReadinessTimeout) {
			jsonErrorCode(w, http.StatusServiceUnavailable, "web_entrypoint_not_ready", err.Error(), map[string]any{"target_url": targetURL})
			return
		}
		jsonErrorCode(w, http.StatusServiceUnavailable, "web_entrypoint_not_ready", err.Error(), map[string]any{"target_url": targetURL})
		return
	}
	if session, ok := a.browserDebug.FindReusable(openReq); ok {
		session, handled := a.reuseBrowserSession(w, r, session, targetURL, req.Path, req.ViewportWidth, req.ViewportHeight)
		if !handled {
			return
		}
		jsonOK(w, session)
		return
	}
	session, err := a.browserDebug.Open(r.Context(), openReq)
	if err != nil {
		if browserDebugLaunchErrorCode(err) == browsercontrol.CodeCDPConnectionFailed {
			jsonErrorCode(w, http.StatusInternalServerError, browsercontrol.CodeCDPConnectionFailed, err.Error(), nil)
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.setBrowserSessionViewport(r.Context(), session, req.ViewportWidth, req.ViewportHeight); err != nil {
		_ = a.browserDebug.Close(session.ID)
		jsonBrowserControlError(w, err)
		return
	}
	jsonOK(w, session)
}

func (a *App) reuseBrowserSession(w http.ResponseWriter, r *http.Request, session browserdebug.Session, targetURL string, targetPath string, viewportWidth int, viewportHeight int) (browserdebug.Session, bool) {
	if !browserdebug.TargetURLMatches(session.TargetURL, targetURL) {
		log.Printf("[SuperDev] navigating reused debug browser session=%s deployment=%s browser=%s from=%s to=%s", session.ID, session.DeploymentID, session.BrowserID, session.TargetURL, targetURL)
		navigation, err := a.browserControl.Navigate(r.Context(), browserSessionRef(session), browsercontrol.NavigateRequest{URL: targetURL})
		a.auditBrowserControl(r.Context(), "navigate", session.ID, session.DeploymentID, err, map[string]any{
			"url":  targetURL,
			"path": targetPath,
		})
		if err != nil {
			jsonBrowserControlError(w, err)
			return browserdebug.Session{}, false
		}
		if updated, ok := a.browserDebug.UpdateTargetURL(session.ID, navigation.URL); ok {
			session = updated
		} else {
			session.TargetURL = navigation.URL
		}
	}
	if err := a.setBrowserSessionViewport(r.Context(), session, viewportWidth, viewportHeight); err != nil {
		jsonBrowserControlError(w, err)
		return browserdebug.Session{}, false
	}
	return session, true
}

func (a *App) setBrowserSessionViewport(ctx context.Context, session browserdebug.Session, width int, height int) error {
	if width == 0 && height == 0 {
		return nil
	}
	viewportReq := browsercontrol.ViewportRequest{Width: width, Height: height}
	_, err := a.browserControl.SetViewport(ctx, browserSessionRef(session), viewportReq)
	a.auditBrowserControl(ctx, "set_viewport", session.ID, session.DeploymentID, err, map[string]any{
		"width":  width,
		"height": height,
	})
	return err
}

func browserSessionRef(session browserdebug.Session) browsercontrol.SessionRef {
	return browsercontrol.SessionRef{
		ID:        session.ID,
		TargetURL: session.TargetURL,
		BrowserWS: session.BrowserWS,
	}
}

func validateOptionalBrowserViewport(width int, height int) error {
	if width == 0 && height == 0 {
		return nil
	}
	if width == 0 || height == 0 {
		return errors.New("viewport_width and viewport_height must be provided together")
	}
	if width < 320 || width > 10000 {
		return errors.New("viewport_width must be between 320 and 10000")
	}
	if height < 240 || height > 10000 {
		return errors.New("viewport_height must be between 240 and 10000")
	}
	return nil
}

func browserDebugLaunchErrorCode(err error) string {
	if err == nil {
		return ""
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "devtools active port"),
		strings.Contains(message, "page target not found"),
		strings.Contains(message, "cdp"),
		strings.Contains(message, "/json/version"),
		strings.Contains(message, "/json/list"):
		return browsercontrol.CodeCDPConnectionFailed
	default:
		return ""
	}
}

func (a *App) listBrowserSessions(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, a.browserDebug.List())
}

func (a *App) getBrowserSession(w http.ResponseWriter, r *http.Request) {
	session, ok := a.browserDebug.Status(r.PathValue("id"))
	if !ok {
		jsonError(w, http.StatusNotFound, "browser session not found")
		return
	}
	jsonOK(w, session)
}

func (a *App) closeBrowserSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, ok := a.browserDebug.Status(id)
	if !ok {
		jsonError(w, http.StatusNotFound, "browser session not found")
		return
	}
	if err := a.browserDebug.Close(id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	session.Closed = true
	session.Alive = false
	if closer, ok := a.browserControl.(interface{ CloseBrowserConnection(string) }); ok {
		closer.CloseBrowserConnection(session.BrowserWS)
	}
	jsonOK(w, session)
}

func (a *App) resolveBrowserDebugTarget(dep model.Deployment, svc model.Service, project model.Project, path string) (browserdebug.Target, string, int, string) {
	if dep.Location != "" && dep.Location != model.LocationLocal {
		return browserdebug.Target{}, "", http.StatusBadRequest, "browser debug v1 supports local deployments only"
	}
	if dep.Web == nil || !dep.Web.Enabled {
		return browserdebug.Target{}, "", http.StatusBadRequest, "web entrypoint is not enabled"
	}
	if !dep.Web.AIDebug.Enabled {
		return browserdebug.Target{}, "", http.StatusBadRequest, "web ai debug is not enabled"
	}
	targetPath := path
	if strings.TrimSpace(targetPath) == "" {
		targetPath = dep.Web.DefaultPath
	}
	targetURL, err := browserdebug.BuildTargetURL(dep.Web.URL, targetPath)
	if err != nil {
		return browserdebug.Target{}, "", http.StatusBadRequest, err.Error()
	}
	defaultPath := strings.TrimSpace(dep.Web.DefaultPath)
	if defaultPath == "" {
		defaultPath = "/"
	}
	if !strings.HasPrefix(defaultPath, "/") {
		defaultPath = "/" + defaultPath
	}
	target := browserdebug.Target{
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		ServiceID:    svc.ID,
		ServiceName:  svc.Name,
		DeploymentID: dep.ID,
		EnvName:      dep.EnvName,
		BaseURL:      strings.TrimRight(dep.Web.URL, "/"),
		DefaultPath:  defaultPath,
	}
	return target, targetURL, http.StatusOK, ""
}

func (a *App) resolveDebugBrowser(requestedBrowserID string) (browserdebug.BrowserRecord, string, int, string) {
	settings, err := a.settings.Load()
	if err != nil {
		return browserdebug.BrowserRecord{}, "", http.StatusInternalServerError, err.Error()
	}
	browserID := strings.TrimSpace(requestedBrowserID)
	if browserID == "" {
		browserID = strings.TrimSpace(settings.DebugBrowser.DefaultBrowserID)
	}
	if browserID == "" {
		return browserdebug.BrowserRecord{}, "", http.StatusBadRequest, "debug browser is not configured"
	}
	for _, browser := range browserdebug.BrowsersFromSettings(settings.DebugBrowser) {
		if browser.ID != browserID {
			continue
		}
		if !browser.Available {
			return browserdebug.BrowserRecord{}, "", http.StatusBadRequest, "browser executable is unavailable"
		}
		return browser, settings.DebugBrowser.ProfileMode, http.StatusOK, ""
	}
	return browserdebug.BrowserRecord{}, "", http.StatusNotFound, "debug browser not found"
}
