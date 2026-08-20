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
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/configchange"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/security"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

type operationApprovalDetailResponse struct {
	Approval               operation.Approval `json:"approval"`
	ApprovalToken          string             `json:"approval_token,omitempty"`
	ApprovalTokenExpiresAt *time.Time         `json:"approval_token_expires_at,omitempty"`
}

// operationApprovalDecisionResponse 是 approve 接口的响应。
type operationApprovalDecisionResponse struct {
	Approval       operation.Approval `json:"approval"`
	GraceGranted   bool               `json:"grace_granted"`
	GraceExpiresAt *time.Time         `json:"grace_expires_at,omitempty"`
}

type operationAuditListResponse struct {
	Events []operation.AuditEvent `json:"events"`
	Count  int                    `json:"count"`
}

type operationTargetRequest struct {
	Kind           string                             `json:"kind"`
	ProjectID      string                             `json:"project_id"`
	ProjectName    string                             `json:"project_name"`
	RootPath       string                             `json:"root_path"`
	EnvName        string                             `json:"env_name"`
	ServiceID      string                             `json:"service_id"`
	ServiceName    string                             `json:"service_name"`
	DeploymentID   string                             `json:"deployment_id"`
	DebugSessionID string                             `json:"debug_session_id"`
	ExpressionHash string                             `json:"expression_hash"`
	Path           string                             `json:"path"`
	TemplatePath   string                             `json:"template_path"`
	Project        *configchange.ProjectPatch         `json:"project,omitempty"`
	Service        *configchange.ServicePatch         `json:"service,omitempty"`
	Pipeline       *configchange.ProjectPipelinePatch `json:"pipeline,omitempty"`
	Delete         bool                               `json:"delete,omitempty"`
	Remove         bool                               `json:"remove,omitempty"`
}

// operationDecisionRequest 是 approve/reject 接口的请求体。
//
// 注意：
//   - DecidedBy 字段仅为兼容旧版桌面客户端（历史上恒发字符串 "user"）而保留解析，
//     解析后不会被采用——裁决方身份绝不信任客户端自报，服务器侧一律改用
//     security.PrincipalFrom(r.Context()) 从已验证凭据推导出的展示名，
//     否则审计里永远看不出真实批准人是谁。
type operationDecisionRequest struct {
	DecidedBy  string `json:"decided_by"`
	Note       string `json:"note"`
	GrantGrace bool   `json:"grant_grace"`
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
	// 外来审批（由某台归属机签发）不在本机 store 里，必须路由回源节点处理。
	// 判据取自聚合器当前的来源表，而不是「本机 Get 不到就当外来」——后者会把
	// 一个真实的 404（id 打错、已过期清理）误当成跨机请求发出去。
	if origin := a.approvalOriginOf(id); origin != "" {
		a.forwardToHostWithObserver(w, r, origin, func(body []byte) {
			a.rememberForwardedApprovalToken(origin, body)
		})
		return
	}
	token, approval, err := a.operationApprovals.IssueToken(r.Context(), id)
	if err != nil {
		if errors.Is(err, operation.ErrApprovalTokenInvalid) {
			pending, getErr := a.operationApprovals.Get(r.Context(), id)
			if getErr == nil && pending.Status == operation.ApprovalPending {
				jsonOK(w, operationApprovalDetailResponse{Approval: sanitizeOperationApproval(pending)})
				return
			}
		}
		writeOperationStoreError(w, err, "")
		return
	}
	jsonOK(w, operationApprovalDetailResponse{
		Approval:               sanitizeOperationApproval(approval),
		ApprovalToken:          token,
		ApprovalTokenExpiresAt: approval.TokenExpiresAt,
	})
}

