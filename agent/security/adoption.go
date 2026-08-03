// adoption.go 实现无凭据接入的受审批通道。
//
// 职责：
//   - 保存进程内的接入请求（pending/approved/rejected/expired）及其一次性
//     adoption token
//   - 为接入方 Create 请求、既有控制面 Approve/Reject 决策、接入方 Exchange
//     兑换长期 token 提供并发安全的状态机
//   - 兑换成功后调用 Store.AppendTokenRecord 为接入方追加一条独立的长期凭据
//
// 边界：
//   - 不落盘：AdoptionManager 是进程内存态，agent 重启即丢失全部接入请求，
//     接入方重新发起 Create 即可，不为此加持久化
//   - 不重装、不吊销、不触碰任何既有控制面已持有的凭据——纳管与重装的唯一
//     区别就在这里，Exchange 只会追加一条新记录（Store.AppendTokenRecord
//     语义），绝不覆盖或删除已有 TokenRecord
//   - 不注册 HTTP 路由，也不做审批联动的路由决策——handler_adoption.go 负责
//     HTTP 层，handler_operations.go 负责在 approve/reject 时回调本管理器
package security

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// AdoptionStatePending 表示接入请求已创建，等待既有控制面审批。
	AdoptionStatePending = "pending"
	// AdoptionStateApproved 表示接入请求已批准，adoption token 已生成待接入方领取。
	AdoptionStateApproved = "approved"
	// AdoptionStateRejected 表示接入请求已被既有控制面拒绝。
	AdoptionStateRejected = "rejected"
	// AdoptionStateExpired 表示接入请求超过有效期仍未完成审批/兑换。
	AdoptionStateExpired = "expired"

	// AdoptionRequestTTL 是接入请求从创建到必须完成审批+兑换全过程的有效期。
	// 单一时钟覆盖整条链路（不为 adoption token 再设独立 TTL），足够简单也
	// 足够安全：超时后任何一步（Approve/Reject/Get/Exchange）都会判定为过期，
	// 接入方只需重新 Create 即可，不构成额外攻击面。
	AdoptionRequestTTL = 10 * time.Minute
	// AdoptionRateLimitWindow 是 Create 限流的滑动窗口。
	AdoptionRateLimitWindow = 30 * time.Second
	// AdoptionRateLimitMax 是 AdoptionRateLimitWindow 窗口内允许存在的最大 pending 接入请求数。
	AdoptionRateLimitMax = 3
)

var (
	// ErrAdoptionNotFound 表示接入请求 ID 不存在。
	ErrAdoptionNotFound = errors.New("adoption request not found")
	// ErrAdoptionExpired 表示接入请求已超过 AdoptionRequestTTL。
	ErrAdoptionExpired = errors.New("adoption request expired")
	// ErrAdoptionRejected 表示接入请求已被拒绝。
	ErrAdoptionRejected = errors.New("adoption request rejected")
	// ErrAdoptionAlreadyDecided 表示接入请求已经处于终态（approved），不可再次裁决。
	ErrAdoptionAlreadyDecided = errors.New("adoption request already decided")
	// ErrAdoptionTokenInvalid 表示 adoption token 不匹配任何已批准的接入请求。
	ErrAdoptionTokenInvalid = errors.New("adoption token invalid")
	// ErrAdoptionTokenConsumed 表示 adoption token 已经被兑换过一次。
	ErrAdoptionTokenConsumed = errors.New("adoption token already used")
)

// AdoptionRequest 是一条无凭据接入请求的可对外展示状态。
type AdoptionRequest struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// adoptionRecord 是接入请求的内部记录，额外持有一次性 adoption token 的相关状态。
type adoptionRecord struct {
	request AdoptionRequest
	// plaintextToken 只在 approved 后、被第一次 Get 取走前短暂持有；
	// 取走后立即清空明文，只留 adoptionHash 供 Exchange 校验。
	plaintextToken string
	// tokenIssued 为 true 表示 Get 已经把 plaintextToken 交给过调用方一次，
	// 之后的 Get 只回显 state，不再重复给出 token（防重放）。
	tokenIssued bool
	// adoptionHash 是 adoption token 的 sha256（复用 store.go 的 hash/verifyHash），
	// approved 后写入、Exchange 命中后保留原值仅靠 exchanged 标记拒绝二次兑换。
	adoptionHash string
	// exchanged 为 true 表示该 adoption token 已经成功兑换过一次长期 token。
	exchanged bool
}

// AdoptionManagerOptions 定义 AdoptionManager 的可选注入。
type AdoptionManagerOptions struct {
	// Now 仅用于测试注入时钟；生产环境为 nil 时使用 time.Now。
	Now func() time.Time
}

