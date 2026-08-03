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
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/security"
)

const (
	// maxAdoptionRequestBytes 是两个匿名 adoption 请求体的读取上限。
	//
	// 为什么必须有：这两个端点都在 bypass 白名单里（调用方没有任何凭据），
	// 而请求体内容会被带进落盘的审批记录。没有上限就等于允许任何能连到 agent
	// 端口的人往本机磁盘/内存里灌任意大小的数据。4KiB 远超两个端点合法请求体
	// （一个短名字 / 一个 token）所需。
	maxAdoptionRequestBytes = 4 << 10

	// maxPendingAdoptApprovals 是同时存在的 pending agent.adopt 审批数上限。
	//
	// 为什么在 AdoptionManager 的限流之外还要这一道：那一道只约束
	// AdoptionRateLimitWindow(30s) 内的**并发** pending 接入请求数，拦不住
	// 「低频但持续」的刷单——每 30s 三条，落盘的审批记录就会一直涨，两个控制面
	// 的待审批列表也会被淹没。这道以「当前 pending 的 agent.adopt 审批总数」
	// 为准的硬上限，让匿名调用方在任何时刻最多只能占住有限几行。
	maxPendingAdoptApprovals = 5
)

// adoptionCreateRequest 是 POST /api/security/adoption-requests 的请求体。
type adoptionCreateRequest struct {
	Name string `json:"name"`
}