// approveOperationApproval 处理 POST /api/operation-approvals/{id}/approve。
func (a *App) approveOperationApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 外来审批的裁决必须回到签发它的归属机；转发失败时公共路由逻辑返回
	// home_unreachable，不能回落本机 store（本机根本没有这条审批）。
	if origin := a.approvalOriginOf(id); origin != "" {
		if a.forwardToHost(w, r, origin) {
			a.appendApprovalProxyAudit(r.Context(), id, origin, "approve")
		}
		return
	}
	var req operationDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 裁决方身份只认服务器侧已验证凭据推导，绝不信任请求体自报的 decided_by
	// （req.DecidedBy 解析后直接丢弃）——见 operationDecisionRequest 的字段注释。
	decidedByName, principalType, principalID := principalFromRequest(r)
	approval, err := a.operationApprovals.Approve(r.Context(), id, decidedByName, req.Note)
	if err != nil {
		writeOperationStoreError(w, err, a.decisionConflictWinner(r.Context(), id, err, "approve"))
		return
	}
	log.Printf("[SuperDev] approval 裁决 id=%s action=approve by=%s(%s)", approval.ID, decidedByName, principalType)
	// 纳管联动（Task 7）：agent.adopt 的 approval.Plan.Fingerprint 就是
	// AdoptionManager 里对应的接入请求 ID（PlanAgentAdopt 直接以请求 ID 作为
	// fingerprint，见 operation/policy.go 注释），批准后驱动它生成一次性
	// adoption token。失败只记日志不影响本次 approve 请求本身——最典型的失败
	// 场景是 agent 在批准前重启过（AdoptionManager 进程内存态已丢失该请求），
	// 此时接入方轮询 GET 也会拿到 404，会自然引导它重新发起 Create。
	a.hookAgentAdoptDecision(approval, "approve")
	// 广播给所有在线控制面：这条单在其余订阅方眼里应该立刻从 pending 变成灰化的
	// 「已由 X 处理」（decided 段），不必等它们各自的下一次轮询。
	a.signalApprovalsPublishers()
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       approval.Plan.Kind,
		Action:     operation.AuditApproved,
		ApprovalID: approval.ID,
		Plan:       approval.Plan,
		Summary:    "operation approval approved",
		Data:       map[string]any{"decided_by": approval.DecidedBy, "principal_type": principalType, "principal_id": principalID},
	})

	graceGranted := false
	var graceExpiresAt *time.Time
	if req.GrantGrace && approval.Plan.Target.ProjectID != "" {
		minutes := a.loadApprovalPolicy().GraceMinutes
		ttl := time.Duration(minutes) * time.Minute
		if grant, err := a.operationGrace.GrantGrace(r.Context(), approval.Plan.Target.ProjectID, approval.DecidedBy, approval.ID, ttl); err == nil {
			graceGranted = true
			graceExpiresAt = &grant.ExpiresAt
			a.appendOperationAudit(r.Context(), operation.AuditEvent{
				Kind:       approval.Plan.Kind,
				Action:     operation.AuditGraceGranted,
				ApprovalID: approval.ID,
				Plan:       approval.Plan,
				Summary:    "project grace window opened",
				Data:       map[string]any{"project_id": approval.Plan.Target.ProjectID, "expires_at": grant.ExpiresAt},
			})
		}
	}

	jsonOK(w, operationApprovalDecisionResponse{
		Approval:       sanitizeOperationApproval(approval),
		GraceGranted:   graceGranted,
		GraceExpiresAt: graceExpiresAt,
	})
}

