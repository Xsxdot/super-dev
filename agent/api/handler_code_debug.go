// handler_code_debug.go 实现本机代码调试 HTTP API。
//
// 职责：
//   - 列出可调试的本机 command deployment
//   - 创建、控制和关闭代码调试会话
//   - 接入 operation 审批链路
//
// 边界：
//   - 不直接实现 DAP 协议
//   - 不修改普通 deployment 启停状态
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func (a *App) listCodeDebugTargets(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	projects := append([]model.Project(nil), a.projects...)
	a.mu.RUnlock()
	jsonOK(w, codedebug.ListTargets(projects))
}

func (a *App) openCodeDebugSession(w http.ResponseWriter, r *http.Request) {
	var req codedebug.OpenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	if req.DeploymentID == "" {
		jsonError(w, http.StatusBadRequest, "deployment_id is required")
		return
	}
	dep, svc, project, ok := a.findDeploymentWithService(req.DeploymentID)
	if !ok {
		jsonCodeError(w, http.StatusNotFound, "debug_target_not_found", "debug target not found", nil)
		return
	}
	provider := ""
	if dep.CodeDebug != nil {
		provider = string(dep.CodeDebug.Provider)
	}
	if req.Provider != "" {
		provider = strings.TrimSpace(req.Provider)
	}
	plan, err := operation.PlanCodeDebugOpen(project, svc, dep, provider)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return
	}
	session, err := a.codeDebug.Open(r.Context(), project, svc, dep, req)
	if err != nil {
		if approval != nil {
			a.appendOperationExecutionFailure(r, plan, approval, "failed to open code debug session: "+err.Error())
		}
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, session)
}

func (a *App) listCodeDebugSessions(w http.ResponseWriter, r *http.Request) {
	jsonOK(w, a.codeDebug.List())
}

func (a *App) getCodeDebugSession(w http.ResponseWriter, r *http.Request) {
	session, ok := a.codeDebug.Status(r.PathValue("id"))
	if !ok {
		jsonCodeError(w, http.StatusNotFound, "debug_session_not_found", "debug session not found", nil)
		return
	}
	jsonOK(w, session)
}

func (a *App) closeCodeDebugSession(w http.ResponseWriter, r *http.Request) {
	stopRuntime := true
	if err := a.codeDebug.Close(r.PathValue("id"), codedebug.CloseRequest{StopRuntime: &stopRuntime}); err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, map[string]string{"session_id": r.PathValue("id")})
}

func (a *App) setCodeDebugBreakpoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Lines  []int  `json:"lines"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := a.codeDebug.SetBreakpoints(r.Context(), r.PathValue("id"), req.Source, req.Lines)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugContinue(w http.ResponseWriter, r *http.Request) {
	a.codeDebugThreadAction(w, r, "continue")
}

func (a *App) codeDebugPause(w http.ResponseWriter, r *http.Request) {
	a.codeDebugThreadAction(w, r, "pause")
}

func (a *App) codeDebugStepOver(w http.ResponseWriter, r *http.Request) {
	a.codeDebugThreadAction(w, r, "step_over")
}

func (a *App) codeDebugStepIn(w http.ResponseWriter, r *http.Request) {
	a.codeDebugThreadAction(w, r, "step_in")
}

func (a *App) codeDebugStepOut(w http.ResponseWriter, r *http.Request) {
	a.codeDebugThreadAction(w, r, "step_out")
}

func (a *App) codeDebugThreadAction(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		ThreadID int `json:"thread_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := a.codeDebug.ThreadAction(r.Context(), r.PathValue("id"), action, req.ThreadID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugStackTrace(w http.ResponseWriter, r *http.Request) {
	threadID, err := strconv.Atoi(r.URL.Query().Get("thread_id"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "thread_id is required")
		return
	}
	result, err := a.codeDebug.StackTrace(r.Context(), r.PathValue("id"), threadID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugScopes(w http.ResponseWriter, r *http.Request) {
	frameID, err := strconv.Atoi(r.URL.Query().Get("frame_id"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "frame_id is required")
		return
	}
	result, err := a.codeDebug.Scopes(r.Context(), r.PathValue("id"), frameID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugVariables(w http.ResponseWriter, r *http.Request) {
	ref, err := strconv.Atoi(r.URL.Query().Get("variables_reference"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "variables_reference is required")
		return
	}
	result, err := a.codeDebug.Variables(r.Context(), r.PathValue("id"), ref)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugEvaluate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expression string `json:"expression"`
		FrameID    int    `json:"frame_id"`
		Source     string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		jsonError(w, http.StatusBadRequest, "expression is required")
		return
	}
	session, ok := a.codeDebug.Status(r.PathValue("id"))
	if !ok {
		jsonCodeError(w, http.StatusNotFound, "debug_session_not_found", "debug session not found", nil)
		return
	}
	expressionHash := hashExpression(req.Expression)
	plan, err := operation.PlanCodeDebugEvaluate(operation.CodeDebugEvaluateRequest{
		ProjectID:      session.ProjectID,
		DeploymentID:   session.DeploymentID,
		DebugSessionID: session.ID,
		ExpressionHash: expressionHash,
	})
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return
	}
	auditData := map[string]any{
		"expression_hash":   expressionHash,
		"expression_length": len(req.Expression),
		"source":            codeDebugEvaluateAuditSource(req.Source),
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		a.auditCodeDebugEvaluate(r.Context(), session.ID, session.DeploymentID, codedebug.ErrEvaluateDenied, auditData)
		return
	}
	result, err := a.codeDebug.Evaluate(r.Context(), session.ID, req.Expression, req.FrameID)
	if err == nil {
		auditData["result_type"] = evaluateResultType(result["result"])
	}
	a.auditCodeDebugEvaluate(r.Context(), session.ID, session.DeploymentID, err, auditData)
	if err != nil {
		if approval != nil {
			a.appendOperationExecutionFailure(r, plan, approval, "failed to evaluate code debug expression: "+err.Error())
		}
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func codeDebugEvaluateAuditSource(source string) string {
	switch strings.TrimSpace(source) {
	case "debug_evaluate", "debug_capture_at", "debug_inspect":
		return strings.TrimSpace(source)
	default:
		return "direct"
	}
}

func (a *App) codeDebugCaptureAt(w http.ResponseWriter, r *http.Request) {
	var req codedebug.CaptureAtRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SessionID = r.PathValue("id")
	result, err := a.codeDebug.CaptureAt(r.Context(), req)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func (a *App) codeDebugInspect(w http.ResponseWriter, r *http.Request) {
	var req codedebug.InspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.SessionID = r.PathValue("id")
	result, err := a.codeDebug.Inspect(r.Context(), req)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, result)
}

func writeCodeDebugError(w http.ResponseWriter, err error) {
	if info, ok := codedebug.AdapterErrorDetails(err); ok {
		status := http.StatusServiceUnavailable
		if info.Code == codedebug.CodeAdapterUnavailable {
			status = http.StatusBadRequest
		}
		jsonCodeError(w, status, info.Code, err.Error(), map[string]any{
			"provider":         string(info.Provider),
			"command":          info.Command,
			"remediation_hint": info.Hint,
		})
		return
	}
	switch {
	case errors.Is(err, codedebug.ErrTargetNotFound):
		jsonCodeError(w, http.StatusNotFound, "debug_target_not_found", "debug target not found", nil)
	case errors.Is(err, codedebug.ErrTargetUnsupported):
		jsonCodeError(w, http.StatusBadRequest, "debug_target_unsupported", "debug target unsupported", nil)
	case errors.Is(err, codedebug.ErrConfigInvalid):
		jsonCodeError(w, http.StatusBadRequest, "debug_config_invalid", "debug config invalid", nil)
	case errors.Is(err, codedebug.ErrAdapterUnavailable):
		jsonCodeError(w, http.StatusBadRequest, "adapter_unavailable", err.Error(), nil)
	case errors.Is(err, codedebug.ErrPathOutsideProject):
		jsonCodeError(w, http.StatusBadRequest, "breakpoint_outside_project", "breakpoint outside project", nil)
	case errors.Is(err, codedebug.ErrSessionNotFound):
		jsonCodeError(w, http.StatusNotFound, "debug_session_not_found", "debug session not found", nil)
	case errors.Is(err, codedebug.ErrSessionClosed):
		jsonCodeError(w, http.StatusGone, "debug_session_closed", "debug session closed", nil)
	default:
		jsonCodeError(w, http.StatusInternalServerError, "dap_request_failed", err.Error(), nil)
	}
}
