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
	"encoding/json"
	"errors"
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
	browser, status, msg := a.resolveDebugBrowser(req.BrowserID)
	if status != http.StatusOK {
		jsonError(w, status, msg)
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
	openDevtools := true
	if req.OpenDevtools != nil {
		openDevtools = *req.OpenDevtools
	}
	session, err := a.browserDebug.Open(r.Context(), browserdebug.OpenResolvedRequest{
		Browser:      browser,
		Target:       target,
		TargetURL:    targetURL,
		OpenDevtools: openDevtools,
	})
	if err != nil {
		if browserDebugLaunchErrorCode(err) == browsercontrol.CodeCDPConnectionFailed {
			jsonErrorCode(w, http.StatusInternalServerError, browsercontrol.CodeCDPConnectionFailed, err.Error(), nil)
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, session)
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

func (a *App) resolveDebugBrowser(requestedBrowserID string) (browserdebug.BrowserRecord, int, string) {
	settings, err := a.settings.Load()
	if err != nil {
		return browserdebug.BrowserRecord{}, http.StatusInternalServerError, err.Error()
	}
	browserID := strings.TrimSpace(requestedBrowserID)
	if browserID == "" {
		browserID = strings.TrimSpace(settings.DebugBrowser.DefaultBrowserID)
	}
	if browserID == "" {
		return browserdebug.BrowserRecord{}, http.StatusBadRequest, "debug browser is not configured"
	}
	for _, browser := range browserdebug.BrowsersFromSettings(settings.DebugBrowser) {
		if browser.ID != browserID {
			continue
		}
		if !browser.Available {
			return browserdebug.BrowserRecord{}, http.StatusBadRequest, "browser executable is unavailable"
		}
		return browser, http.StatusOK, ""
	}
	return browserdebug.BrowserRecord{}, http.StatusNotFound, "debug browser not found"
}