// rejectOperationApproval 处理 POST /api/operation-approvals/{id}/reject。
func (a *App) rejectOperationApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 外来审批的裁决必须回到签发它的归属机；转发失败时公共路由逻辑返回
	// home_unreachable，不能回落本机 store（本机根本没有这条审批）。
	if origin := a.approvalOriginOf(id); origin != "" {
		if a.forwardToHost(w, r, origin) {
			a.appendApprovalProxyAudit(r.Context(), id, origin, "reject")
		}
		return
	}
	var req operationDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// 裁决方身份只认服务器侧已验证凭据推导，绝不信任请求体自报的 decided_by
	// （req.DecidedBy 解析后直接丢弃）——见 operationDecisionRequest 的字段注释。
	decidedByName, principalType, principalID := principalFromRequest(r)
	approval, err := a.operationApprovals.Reject(r.Context(), id, decidedByName, req.Note)
	if err != nil {
		writeOperationStoreError(w, err, a.decisionConflictWinner(r.Context(), id, err, "reject"))
		return
	}
	log.Printf("[SuperDev] approval 裁决 id=%s action=reject by=%s(%s)", approval.ID, decidedByName, principalType)
	// 纳管联动（Task 7），语义同 approve 分支，见其注释。
	a.hookAgentAdoptDecision(approval, "reject")
	// 广播给所有在线控制面，语义同 approve 分支。
	a.signalApprovalsPublishers()
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       approval.Plan.Kind,
		Action:     operation.AuditRejected,
		ApprovalID: approval.ID,
		Plan:       approval.Plan,
		Summary:    "operation approval rejected",
		Data:       map[string]any{"decided_by": approval.DecidedBy, "principal_type": principalType, "principal_id": principalID},
	})
	jsonOK(w, sanitizeOperationApproval(approval))
}

// approvalOriginOf 查聚合器当前合并快照里的审批来源。
//
// 为什么不用「本机 Get 不到就当外来」：一个真实的本机 404（id 写错或记录已被
// 清理）不应被误发到远端；只有聚合器明确持有、且项目属于本控制面管辖范围的
// 外来审批才允许回源节点裁决。
func (a *App) approvalOriginOf(id string) string {
	_, hostID, ok := a.remoteApprovalForID(id)
	if !ok {
		return ""
	}
	return hostID
}

func (a *App) remoteApprovalForID(id string) (operation.Approval, string, bool) {
	if id == "" || a.approvalAggregator == nil {
		return operation.Approval{}, "", false
	}
	managed := a.managedProjectIDsByHome()
	for hostID, remote := range a.approvalAggregator.All() {
		allowed := managed[hostID]
		for _, approval := range remote.Snapshot.Pending {
			if approval.ID == id {
				if _, ok := allowed[approval.Plan.Target.ProjectID]; ok {
					return approval, hostID, true
				}
			}
		}
		for _, approval := range remote.Snapshot.Decided {
			if approval.ID == id {
				if _, ok := allowed[approval.Plan.Target.ProjectID]; ok {
					return approval, hostID, true
				}
			}
		}
	}
	return operation.Approval{}, "", false
}

// appendApprovalProxyAudit 在本机记录跨机裁决的发起事实；源节点仍负责记录
// 实际执行的裁决结果，本机这条记录补上「操作员从哪里发起」这一侧的审计链。
func (a *App) appendApprovalProxyAudit(ctx context.Context, id, origin, decision string) {
	approval, _, _ := a.remoteApprovalForID(id)
	event := operation.AuditEvent{
		Kind:       approval.Plan.Kind,
		Action:     operation.AuditApprovalProxied,
		ApprovalID: id,
		Plan:       approval.Plan,
		Summary:    "operation approval decision proxied to origin host",
		Data: map[string]any{
			"origin_host_id": origin,
			"decision":       decision,
		},
	}
	if a.operationAudit == nil {
		log.Printf("[SuperDev] approval 代理审计写入失败 approval_id=%s origin=%s err=operation audit store 未装配", id, origin)
		return
	}
	if _, err := a.operationAudit.Append(ctx, event); err != nil {
		log.Printf("[SuperDev] approval 代理审计写入失败 approval_id=%s origin=%s err=%v", id, origin, err)
	}
}

