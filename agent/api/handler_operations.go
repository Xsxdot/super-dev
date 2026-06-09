// handler_operations.go 实现 MCP 写操作的安全门禁 HTTP 处理器。
//
// 职责：
//   - 提供 operation preflight、审批、拒绝、token 发放和审计查询
//   - 为运行态和模板写操作提供统一 authorizer
//   - 生成结构化错误，便于 MCP 保留 plan/approval 上下文
//
// 边界：
//   - 不执行进程启停或模板导入
//   - 不直接暴露本机 store 文件路径
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/configchange"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

type operationApprovalDetailResponse struct {
	Approval      operation.Approval `json:"approval"`
	ApprovalToken string             `json:"approval_token,omitempty"`
}

type operationAuditListResponse struct {
	Events []operation.AuditEvent `json:"events"`
	Count  int                    `json:"count"`
}

type operationTargetRequest struct {
	Kind         string                             `json:"kind"`
	ProjectID    string                             `json:"project_id"`
	ProjectName  string                             `json:"project_name"`
	RootPath     string                             `json:"root_path"`
	EnvName      string                             `json:"env_name"`
	ServiceID    string                             `json:"service_id"`
	ServiceName  string                             `json:"service_name"`
	DeploymentID string                             `json:"deployment_id"`
	TemplatePath string                             `json:"template_path"`
	Project      *configchange.ProjectPatch         `json:"project,omitempty"`
	Service      *configchange.ServicePatch         `json:"service,omitempty"`
	Pipeline     *configchange.ProjectPipelinePatch `json:"pipeline,omitempty"`
	Delete       bool                               `json:"delete,omitempty"`
	Remove       bool                               `json:"remove,omitempty"`
}

type operationDecisionRequest struct {
	DecidedBy string `json:"decided_by"`
	Note      string `json:"note"`
}

type operationApprovalTokenBody struct {
	ApprovalToken string `json:"approval_token"`
}

// preflightOperation 处理 POST /api/operations/preflight。
func (a *App) preflightOperation(w http.ResponseWriter, r *http.Request) {
	var req operationTargetRequest
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	plan, status, msg := a.planOperation(req)
	if status != http.StatusOK {
		jsonError(w, status, msg)
		return
	}
	jsonOK(w, plan)
}

// listOperationApprovals 处理 GET /api/operation-approvals。
func (a *App) listOperationApprovals(w http.ResponseWriter, r *http.Request) {
	limit, err := parseOperationLimit(r.URL.Query().Get("limit"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	approvals, err := a.operationApprovals.List(r.Context(), operation.ApprovalFilter{
		Status:    r.URL.Query().Get("status"),
		ProjectID: r.URL.Query().Get("project_id"),
		Limit:     limit,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, sanitizeOperationApprovals(approvals))
}

// getOperationApproval 处理 GET /api/operation-approvals/{id}。
func (a *App) getOperationApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	token, approval, err := a.operationApprovals.IssueToken(r.Context(), id)
	if err != nil {
		if errors.Is(err, operation.ErrApprovalTokenInvalid) {
			pending, getErr := a.operationApprovals.Get(r.Context(), id)
			if getErr == nil && pending.Status == operation.ApprovalPending {
				jsonOK(w, operationApprovalDetailResponse{Approval: sanitizeOperationApproval(pending)})
				return
			}
		}
		writeOperationStoreError(w, err)
		return
	}
	jsonOK(w, operationApprovalDetailResponse{Approval: sanitizeOperationApproval(approval), ApprovalToken: token})
}

// approveOperationApproval 处理 POST /api/operation-approvals/{id}/approve。
func (a *App) approveOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req operationDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	approval, err := a.operationApprovals.Approve(r.Context(), r.PathValue("id"), req.DecidedBy, req.Note)
	if err != nil {
		writeOperationStoreError(w, err)
		return
	}
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       approval.Plan.Kind,
		Action:     operation.AuditApproved,
		ApprovalID: approval.ID,
		Plan:       approval.Plan,
		Summary:    "operation approval approved",
		Data:       map[string]any{"decided_by": approval.DecidedBy},
	})
	jsonOK(w, sanitizeOperationApproval(approval))
}

