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
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

var errLeaseUnavailable = errors.New("debug lease unavailable")

func (a *App) listCodeDebugTargets(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	projects := append([]model.Project(nil), a.projects...)
	a.mu.RUnlock()
	jsonOK(w, codedebug.ListTargets(
		projects,
		codedebug.WithRuntimeSnapshot(a.codeDebug.RuntimeStatus),
		codedebug.WithLeaseActive(a.codeDebug.LeaseActive),
	))
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
	plan, err := operation.PlanCodeDebugOpen(project, svc, dep, svc.Language)
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
	var req codedebug.CloseRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	_, ok := a.codeDebug.Status(r.PathValue("id"))
	if !ok {
		writeCodeDebugError(w, codedebug.ErrSessionNotFound)
		return
	}
	if req.StopRuntime == nil {
		stopRuntime := true
		req.StopRuntime = &stopRuntime
	}
	if err := a.codeDebug.Close(r.PathValue("id"), req); err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, map[string]any{"session_id": r.PathValue("id"), "stop_runtime": req.StopRuntime})
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

func (a *App) continueDeploymentDebug(w http.ResponseWriter, r *http.Request) {
	deploymentID := strings.TrimSpace(r.PathValue("id"))
	if deploymentID == "" {
		jsonError(w, http.StatusBadRequest, "deployment id is required")
		return
	}
	snap, ok := a.codeDebug.DebuggerSnapshot(deploymentID)
	if !ok {
		jsonCodeError(w, http.StatusNotFound, "debugger_not_active", "no active debugger for deployment", nil)
		return
	}
	threadID := snap.ThreadID
	if threadID == 0 {
		threadID = 1
	}
	if err := a.codeDebug.ContinueRuntime(r.Context(), deploymentID, threadID); err != nil {
		writeCodeDebugError(w, err)
		return
	}
	jsonOK(w, map[string]any{"deployment_id": deploymentID, "continued": true})
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

func (a *App) deploymentDebugCapture(w http.ResponseWriter, r *http.Request) {
	var req codedebug.CaptureAtRequest
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	req.SessionID = session.ID
	result, err := a.codeDebug.CaptureAt(r.Context(), req)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugInspect(w http.ResponseWriter, r *http.Request) {
	var req codedebug.InspectRequest
	if err := decodeJSONPreserveBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	req.SessionID = session.ID
	result, err := a.codeDebug.Inspect(r.Context(), req)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugBreakpoints(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source string `json:"source"`
		Lines  []int  `json:"lines"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	result, err := a.codeDebug.SetBreakpoints(r.Context(), session.ID, req.Source, req.Lines)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugContinueThread(w http.ResponseWriter, r *http.Request) {
	a.deploymentDebugThreadAction(w, r, "continue")
}

func (a *App) deploymentDebugPause(w http.ResponseWriter, r *http.Request) {
	a.deploymentDebugThreadAction(w, r, "pause")
}

func (a *App) deploymentDebugStepOver(w http.ResponseWriter, r *http.Request) {
	a.deploymentDebugThreadAction(w, r, "step_over")
}

func (a *App) deploymentDebugStepIn(w http.ResponseWriter, r *http.Request) {
	a.deploymentDebugThreadAction(w, r, "step_in")
}

func (a *App) deploymentDebugStepOut(w http.ResponseWriter, r *http.Request) {
	a.deploymentDebugThreadAction(w, r, "step_out")
}

func (a *App) deploymentDebugThreadAction(w http.ResponseWriter, r *http.Request, action string) {
	var req struct {
		ThreadID int `json:"thread_id"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	result, err := a.codeDebug.ThreadAction(r.Context(), session.ID, action, req.ThreadID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugStack(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ThreadID int `json:"thread_id"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	result, err := a.codeDebug.StackTrace(r.Context(), session.ID, req.ThreadID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugScopes(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FrameID int `json:"frame_id"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	result, err := a.codeDebug.Scopes(r.Context(), session.ID, req.FrameID)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugVariables(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VariablesReference int `json:"variables_reference"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil && !errors.Is(err, io.EOF) {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, created, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
		return
	}
	result, err := a.codeDebug.Variables(r.Context(), session.ID, req.VariablesReference)
	if err != nil {
		writeCodeDebugError(w, err)
		return
	}
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	result["lease_created"] = created
	jsonOK(w, result)
}

func (a *App) deploymentDebugEvaluate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expression string `json:"expression"`
		FrameID    int    `json:"frame_id"`
		Source     string `json:"source"`
	}
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		jsonError(w, http.StatusBadRequest, "expression is required")
		return
	}
	dep, svc, project, ok := a.findDeploymentDebugTarget(w, r)
	if !ok {
		return
	}
	session, _, err := a.resolveDebugLease(w, r, project, svc, dep)
	if err != nil {
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
	result = ensureCodeDebugResult(result)
	result["deployment_id"] = dep.ID
	jsonOK(w, result)
}

func (a *App) findDeploymentDebugTarget(w http.ResponseWriter, r *http.Request) (model.Deployment, model.Service, model.Project, bool) {
	depID := strings.TrimSpace(r.PathValue("id"))
	if depID == "" {
		jsonError(w, http.StatusBadRequest, "deployment id is required")
		return model.Deployment{}, model.Service{}, model.Project{}, false
	}
	dep, svc, project, ok := a.findDeploymentWithService(depID)
	if !ok {
		jsonCodeError(w, http.StatusNotFound, "debug_target_not_found", "debug target not found", nil)
		return model.Deployment{}, model.Service{}, model.Project{}, false
	}
	return dep, svc, project, true
}

func (a *App) resolveDebugLease(w http.ResponseWriter, r *http.Request, project model.Project, svc model.Service, dep model.Deployment) (codedebug.Session, bool, error) {
	if session, ok := a.codeDebug.LeaseFor(dep.ID); ok {
		return session, false, nil
	}
	plan, err := operation.PlanCodeDebugOpen(project, svc, dep, svc.Language)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid operation")
		return codedebug.Session{}, false, errLeaseUnavailable
	}
	allowed, approval := a.authorizeOperation(w, r, plan)
	if !allowed {
		return codedebug.Session{}, false, errLeaseUnavailable
	}
	session, created, err := a.codeDebug.ResolveLease(r.Context(), project, svc, dep, approvalTokenFromRequest(r))
	if err != nil {
		switch {
		case errors.Is(err, codedebug.ErrAttachUnsupported):
			jsonCodeError(w, http.StatusConflict, "attach_unsupported",
				"this service's language cannot attach to a running process; restart it with mode=debug to debug", nil)
		case errors.Is(err, codedebug.ErrAttachTargetUnresolved):
			jsonCodeError(w, http.StatusConflict, "attach_target_unresolved",
				"could not locate the running process to attach; restart with mode=debug", nil)
		case errors.Is(err, codedebug.ErrRuntimeNotRunning):
			jsonCodeError(w, http.StatusConflict, "runtime_not_running",
				"debug runtime not running; start it with mode=debug first", nil)
		default:
			writeCodeDebugError(w, err)
		}
		if approval != nil {
			a.appendOperationExecutionFailure(r, plan, approval, "failed to resolve code debug lease: "+err.Error())
		}
		return codedebug.Session{}, false, err
	}
	return session, created, nil
}

func ensureCodeDebugResult(result map[string]any) map[string]any {
	if result == nil {
		return map[string]any{}
	}
	return result
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
	case errors.Is(err, codedebug.ErrRuntimeNotRunning):
		jsonCodeError(w, http.StatusConflict, "runtime_not_running", "debug runtime not running; start it with mode=debug first", nil)
	default:
		jsonCodeError(w, http.StatusInternalServerError, "dap_request_failed", err.Error(), nil)
	}
}