func (a *App) rememberForwardedApprovalToken(hostID string, body []byte) {
	var detail operationApprovalDetailResponse
	if err := json.Unmarshal(body, &detail); err != nil {
		log.Printf("[SuperDev] approval token 来源登记失败 host=%s err=%v", hostID, err)
		return
	}
	token := strings.TrimSpace(detail.ApprovalToken)
	if token == "" || a.approvalTokenOrigin == nil {
		return
	}
	expiresAt := time.Time{}
	if detail.ApprovalTokenExpiresAt != nil {
		expiresAt = *detail.ApprovalTokenExpiresAt
	} else if detail.Approval.TokenExpiresAt != nil {
		// 兼容尚未提供顶层过期时间的旧 agent；有些旧响应仍会把它放在
		// approval 对象里，最终仍由登记表的 fallback TTL 兜底。
		expiresAt = *detail.Approval.TokenExpiresAt
	}
	a.approvalTokenOrigin.Remember(token, hostID, expiresAt)
}

// hookAgentAdoptDecision 在 KindAgentAdopt 的 approval 被裁决时驱动
// security.AdoptionManager 的状态机（Task 7 的审批联动钩子）。
//
// 参数：
//   - approval: 已完成裁决（approve 或 reject）的 operation approval
//   - action: "approve" 或 "reject"，仅用于日志区分
//
// 注意：
//   - 非 KindAgentAdopt 的 approval 直接跳过，不影响其余 kind 的裁决路径
//   - 钩子失败只记日志，不会让 approve/reject 这个已经成功落盘的裁决本身失败——
//     operation approval 是通用子系统，不能因为纳管这一个消费者的联动失败而回滚
func (a *App) hookAgentAdoptDecision(approval operation.Approval, action string) {
	if approval.Plan.Kind != operation.KindAgentAdopt || a.adoptions == nil {
		return
	}
	requestID := approval.Plan.Fingerprint
	var err error
	switch action {
	case "approve":
		_, err = a.adoptions.Approve(requestID)
	case "reject":
		err = a.adoptions.Reject(requestID)
	}
	if err != nil {
		log.Printf("[SuperDev] adoption 审批联动失败 approval_id=%s action=%s err=%v", approval.ID, action, err)
	}
}

// principalFromRequest 从已验证凭据推导裁决方展示名/类型/ID。
//
// 参数：
//   - r: 已经过 withSecurity 中间件的请求；Principal 挂在其 context 上
//
// 返回：
//   - 展示名、Principal 类型（local/remote）、Principal 稳定 ID
//
// 注意：
//   - Principal 缺失（如测试假件直接调用 handler、绕过 withSecurity 中间件的路径）
//     时三者一律回退 "unknown"，不 panic 也不 401——鉴权判定是 withSecurity 的职责，
//     这里只负责「有 Principal 就用，没有就诚实地说不知道」
func principalFromRequest(r *http.Request) (name string, principalType string, id string) {
	p, ok := security.PrincipalFrom(r.Context())
	if !ok {
		return "unknown", "unknown", "unknown"
	}
	return p.Name, string(p.Type), p.ID
}