// rejectOperationApproval 处理 POST /api/operation-approvals/{id}/reject。
func (a *App) rejectOperationApproval(w http.ResponseWriter, r *http.Request) {
	var req operationDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	approval, err := a.operationApprovals.Reject(r.Context(), r.PathValue("id"), req.DecidedBy, req.Note)
	if err != nil {
		writeOperationStoreError(w, err)
		return
	}
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       approval.Plan.Kind,
		Action:     operation.AuditRejected,
		ApprovalID: approval.ID,
		Plan:       approval.Plan,
		Summary:    "operation approval rejected",
		Data:       map[string]any{"decided_by": approval.DecidedBy},
	})
	jsonOK(w, sanitizeOperationApproval(approval))
}

// listOperationAudit 处理 GET /api/operation-audit。
func (a *App) listOperationAudit(w http.ResponseWriter, r *http.Request) {
	limit, err := parseOperationLimit(r.URL.Query().Get("limit"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid limit")
		return
	}
	since, err := parseOperationSince(r.URL.Query().Get("since"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid since")
		return
	}
	events, err := a.operationAudit.List(r.Context(), operation.AuditFilter{
		ProjectID:  r.URL.Query().Get("project_id"),
		Kind:       r.URL.Query().Get("kind"),
		ApprovalID: r.URL.Query().Get("approval_id"),
		Since:      since,
		Limit:      limit,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, operationAuditListResponse{Events: events, Count: len(events)})
}

func (a *App) planOperation(req operationTargetRequest) (operation.Plan, int, string) {
	req.Kind = strings.TrimSpace(req.Kind)
	switch req.Kind {
	case operation.OperationRuntimeStart, operation.OperationRuntimeStop, operation.OperationRuntimeRestart:
		project, service, dep, status, msg := a.resolveOperationRuntimeTarget(req)
		if status != http.StatusOK {
			return operation.Plan{}, status, msg
		}
		plan, err := operation.PlanRuntime(req.Kind, project, service, dep)
		if err != nil {
			return operation.Plan{}, http.StatusBadRequest, "invalid operation"
		}
		return plan, http.StatusOK, ""
	case operation.OperationTemplateImport:
		plan, err := a.planTemplateImport(req.TemplatePath)
		if err != nil {
			return operation.Plan{}, http.StatusBadRequest, err.Error()
		}
		return plan, http.StatusOK, ""
	case operation.OperationConfigProjectUpsert, operation.OperationConfigServiceUpsert, operation.OperationConfigPipelineUpsert:
		preview, status, msg := a.previewConfigChangeRequest(configchange.ChangeRequest{
			Kind:        req.Kind,
			ProjectID:   req.ProjectID,
			ProjectName: req.ProjectName,
			RootPath:    req.RootPath,
			Project:     req.Project,
			Service:     req.Service,
			Pipeline:    req.Pipeline,
			Delete:      req.Delete,
			Remove:      req.Remove,
		})
		if status != http.StatusOK {
			return operation.Plan{}, status, msg
		}
		return preview.Plan, http.StatusOK, ""
	default:
		return operation.Plan{}, http.StatusBadRequest, "invalid operation kind"
	}
}

func (a *App) planTemplateImport(path string) (operation.Plan, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return operation.Plan{}, operation.ErrInvalidOperation
	}
	preview := pipelinetemplate.PreviewFile(path)
	if len(preview.Errors) > 0 {
		return operation.Plan{}, errors.New(strings.Join(preview.Errors, "; "))
	}
	return operation.PlanTemplateImport(operation.TemplateImportRequest{
		Path:   path,
		Digest: preview.Digest,
		Summary: operation.TemplateSummary{
			Source:  "user",
			ID:      preview.Template.ID,
			Name:    preview.Template.Name,
			Version: preview.Template.Version,
			Digest:  preview.Digest,
		},
	})
}

func (a *App) resolveOperationRuntimeTarget(req operationTargetRequest) (model.Project, model.Service, model.Deployment, int, string) {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.EnvName = strings.TrimSpace(req.EnvName)
	req.ServiceID = strings.TrimSpace(req.ServiceID)
	req.ServiceName = strings.TrimSpace(req.ServiceName)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)

	a.mu.RLock()
	defer a.mu.RUnlock()

	type candidate struct {
		project model.Project
		service model.Service
		dep     model.Deployment
	}
	candidates := make([]candidate, 0, 1)
	for _, project := range a.projects {
		if req.ProjectID != "" && project.ID != req.ProjectID {
			continue
		}
		if req.ProjectName != "" && project.Name != req.ProjectName {
			continue
		}
		for _, service := range project.Services {
			if req.ServiceID != "" && service.ID != req.ServiceID {
				continue
			}
			if req.ServiceName != "" && service.Name != req.ServiceName {
				continue
			}
			for _, dep := range service.Deployments {
				if req.DeploymentID != "" && dep.ID != req.DeploymentID {
					continue
				}
				if req.EnvName != "" && dep.EnvName != req.EnvName {
					continue
				}
				candidates = append(candidates, candidate{project: project, service: service, dep: dep})
			}
		}
	}
	if len(candidates) == 0 {
		return model.Project{}, model.Service{}, model.Deployment{}, http.StatusNotFound, "deployment not found"
	}
	if len(candidates) > 1 {
		return model.Project{}, model.Service{}, model.Deployment{}, http.StatusBadRequest, "operation target is ambiguous"
	}
	return candidates[0].project, candidates[0].service, candidates[0].dep, http.StatusOK, ""
}

// applyApprovalPolicy 用 agent 设置覆盖 plan 的审批要求。
//
// 注意：
//   - Denied 的 plan 原样返回，开关无权放行被安全策略禁止的操作
//   - runtime.* 不在覆盖范围，dev 启停逻辑保持代码写死
func applyApprovalPolicy(plan operation.Plan, policy config.ApprovalPolicy) operation.Plan {
	if plan.Denied {
		return plan
	}
	switch plan.Kind {
	case operation.OperationConfigProjectUpsert, operation.OperationConfigServiceUpsert:
		plan.RequiresApproval = policy.ConfigUpsert
	case operation.OperationConfigPipelineUpsert:
		plan.RequiresApproval = policy.PipelineUpsert
	case operation.OperationPipelineRun:
		plan.RequiresApproval = policy.PipelineRun
	case operation.OperationTemplateImport:
		plan.RequiresApproval = policy.TemplateImport
	}
	return plan
}

// loadApprovalPolicy 读取当前审批策略；读取失败时回退默认策略（保持必审）。
func (a *App) loadApprovalPolicy() config.ApprovalPolicy {
	settings, err := a.settings.Load()
	if err != nil {
		return config.DefaultAgentSettings().Approval
	}
	return settings.Approval
}

func (a *App) authorizeOperation(w http.ResponseWriter, r *http.Request, plan operation.Plan) (bool, *operation.Approval) {
	plan = applyApprovalPolicy(plan, a.loadApprovalPolicy())
	if plan.Denied {
		a.appendOperationAudit(r.Context(), operation.AuditEvent{
			Kind:    plan.Kind,
			Action:  operation.AuditFailed,
			Plan:    plan,
			Summary: "operation denied by safety policy",
			Data:    map[string]any{"reasons": plan.Reasons},
		})
		jsonCodeError(w, http.StatusForbidden, "operation_denied", "operation denied", map[string]any{
			"plan":     plan,
			"approval": nil,
		})
		return false, nil
	}

	if !plan.RequiresApproval {
		a.appendOperationAudit(r.Context(), operation.AuditEvent{
			Kind:    plan.Kind,
			Action:  operation.AuditExecuted,
			Plan:    plan,
			Summary: "operation allowed without approval",
		})
		return true, nil
	}

	// 项目级豁免窗口：命中则直接放行，无需 token。
	if plan.Target.ProjectID != "" {
		if grant, ok, err := a.operationGrace.ActiveGrace(r.Context(), plan.Target.ProjectID); err == nil && ok {
			a.appendOperationAudit(r.Context(), operation.AuditEvent{
				Kind:    plan.Kind,
				Action:  operation.AuditApprovedByGrace,
				Plan:    plan,
				Summary: "operation allowed by project grace window",
				Data:    map[string]any{"grace_from": grant.GrantedFrom, "grace_expires_at": grant.ExpiresAt},
			})
			return true, nil
		}
	}

	if token := approvalTokenFromRequest(r); token != "" {
		approval, err := a.operationApprovals.ConsumeToken(r.Context(), token, plan.Fingerprint)
		if err != nil {
			writeApprovalTokenError(w, err, plan)
			return false, nil
		}
		a.appendOperationAudit(r.Context(), operation.AuditEvent{
			Kind:       plan.Kind,
			Action:     operation.AuditExecuted,
			ApprovalID: approval.ID,
			Plan:       plan,
			Summary:    "operation approved and token consumed",
		})
		return true, &approval
	}

	approval, err := a.operationApprovals.FindOrCreatePending(r.Context(), plan, operationRequester(r), operationRequesterLabel(r))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return false, nil
	}
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       plan.Kind,
		Action:     operation.AuditApprovalRequired,
		ApprovalID: approval.ID,
		Plan:       plan,
		Summary:    "operation requires approval",
	})
	jsonCodeError(w, http.StatusForbidden, "approval_required", "approval required", map[string]any{
		"plan":     plan,
		"approval": sanitizeOperationApproval(approval),
	})
	return false, &approval
}

