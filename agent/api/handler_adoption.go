// handler_adoption.go 实现无凭据接入（agent adoption）的 HTTP 端点。
//
// 职责：
//   - 接收接入方的 Create 请求，落一条待审批 operation approval 并广播给
//     既有控制面（复用 Task 5 的 FindOrCreatePending 广播路径）
//   - 提供接入方轮询状态、一次性领取 adoption token 的 Get 端点
//   - 提供接入方用 adoption token 兑换独立长期凭据的 Exchange 端点
//
// 边界：
//   - 不做审批裁决本身——approve/reject 由 handler_operations.go 处理，
//     本文件只负责在 Create 时落单、在 Get/Exchange 时读取
//     security.AdoptionManager 的状态机结果
//   - 不生成或返回长期 token 以外的任何秘密值日志
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/security"
)

// adoptionCreateRequest 是 POST /api/security/adoption-requests 的请求体。
type adoptionCreateRequest struct {
	Name string `json:"name"`
}

// adoptionCreateResponse 是 Create 端点的响应。
type adoptionCreateResponse struct {
	ID        string    `json:"id"`
	State     string    `json:"state"`
	ExpiresAt time.Time `json:"expires_at"`
}

// adoptionStatusResponse 是 GET /api/security/adoption-requests/{id} 的响应。
//
// 注意：AdoptionToken 只在 approved 且本次是首次领取时非空——一次性语义由
// security.AdoptionManager.Get 保证，本层不重复判断。
type adoptionStatusResponse struct {
	State         string `json:"state"`
	AdoptionToken string `json:"adoption_token,omitempty"`
}

// adoptionExchangeRequest 是 exchange 端点的请求体。
type adoptionExchangeRequest struct {
	AdoptionToken string `json:"adoption_token"`
}

// adoptionExchangeResponse 是 exchange 端点的响应。
type adoptionExchangeResponse struct {
	Token  string               `json:"token"`
	Record security.TokenRecord `json:"record"`
}

// createAdoptionRequest 处理 POST /api/security/adoption-requests。
//
// 注意：
//   - bypass 白名单路径，接入方此刻没有任何凭据
//   - 限流：AdoptionRateLimitWindow 内 pending 请求数达到 AdoptionRateLimitMax
//     即拒绝，防止未持有凭据的一方刷屏骚扰既有控制面的审批列表
func (a *App) createAdoptionRequest(w http.ResponseWriter, r *http.Request) {
	if a.adoptions == nil {
		jsonError(w, http.StatusServiceUnavailable, "adoption unavailable")
		return
	}
	var req adoptionCreateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if a.adoptions.RateLimited() {
		log.Printf("[SuperDev] security: adoption 创建限流触发 name=%s", req.Name)
		jsonError(w, http.StatusTooManyRequests, "too many pending adoption requests, try again later")
		return
	}

	adoptionReq := a.adoptions.Create(req.Name)
	plan, err := operation.PlanAgentAdopt(adoptionReq.ID, adoptionReq.Name)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// requester/requesterLabel 都取接入方自报名——这条单没有 X-SuperDev-Requester
	// 之类的 MCP 请求头可用，接入方展示名就是「谁在申请」的唯一可读信息。
	approval, err := a.operationApprovals.FindOrCreatePending(r.Context(), plan, adoptionReq.Name, adoptionReq.Name)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 广播给所有在线控制面：新落的待审批单必须立刻出现在既有控制面的审批列表里，
	// 这就是「对方桌面收到通知」的落点，语义同 authorizeOperation 里的同名调用。
	a.signalApprovalsPublishers()
	a.appendOperationAudit(r.Context(), operation.AuditEvent{
		Kind:       plan.Kind,
		Action:     operation.AuditApprovalRequired,
		ApprovalID: approval.ID,
		Plan:       plan,
		Summary:    "agent adoption requires approval",
	})
	jsonWrite(w, http.StatusCreated, adoptionCreateResponse{
		ID:        adoptionReq.ID,
		State:     adoptionReq.State,
		ExpiresAt: adoptionReq.ExpiresAt,
	})
}

// getAdoptionRequest 处理 GET /api/security/adoption-requests/{id}。
//
// 注意：
//   - bypass 白名单路径；除了「请求不存在」外恒返回 200，approved 状态下
//     adoption_token 只在首次调用时非空，防重放的判定全部下沉在
//     security.AdoptionManager.Get 内部
func (a *App) getAdoptionRequest(w http.ResponseWriter, r *http.Request) {
	if a.adoptions == nil {
		jsonError(w, http.StatusServiceUnavailable, "adoption unavailable")
		return
	}
	id := r.PathValue("id")
	req, token, err := a.adoptions.Get(id)
	if err != nil {
		if errors.Is(err, security.ErrAdoptionNotFound) {
			jsonError(w, http.StatusNotFound, "adoption request not found")
			return
		}
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, adoptionStatusResponse{State: req.State, AdoptionToken: token})
}

// exchangeAdoptionRequest 处理 POST /api/security/adoption-requests/{id}/exchange。
//
// 注意：
//   - bypass 白名单路径；准入凭证是请求体里的一次性 adoption token，不是
//     Bearer 头——这与 provisionSecurity 用 bootstrap token 做准入同理
//   - 一次性：同一 adoption token 第二次兑换恒失败，见
//     security.AdoptionManager.Exchange 的 exchanged 标记
func (a *App) exchangeAdoptionRequest(w http.ResponseWriter, r *http.Request) {
	if a.adoptions == nil {
		jsonError(w, http.StatusServiceUnavailable, "adoption unavailable")
		return
	}
	var req adoptionExchangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	token, record, err := a.adoptions.Exchange(req.AdoptionToken)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrAdoptionTokenConsumed):
			jsonError(w, http.StatusUnauthorized, "adoption token already used")
		case errors.Is(err, security.ErrAdoptionExpired):
			jsonError(w, http.StatusUnauthorized, "adoption request expired")
		default:
			jsonError(w, http.StatusUnauthorized, "adoption token invalid")
		}
		return
	}
	jsonOK(w, adoptionExchangeResponse{Token: token, Record: record})
}