// decisionConflictWinner 在裁决冲突（approval 已是终态）时，从 store 现取真正的
// 胜者 DecidedBy，供 409 响应体回显；非冲突错误直接返回空串。
//
// 为什么现取而不是复用请求方自己的身份：409 时"谁赢了"和"这次请求是谁发的"是
// 两码事——本次请求的 Principal 只是败者，只有重新 Get 一次才能读到抢先裁决成功
// 的那一方；Get 失败（理论上不会，Approve/Reject 已经用同一个 id 命中过记录）时
// 宁可返回空串也不 500，避免把一个次要的可观测性缺口升级成整个 409 响应失败。
func (a *App) decisionConflictWinner(ctx context.Context, id string, err error, action string) string {
	if !errors.Is(err, operation.ErrApprovalAlreadyDecided) {
		return ""
	}
	winner := ""
	if existing, getErr := a.operationApprovals.Get(ctx, id); getErr == nil {
		winner = existing.DecidedBy
	}
	log.Printf("[SuperDev] approval 裁决冲突 id=%s action=%s 已由 %s 裁决", id, action, winner)
	return winner
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
		if req.Kind == operation.OperationRuntimeStart {
			plan = a.annotateStartPlanWithCascade(plan, project.ID, dep)
		}
		return plan, http.StatusOK, ""
	case operation.OperationBrowserDebugOpen:
		project, service, dep, status, msg := a.resolveOperationRuntimeTarget(req)
		if status != http.StatusOK {
			return operation.Plan{}, status, msg
		}
		_, targetURL, status, msg := a.resolveBrowserDebugTarget(dep, service, project, req.Path)
		if status != http.StatusOK {
			return operation.Plan{}, status, msg
		}
		plan, err := operation.PlanBrowserDebugOpen(project, service, dep, targetURL)
		if err != nil {
			return operation.Plan{}, http.StatusBadRequest, "invalid operation"
		}
		return plan, http.StatusOK, ""
	case operation.OperationCodeDebugOpen:
		project, service, dep, status, msg := a.resolveOperationRuntimeTarget(req)
		if status != http.StatusOK {
			return operation.Plan{}, status, msg
		}
		plan, err := operation.PlanCodeDebugOpen(project, service, dep, service.Language)
		if err != nil {
			return operation.Plan{}, http.StatusBadRequest, "invalid operation"
		}
		return plan, http.StatusOK, ""
	case operation.OperationCodeDebugEvaluate:
		plan, err := operation.PlanCodeDebugEvaluate(operation.CodeDebugEvaluateRequest{
			ProjectID:      req.ProjectID,
			ProjectName:    req.ProjectName,
			DeploymentID:   req.DeploymentID,
			DebugSessionID: req.DebugSessionID,
			ExpressionHash: req.ExpressionHash,
		})
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
	case operation.OperationBrowserDebugOpen:
		plan.RequiresApproval = policy.BrowserDebugOpen
	case operation.OperationCodeDebugOpen:
		plan.RequiresApproval = policy.CodeDebugOpen
	case operation.OperationCodeDebugEvaluate:
		plan.RequiresApproval = policy.CodeDebugEvaluate
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
		// approved → used 也是一次订阅方可见的状态变更：这条单从「已批准、等着被
		// 执行」变成「已执行完毕」。它和 approve/reject 一样是急切写入且有天然
		// 调用点，因此必须在这里 signal——否则所有控制面的 decided 段会一直显示
		// approved，直到碰巧有别的审批事件把快照顶一次。
		// （expire 没有 signal 点是另一回事：过期是读路径上的懒扫描，没有这样的
		// 急切写入点，见 signalApprovalsPublishers 的说明。）
		a.signalApprovalsPublishers()
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
	// 广播给所有在线控制面：新建的 pending 单（或 FindOrCreatePending 内部顺带
	// 过期的旧单）必须立刻出现在每个订阅方的快照里，不必等它们各自的下一次轮询——
	// 这也是「expire 没有独立 signal 点」的落点：过期只在这条读路径内懒发生，
	// 这一次 signal 已经覆盖了它，不为此另建定时器。
	a.signalApprovalsPublishers()
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

// writeOperationStoreError 把 operation store 的错误映射为稳定的 HTTP 状态码 + code。
//
// 参数：
//   - decidedBy: 仅 ErrApprovalAlreadyDecided 分支使用，回显真正裁决成功的胜者身份，
//     供客户端展示"这单已经被谁处理了"；其余错误分支忽略该参数，调用方可传空串
func writeOperationStoreError(w http.ResponseWriter, err error, decidedBy string) {
	switch {
	case errors.Is(err, operation.ErrApprovalNotFound):
		jsonCodeError(w, http.StatusNotFound, "approval_not_found", "approval not found", nil)
	case errors.Is(err, operation.ErrApprovalAlreadyDecided):
		// 409：approved 是终态，第二个控制面的裁决请求（无论 approve 还是 reject）
		// 都不能覆盖或翻案第一个裁决者，见 operation.ensureApprovalDecisionAllowed。
		jsonCodeError(w, http.StatusConflict, "approval_already_decided", "approval already decided", map[string]any{"decided_by": decidedBy})
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