// AdoptionManager 是无凭据接入请求的进程内存态管理器。
//
// 注意：
//   - 重启即丢失全部接入请求（含已 approved 但尚未 Exchange 的），接入方
//     重新发起 Create 即可，本管理器不做持久化
//   - 与 Store 组合而非继承：Exchange 成功后只调用 Store.AppendTokenRecord
//     追加一条记录，不触碰 Store 已有的任何 TokenRecord
type AdoptionManager struct {
	mu       sync.Mutex
	now      func() time.Time
	store    *Store
	requests map[string]*adoptionRecord
}

// NewAdoptionManager 创建一个空的接入请求管理器。
//
// 参数：
//   - store: 兑换成功后追加长期凭据记录的目标 Store，不能为 nil
//   - opts: 可选时钟注入
func NewAdoptionManager(store *Store, opts AdoptionManagerOptions) *AdoptionManager {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &AdoptionManager{now: now, store: store, requests: map[string]*adoptionRecord{}}
}

// Create 创建一条新的待审批接入请求。
//
// 参数：
//   - name: 接入方（新控制面）自报的展示名，空则默认为「控制面」
//
// 返回：
//   - 新创建的 pending AdoptionRequest
//
// 注意：
//   - 调用方（handler_adoption.go 的 Create 端点）必须先经 RateLimited 判断
//     是否已达到 30s 内 3 个 pending 的上限，本方法本身不做限流拒绝
func (m *AdoptionManager) Create(name string) AdoptionRequest {
	name = strings.TrimSpace(name)
	if name == "" {
		name = defaultTokenRecordName
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	req := AdoptionRequest{
		ID:        uuid.NewString(),
		Name:      name,
		State:     AdoptionStatePending,
		CreatedAt: now,
		ExpiresAt: now.Add(AdoptionRequestTTL),
	}
	m.requests[req.ID] = &adoptionRecord{request: req}
	log.Printf("[SuperDev] security: adoption 接入请求已创建 id=%s name=%s", req.ID, req.Name)
	return req
}

// RateLimited 判断当前是否已达到 Create 的限流上限。
//
// 返回：
//   - AdoptionRateLimitWindow 窗口内创建、且仍处于 pending 状态的接入请求数
//     达到 AdoptionRateLimitMax 时返回 true
//
// 注意：
//   - 接入方此刻没有任何凭据，Create 端点是 bypass 白名单的一部分——这里的
//     限流就是防骚扰的唯一门槛，必须在调用 Create 前先检查
func (m *AdoptionManager) RateLimited() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	count := 0
	for _, rec := range m.requests {
		if rec.request.State != AdoptionStatePending {
			continue
		}
		if now.After(rec.request.ExpiresAt) {
			continue
		}
		if now.Sub(rec.request.CreatedAt) <= AdoptionRateLimitWindow {
			count++
		}
	}
	return count >= AdoptionRateLimitMax
}