func jsonCodeError(w http.ResponseWriter, status int, code string, msg string, data map[string]any) {
	payload := map[string]any{
		"code":  code,
		"error": msg,
	}
	for key, value := range data {
		payload[key] = value
	}
	jsonWrite(w, status, payload)
}

func writeOperationStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, operation.ErrApprovalNotFound):
		jsonCodeError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
	case errors.Is(err, operation.ErrApprovalRejected):
		jsonCodeError(w, http.StatusForbidden, "approval_rejected", "approval rejected", nil)
	case errors.Is(err, operation.ErrApprovalExpired):
		jsonCodeError(w, http.StatusForbidden, "approval_expired", "approval expired", nil)
	case errors.Is(err, operation.ErrApprovalTokenConsumed):
		jsonCodeError(w, http.StatusForbidden, "approval_token_consumed", "approval token already used", nil)
	case errors.Is(err, operation.ErrApprovalTokenInvalid):
		jsonCodeError(w, http.StatusForbidden, "approval_token_invalid", "approval token invalid", nil)
	default:
		jsonError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeApprovalTokenError(w http.ResponseWriter, err error, plan operation.Plan) {
	code := "approval_token_invalid"
	msg := "approval token invalid"
	switch {
	case errors.Is(err, operation.ErrApprovalRejected):
		code = "approval_rejected"
		msg = "approval rejected"
	case errors.Is(err, operation.ErrApprovalExpired):
		code = "approval_expired"
		msg = "approval expired"
	case errors.Is(err, operation.ErrApprovalTokenConsumed):
		code = "approval_token_consumed"
		msg = "approval token already used"
	}
	jsonCodeError(w, http.StatusForbidden, code, msg, map[string]any{"plan": plan})
}

