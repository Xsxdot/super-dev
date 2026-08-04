// adoption.go 实现无凭据接入的受审批通道。
//
// 职责：
//   - 保存进程内的接入请求（pending/approved/rejected/expired）及其一次性
//     adoption token
//   - 为接入方 TryCreate 请求（限流判断与创建原子化）、既有控制面
//     Approve/Reject 决策、接入方 Exchange 兑换长期 token 提供并发安全的状态机
//   - 兑换成功后调用 Store.AppendTokenRecord 为接入方追加一条独立的长期凭据
//   - 惰性清扫过期记录，约束 bypass 白名单端点带来的无界内存增长
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
	"crypto/sha256"
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

	// AdoptionNameMaxRunes 是接入方自报展示名的最大字符数。
	//
	// 为什么必须有上限：Create 端点在 bypass 白名单里（调用方此刻没有任何凭据），
	// 自报名会被原样带进 operation plan 的 TargetSummary/ExpectedEffects 并落盘到
	// operation-approvals.json，同时进日志。没有上限就等于给匿名调用方一条
	// 「往本机磁盘写任意长度字符串」的通道。截断只影响展示，不影响任何判定逻辑。
	AdoptionNameMaxRunes = 64

	// AdoptionPairingCodeLength 是配对码的字符数：短到能口头念出来，
	// 长到（32^6 ≈ 10 亿）足以让并发的几条接入请求不会撞码。
	AdoptionPairingCodeLength = 6

	// adoptionPairingCodeAlphabet 是配对码字母表：刻意去掉 I/O/0/1 等口头易混字符。
	adoptionPairingCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
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
//
// 注意：
//   - Name 是接入方**自报**的展示名，不可信；真正可信的是服务器侧推导的来源
//     （见 api.requestOriginLabel）与 PairingCode
type AdoptionRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// PairingCode 由 ID 确定性派生（见 PairingCode），供发起方与批准方口头核对
	// 「是不是同一次请求」。它不是秘密、更不是鉴权因子。
	PairingCode string    `json:"pairing_code"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// PairingCode 由接入请求 ID 确定性派生一个短配对码。
//
// 参数：
//   - requestID: 接入请求 ID（uuid）
//
// 返回：
//   - AdoptionPairingCodeLength 个字符的配对码；同一个 ID 恒得同一个码
//
// 注意：
//   - **配对码不是秘密，也绝不是鉴权因子**：它由请求 ID 单向派生，而请求 ID
//     本身就会明文返回给接入方并展示在审批行上，任何拿到 ID 的人都能算出同一
//     个码。它唯一的作用是让「发起纳管的人」和「按下批准的人」能口头核对是不是
//     同一次请求——堵住「攻击者伪造成 SuperDev Desktop 同名请求鱼目混珠、
//     操作员批错行」的确认代理（confused deputy）漏洞。任何时候都不能拿它做
//     准入校验，也不能用它替代审批本身
func PairingCode(requestID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(requestID)))
	out := make([]byte, 0, AdoptionPairingCodeLength)
	for i := 0; i < AdoptionPairingCodeLength; i++ {
		out = append(out, adoptionPairingCodeAlphabet[int(sum[i])%len(adoptionPairingCodeAlphabet)])
	}
	return string(out)
}

// sanitizeAdoptionName 把接入方自报的展示名收敛成一个可安全展示与落盘的短字符串。
//
// 注意：
//   - 先剥控制字符再截断：自报名会进 log.Printf 的一行式日志，含换行/回车的
//     名字可以伪造出看似独立的日志行（日志注入）；这里统一换成空格
//   - 截断按 rune 而非 byte，避免把多字节字符切成半个导致落盘的 JSON 里出现
//     替换字符
func sanitizeAdoptionName(name string) string {
	replaced := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name)
	trimmed := strings.TrimSpace(replaced)
	if trimmed == "" {
		return defaultTokenRecordName
	}
	runes := []rune(trimmed)
	if len(runes) > AdoptionNameMaxRunes {
		trimmed = strings.TrimSpace(string(runes[:AdoptionNameMaxRunes]))
	}
	if trimmed == "" {
		return defaultTokenRecordName
	}
	return trimmed
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

// TryCreate 原子地判断限流上限并创建一条新的待审批接入请求。
//
// 参数：
//   - name: 接入方（新控制面）自报的展示名，空则默认为「控制面」；
//     统一经 sanitizeAdoptionName 剥控制字符 + 截断到 AdoptionNameMaxRunes
//
// 返回：
//   - 新创建的 pending AdoptionRequest；被限流时为零值
//   - 是否成功创建；false 表示 AdoptionRateLimitWindow 内已有
//     AdoptionRateLimitMax 个 pending 接入请求，调用方（handler_adoption.go
//     的 Create 端点）应据此返回 429，不再重试
//
// 注意：
//   - 限流判断与插入在同一次加锁内完成——这是本方法存在的唯一原因：早期版本
//     把「查限流」和「真正创建」拆成 RateLimited()+Create() 两次独立加锁的
//     调用，中间留出的窗口让并发请求可以全部先通过检查、再各自成功插入，
//     限流形同虚设（这是本文件唯一防骚扰的门槛，因为 Create 端点是 bypass
//     白名单，攻击者此刻没有任何凭据）。调用方必须只用本方法创建，不要再
//     自行组合别的判断+插入两步调用
//   - 顺带惰性清扫已过期的接入请求（见 evictExpiredLocked），把内存增长
//     限制在「窗口内活跃请求数」量级，而不是无界累积
//   - 自报名的收敛（剥控制字符 + 长度上限）放在这里而不是 HTTP handler 里，
//     是为了让**所有**入口都被覆盖：这是唯一的创建通道，收敛在此即无死角
func (m *AdoptionManager) TryCreate(name string) (AdoptionRequest, bool) {
	name = sanitizeAdoptionName(name)
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	m.evictExpiredLocked(now)
	if m.pendingCountWithinWindowLocked(now) >= AdoptionRateLimitMax {
		return AdoptionRequest{}, false
	}
	id := uuid.NewString()
	req := AdoptionRequest{
		ID:          id,
		Name:        name,
		PairingCode: PairingCode(id),
		State:       AdoptionStatePending,
		CreatedAt:   now,
		ExpiresAt:   now.Add(AdoptionRequestTTL),
	}
	m.requests[req.ID] = &adoptionRecord{request: req}
	log.Printf("[SuperDev] security: adoption 接入请求已创建 id=%s code=%s name=%s", req.ID, req.PairingCode, req.Name)
	return req, true
}

// pendingCountWithinWindowLocked 统计 AdoptionRateLimitWindow 内创建、且仍
// 处于 pending 状态的接入请求数；调用方必须已持有 m.mu。
func (m *AdoptionManager) pendingCountWithinWindowLocked(now time.Time) int {
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
	return count
}

// evictExpiredLocked 清理已超过 AdoptionRequestTTL 的接入请求（无论
// pending/approved/rejected），防止 requests map 无界增长；调用方必须已
// 持有 m.mu。
//
// 注意：
//   - 只在每次 TryCreate 时惰性清扫，不额外起定时器 goroutine（YAGNI）——
//     Create 本身就是本文件唯一的增长入口，没有 Create 就没有新的清理压力
//   - 不提前清理仍在有效期内、approved 但尚未被领取/兑换的记录——它们的
//     ExpiresAt 还没到，本来就不会被本函数删除，Get/Exchange 仍能正常命中
//   - 清扫只发生在被 TryCreate 触发时，因此一条请求过期后、在下一次任意
//     TryCreate 调用之前，仍可被 Get 正常读到「state: expired」——这不是
//     竞态，只是清理时机是惰性的，不影响单条请求自身的过期判定（那部分由
//     Approve/Reject/Get/Exchange 各自的时间比较独立保证）
func (m *AdoptionManager) evictExpiredLocked(now time.Time) {
	for id, rec := range m.requests {
		if now.After(rec.request.ExpiresAt) {
			delete(m.requests, id)
		}
	}
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