// adoptionCreateResponse 是 Create 端点的响应。
//
// 注意：
//   - PairingCode 回给接入方，供发起纳管的人向按下批准的人口头核对；
//     它由请求 ID 派生，不是秘密也不是鉴权因子（见 security.PairingCode）
type adoptionCreateResponse struct {
	ID          string    `json:"id"`
	PairingCode string    `json:"pairing_code"`
	State       string    `json:"state"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// requestOriginLabel 从连接的对端地址推导请求来源标签。
//
// 参数：
//   - r: 当前请求
//
// 返回：
//   - 对端 host（去掉临时端口）；取不到时返回 "unknown"
//
// 注意：
//   - 只读 net/http 记录的 RemoteAddr，**绝不**读 X-Forwarded-For 之类的请求头：
//     那些字段由调用方自填，一旦采信就等于把「来源」这唯一的服务器侧事实重新
//     交回给攻击者，本函数存在的意义（给审批行一个不可伪造的身份锚点）就没了
//   - 去掉端口只留 host：临时端口每次连接都变，对人工核对没有信息量
func requestOriginLabel(r *http.Request) string {
	addr := strings.TrimSpace(r.RemoteAddr)
	if addr == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(addr); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return addr
}

// countPendingAdoptApprovals 统计当前仍处于 pending 的 agent.adopt 审批条数。
func (a *App) countPendingAdoptApprovals(ctx context.Context) (int, error) {
	pending, err := a.operationApprovals.List(ctx, operation.ApprovalFilter{Status: operation.ApprovalPending})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, approval := range pending {
		if approval.Plan.Kind == operation.KindAgentAdopt {
			count++
		}
	}
	return count, nil
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
//   - 请求体经 http.MaxBytesReader 限长（maxAdoptionRequestBytes）
//   - 三道防刷叠加：请求体长度上限、AdoptionRateLimitWindow 内的并发 pending
//     上限（AdoptionRateLimitMax）、落盘 pending agent.adopt 审批总数上限
//     （maxPendingAdoptApprovals）
//   - 记入审批的来源（Plan.Target.RequestOrigin）一律取服务器侧的
//     requestOriginLabel，绝不采信请求体里的任何自报地址
func (a *App) createAdoptionRequest(w http.ResponseWriter, r *http.Request) {
	if a.adoptions == nil {
		jsonError(w, http.StatusServiceUnavailable, "adoption unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdoptionRequestBytes)
	var req adoptionCreateRequest
	if err := decodeJSONBody(r, &req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	origin := requestOriginLabel(r)
	// 先看落盘的 pending 总数：AdoptionManager 的窗口限流管不到已经落盘的审批单，
	// 这一道才是「审批列表不被匿名调用方灌满」的持久化侧硬上限。
	pendingAdopt, err := a.countPendingAdoptApprovals(r.Context())
	if err != nil {
		// 与下方 FindOrCreatePending 失败同理：err 可能带出本机路径，不回显。
		log.Printf("[SuperDev] security: adoption 待审批数校验失败 err=%v", err)
		jsonError(w, http.StatusInternalServerError, "adoption request failed")
		return
	}
	if pendingAdopt >= maxPendingAdoptApprovals {
		log.Printf("[SuperDev] security: adoption 待审批上限触发 pending=%d origin=%s", pendingAdopt, origin)
		jsonError(w, http.StatusTooManyRequests, "too many pending adoption requests, try again later")
		return
	}
	// 限流判断与创建必须原子完成（见 TryCreate 注释）——分成两次独立加锁的
	// RateLimited()+Create() 调用会被并发请求绕过，这是本端点唯一的防骚扰门槛。
	adoptionReq, ok := a.adoptions.TryCreate(req.Name)
	if !ok {
		log.Printf("[SuperDev] security: adoption 创建限流触发 origin=%s", origin)
		jsonError(w, http.StatusTooManyRequests, "too many pending adoption requests, try again later")
		return
	}
	plan, err := operation.PlanAgentAdopt(adoptionReq.ID, adoptionReq.Name, origin, adoptionReq.PairingCode)
	if err != nil {
		// requestID 不可能为空（uuid.NewString 生成），本分支理论不可达；
		// 即便如此也不把内部错误文本回显给未持有凭据的匿名调用方。
		log.Printf("[SuperDev] security: adoption plan 生成失败 id=%s err=%v", adoptionReq.ID, err)
		jsonError(w, http.StatusInternalServerError, "adoption request failed")
		return
	}
	// requestedBy 取服务器侧推导的来源（不可伪造），requesterLabel 才是接入方
	// 自报名——两者刻意分开：前者是事实，后者是上下文。绝不再像早期版本那样
	// 两个位置都填自报名，否则整条审批记录里没有一个字段是服务器侧推导的，
	// 攻击者可以把自己伪装成操作员正在等的那一条请求。
	approval, err := a.operationApprovals.FindOrCreatePending(r.Context(), plan, origin, adoptionReq.Name)
	if err != nil {
		// FindOrCreatePending 内部做文件 I/O（operation.ApprovalFileStore），
		// 磁盘/权限故障会带出本机路径等信息——绝不能把 err.Error() 原样回显给
		// 这个 bypass 白名单端点背后未持有任何凭据的匿名调用方，只服务器侧记录。
		log.Printf("[SuperDev] security: adoption 审批单创建失败 id=%s err=%v", adoptionReq.ID, err)
		jsonError(w, http.StatusInternalServerError, "adoption request failed")
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
		ID:          adoptionReq.ID,
		PairingCode: adoptionReq.PairingCode,
		State:       adoptionReq.State,
		ExpiresAt:   adoptionReq.ExpiresAt,
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
		// AdoptionManager.Get 当前只会返回 ErrAdoptionNotFound，本分支理论不
		// 可达；防御性地同样不把内部错误文本回显给匿名调用方（同上）。
		log.Printf("[SuperDev] security: adoption 状态查询失败 id=%s err=%v", id, err)
		jsonError(w, http.StatusInternalServerError, "adoption request failed")
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
//   - 与 Create 端点用同一条解码路径（MaxBytesReader + decodeJSONBody）：
//     两个匿名端点的请求体处理必须一致，不留一个没有长度上限的裸解码口子
func (a *App) exchangeAdoptionRequest(w http.ResponseWriter, r *http.Request) {
	if a.adoptions == nil {
		jsonError(w, http.StatusServiceUnavailable, "adoption unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdoptionRequestBytes)
	var req adoptionExchangeRequest
	if err := decodeJSONBody(r, &req); err != nil {
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