func approvalTokenFromRequest(r *http.Request) string {
	if token := strings.TrimSpace(r.Header.Get("X-SuperDev-Approval-Token")); token != "" {
		return token
	}
	if r.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if len(raw) == 0 {
		return ""
	}
	var body operationApprovalTokenBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return ""
	}
	return strings.TrimSpace(body.ApprovalToken)
}

func decodeJSONPreserveBody(r *http.Request, out any) error {
	if r.Body == nil {
		return io.EOF
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if len(raw) == 0 {
		return io.EOF
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return nil
}

func operationRequester(r *http.Request) string {
	if requester := strings.TrimSpace(r.Header.Get("X-SuperDev-Requester")); requester != "" {
		return requester
	}
	return "mcp"
}

func operationRequesterLabel(r *http.Request) string {
	if label := strings.TrimSpace(r.Header.Get("X-SuperDev-Requester-Label")); label != "" {
		return label
	}
	return "Codex"
}

func sanitizeOperationApprovals(approvals []operation.Approval) []operation.Approval {
	out := make([]operation.Approval, 0, len(approvals))
	for _, approval := range approvals {
		out = append(out, sanitizeOperationApproval(approval))
	}
	return out
}

func sanitizeOperationApproval(approval operation.Approval) operation.Approval {
	approval.TokenHash = ""
	approval.TokenIssuedAt = nil
	approval.TokenExpiresAt = nil
	return approval
}

func (a *App) appendOperationAudit(ctx context.Context, event operation.AuditEvent) {
	_, _ = a.operationAudit.Append(ctx, event)
}

func (a *App) appendOperationExecutionFailure(r *http.Request, plan operation.Plan, approval *operation.Approval, summary string) {
	event := operation.AuditEvent{
		Kind:    plan.Kind,
		Action:  operation.AuditFailed,
		Plan:    plan,
		Summary: summary,
	}
	if approval != nil {
		event.ApprovalID = approval.ID
	}
	a.appendOperationAudit(r.Context(), event)
}

func parseOperationLimit(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0, errors.New("invalid limit")
	}
	return limit, nil
}

func parseOperationSince(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}