// Approve 批准一条 pending 接入请求，生成一次性 adoption token（只落 hash）。
//
// 参数：
//   - id: 接入请求 ID（即 approval.Plan.Fingerprint）
//
// 返回：
//   - adoption token 明文——调用方（handler_operations.go 的 approve 钩子）
//     不得记录该返回值，它只是内部生成瞬间的副产物；真正的领取通道是
//     接入方稍后调用的 Get，Get 只会成功返回一次
//   - 请求不存在、已过期或已处于终态时返回相应错误
func (m *AdoptionManager) Approve(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.requests[strings.TrimSpace(id)]
	if !ok {
		return "", ErrAdoptionNotFound
	}
	now := m.now().UTC()
	if now.After(rec.request.ExpiresAt) {
		rec.request.State = AdoptionStateExpired
		return "", ErrAdoptionExpired
	}
	switch rec.request.State {
	case AdoptionStatePending:
	case AdoptionStateApproved:
		return "", ErrAdoptionAlreadyDecided
	case AdoptionStateRejected:
		return "", ErrAdoptionRejected
	default:
		return "", ErrAdoptionExpired
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	rec.plaintextToken = token
	rec.adoptionHash = hash(token)
	rec.request.State = AdoptionStateApproved
	log.Printf("[SuperDev] security: adoption 接入请求已批准 id=%s name=%s", rec.request.ID, rec.request.Name)
	return token, nil
}

// Reject 拒绝一条 pending 接入请求。
//
// 参数：
//   - id: 接入请求 ID（即 approval.Plan.Fingerprint）
//
// 返回：
//   - 请求不存在、已过期或已处于终态时返回相应错误
func (m *AdoptionManager) Reject(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.requests[strings.TrimSpace(id)]
	if !ok {
		return ErrAdoptionNotFound
	}
	now := m.now().UTC()
	if now.After(rec.request.ExpiresAt) {
		rec.request.State = AdoptionStateExpired
		return ErrAdoptionExpired
	}
	switch rec.request.State {
	case AdoptionStatePending:
	case AdoptionStateApproved:
		return ErrAdoptionAlreadyDecided
	case AdoptionStateRejected:
		return ErrAdoptionRejected
	default:
		return ErrAdoptionExpired
	}
	rec.request.State = AdoptionStateRejected
	log.Printf("[SuperDev] security: adoption 接入请求已拒绝 id=%s name=%s", rec.request.ID, rec.request.Name)
	return nil
}

// Get 查询接入请求当前状态；approved 且尚未被领取过时一并带出一次性 adoption token 明文。
//
// 参数：
//   - id: 接入请求 ID
//
// 返回：
//   - 当前请求状态快照
//   - adoption token 明文；仅在 approved 且本次调用是首次领取时非空，
//     领取后立即清空内部明文并标记已发放，此后的 Get 恒不再带出 token（防重放）
//   - 请求不存在时返回 ErrAdoptionNotFound；其余状态（pending/rejected/expired）
//     不视为错误，只是 token 恒为空
func (m *AdoptionManager) Get(id string) (AdoptionRequest, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.requests[strings.TrimSpace(id)]
	if !ok {
		return AdoptionRequest{}, "", ErrAdoptionNotFound
	}
	now := m.now().UTC()
	if (rec.request.State == AdoptionStatePending || rec.request.State == AdoptionStateApproved) &&
		now.After(rec.request.ExpiresAt) {
		rec.request.State = AdoptionStateExpired
	}
	if rec.request.State != AdoptionStateApproved || rec.tokenIssued {
		return rec.request, "", nil
	}
	token := rec.plaintextToken
	rec.plaintextToken = ""
	rec.tokenIssued = true
	return rec.request, token, nil
}

// Exchange 用一次性 adoption token 兑换独立的长期控制面凭据。
//
// 参数：
//   - adoptionToken: 接入方从 Get 领取到的一次性 token 明文
//
// 返回：
//   - 新生成的长期 token 明文（调用方需立即下发给接入方，不得记录）
//   - 追加到 Store 的 TokenRecord（只含 hash，可安全记录 ID/Name）
//   - token 无效、已过期或已被兑换过时返回相应错误
//
// 注意：
//   - 只调用 Store.AppendTokenRecord 追加一条新记录，绝不吊销或覆盖 Store
//     中任何既有 TokenRecord——这是纳管与「重装覆盖凭据」的根本区别
func (m *AdoptionManager) Exchange(adoptionToken string) (string, TokenRecord, error) {
	token := strings.TrimSpace(adoptionToken)
	if token == "" {
		return "", TokenRecord{}, ErrAdoptionTokenInvalid
	}

	m.mu.Lock()
	var target *adoptionRecord
	for _, rec := range m.requests {
		if verifyHash(rec.adoptionHash, token) {
			target = rec
			break
		}
	}
	if target == nil {
		m.mu.Unlock()
		return "", TokenRecord{}, ErrAdoptionTokenInvalid
	}
	now := m.now().UTC()
	if now.After(target.request.ExpiresAt) {
		target.request.State = AdoptionStateExpired
		m.mu.Unlock()
		return "", TokenRecord{}, ErrAdoptionExpired
	}
	if target.exchanged {
		m.mu.Unlock()
		return "", TokenRecord{}, ErrAdoptionTokenConsumed
	}
	// 提前标记 exchanged，持锁期间完成一次性语义的裁决，避免并发 Exchange
	// 同一 token 时两个 goroutine 都读到「尚未兑换」而各自签发一份长期凭据。
	target.exchanged = true
	name := target.request.Name
	m.mu.Unlock()

	longToken, err := GenerateToken()
	if err != nil {
		return "", TokenRecord{}, err
	}
	// 复用 Task 1 的追加通路：只落一条新记录的 hash，不影响 Store 中任何既有凭据。
	record, err := m.store.AppendTokenRecord(name, longToken)
	if err != nil {
		// 注意：此处失败会让本次一次性 token 白白作废（exchanged 已置位），
		// 属于极少发生的磁盘 I/O 故障；接入方重新走一遍 Create 即可恢复，
		// 不为这个边缘场景增加回滚复杂度。
		return "", TokenRecord{}, err
	}
	log.Printf("[SuperDev] security: adoption 兑换成功，凭据已签发给控制面 %s id=%s", name, record.ID)
	return longToken, record, nil
}
