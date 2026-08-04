// Package security_test 验证无凭据接入（agent adoption）的状态机。
package security_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

// testClock 提供测试可控的时钟，避免真实等待 10 分钟的接入请求有效期。
type testClock struct{ now time.Time }

func (c *testClock) Now() time.Time { return c.now }
func (c *testClock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

// mustTryCreate 是 TryCreate 的测试便利包装：断言未被限流并返回创建结果，
// 简化"确定不会命中限流"的既有用例。
func mustTryCreate(t *testing.T, mgr *security.AdoptionManager, name string) security.AdoptionRequest {
	t.Helper()
	req, ok := mgr.TryCreate(name)
	require.True(t, ok, "本次 TryCreate 不应被限流")
	return req
}

// TestAdoptionManager_FullHappyPath 覆盖 brief Step1 的全链路：
// Create → 既有控制面 Approve → 接入方 Get 拿 adoption_token → Exchange 拿长期
// token → VerifyTokenPrincipal 命中且 Name 正确。同时断言纳管成功后原控制面
// （预先 AppendTokenRecord 的记录）的凭据仍然有效——这是纳管区别于重装的核心。
func TestAdoptionManager_FullHappyPath(t *testing.T) {
	store := newTestStore(t)
	existing, err := store.AppendTokenRecord("既有控制面", "existing-secret-token")
	require.NoError(t, err)

	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	req := mustTryCreate(t, mgr, "新控制面")
	assert.Equal(t, security.AdoptionStatePending, req.State)
	assert.Equal(t, "新控制面", req.Name)
	assert.NotEmpty(t, req.ID)

	adoptionToken, err := mgr.Approve(req.ID)
	require.NoError(t, err)
	require.NotEmpty(t, adoptionToken)

	got, gotToken, err := mgr.Get(req.ID)
	require.NoError(t, err)
	assert.Equal(t, security.AdoptionStateApproved, got.State)
	assert.Equal(t, adoptionToken, gotToken)

	longToken, record, err := mgr.Exchange(adoptionToken)
	require.NoError(t, err)
	require.NotEmpty(t, longToken)
	assert.Equal(t, "新控制面", record.Name)
	assert.NotEmpty(t, record.ID)
	assert.NotEqual(t, existing.ID, record.ID)

	// 新凭据命中且 Name 正确。
	hitNew, ok := store.VerifyTokenPrincipal(longToken)
	require.True(t, ok)
	assert.Equal(t, "新控制面", hitNew.Name)
	assert.Equal(t, record.ID, hitNew.ID)

	// 关键安全断言：原控制面的凭据在纳管完成后仍然有效——纳管只追加，不覆盖、不吊销。
	hitExisting, ok := store.VerifyTokenPrincipal("existing-secret-token")
	require.True(t, ok, "既有控制面的凭据必须在纳管后继续有效")
	assert.Equal(t, existing.ID, hitExisting.ID)
	assert.Equal(t, "既有控制面", hitExisting.Name)
}

// TestAdoptionManager_ApproveDefaultsEmptyNameToPlaceholder 验证接入方未自报
// 展示名时落地为默认占位符，而不是空字符串（与 Provision/AppendTokenRecord 的
// 兜底语义一致）。
func TestAdoptionManager_ApproveDefaultsEmptyNameToPlaceholder(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	req := mustTryCreate(t, mgr, "  ")
	assert.NotEmpty(t, req.Name)
	assert.NotEqual(t, "  ", req.Name)
}

// TestAdoptionManager_ExpiredRequestRejectsApprove 验证接入请求超过 10 分钟
// 有效期后，Approve 必须拒绝而不是静默批准一个早已过期的请求。
func TestAdoptionManager_ExpiredRequestRejectsApprove(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	req := mustTryCreate(t, mgr, "迟到的控制面")
	clock.Advance(security.AdoptionRequestTTL + time.Second)

	_, err := mgr.Approve(req.ID)
	require.ErrorIs(t, err, security.ErrAdoptionExpired)

	got, token, err := mgr.Get(req.ID)
	require.NoError(t, err)
	assert.Equal(t, security.AdoptionStateExpired, got.State)
	assert.Empty(t, token)
}

// TestAdoptionManager_GetIsOneTimeForToken 验证 GET 的一次性语义：第二次 Get
// 不再带出 adoption_token（防重放），但 state 仍正确反映为 approved。
func TestAdoptionManager_GetIsOneTimeForToken(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	req := mustTryCreate(t, mgr, "控制面 X")
	_, err := mgr.Approve(req.ID)
	require.NoError(t, err)

	first, firstToken, err := mgr.Get(req.ID)
	require.NoError(t, err)
	require.NotEmpty(t, firstToken)
	assert.Equal(t, security.AdoptionStateApproved, first.State)

	second, secondToken, err := mgr.Get(req.ID)
	require.NoError(t, err)
	assert.Empty(t, secondToken, "第二次 Get 不应再次给出 adoption token")
	assert.Equal(t, security.AdoptionStateApproved, second.State)
}

// TestAdoptionManager_ExchangeIsOneTime 验证 Exchange 的一次性语义：同一
// adoption token 第二次兑换必须失败，防止 token 被重放领取多份长期凭据。
func TestAdoptionManager_ExchangeIsOneTime(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	req := mustTryCreate(t, mgr, "控制面 Y")
	adoptionToken, err := mgr.Approve(req.ID)
	require.NoError(t, err)

	_, _, err = mgr.Exchange(adoptionToken)
	require.NoError(t, err)

	_, _, err = mgr.Exchange(adoptionToken)
	require.ErrorIs(t, err, security.ErrAdoptionTokenConsumed)
}

// TestAdoptionManager_ExchangeUnknownTokenRejected 验证 Exchange 拒绝任意未
// 关联到已批准接入请求的 token（包括从未存在过、或对应请求仍是 pending 的情形）。
func TestAdoptionManager_ExchangeUnknownTokenRejected(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	_, _, err := mgr.Exchange("never-issued-token")
	require.ErrorIs(t, err, security.ErrAdoptionTokenInvalid)

	req := mustTryCreate(t, mgr, "控制面 Z")
	_, _, err = mgr.Exchange("guess-" + req.ID)
	require.ErrorIs(t, err, security.ErrAdoptionTokenInvalid)
}

// TestAdoptionManager_RejectThenGetShowsRejected 验证 reject 后 GET 能正确反映
// state==rejected，且不带出任何 token。
func TestAdoptionManager_RejectThenGetShowsRejected(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	req := mustTryCreate(t, mgr, "被拒绝的控制面")
	err := mgr.Reject(req.ID)
	require.NoError(t, err)

	got, token, err := mgr.Get(req.ID)
	require.NoError(t, err)
	assert.Equal(t, security.AdoptionStateRejected, got.State)
	assert.Empty(t, token)

	// rejected 是终态，不可再被 approve 翻案。
	_, err = mgr.Approve(req.ID)
	require.ErrorIs(t, err, security.ErrAdoptionRejected)
}

// TestAdoptionManager_ApproveTwiceAlreadyDecided 验证 approved 是终态，
// 第二次 Approve 不能覆盖或重新生成 token。
func TestAdoptionManager_ApproveTwiceAlreadyDecided(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	req := mustTryCreate(t, mgr, "控制面 W")
	_, err := mgr.Approve(req.ID)
	require.NoError(t, err)

	_, err = mgr.Approve(req.ID)
	require.ErrorIs(t, err, security.ErrAdoptionAlreadyDecided)
}

// TestAdoptionManager_UnknownIDReturnsNotFound 验证对不存在的请求 ID 操作恒
// 返回 ErrAdoptionNotFound，不 panic、不误判为其他状态。
func TestAdoptionManager_UnknownIDReturnsNotFound(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	_, err := mgr.Approve("unknown-id")
	require.ErrorIs(t, err, security.ErrAdoptionNotFound)

	err = mgr.Reject("unknown-id")
	require.ErrorIs(t, err, security.ErrAdoptionNotFound)

	_, _, err = mgr.Get("unknown-id")
	require.ErrorIs(t, err, security.ErrAdoptionNotFound)
}

// TestAdoptionManager_RateLimited 验证 30s 窗口内超过 3 个 pending 接入请求时
// TryCreate 报告 !ok；窗口滑出后自动恢复为可创建。
func TestAdoptionManager_RateLimited(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	for i := 0; i < security.AdoptionRateLimitMax; i++ {
		_, ok := mgr.TryCreate("控制面")
		require.True(t, ok, "第 %d 个 pending 请求不应被限流", i+1)
	}
	_, ok := mgr.TryCreate("控制面")
	assert.False(t, ok, "达到上限后应拒绝创建")

	clock.Advance(security.AdoptionRateLimitWindow + time.Second)
	_, ok = mgr.TryCreate("控制面")
	assert.True(t, ok, "窗口滑出后应自动恢复")
}

// TestAdoptionManager_TryCreateConcurrentRequestsNeverExceedCap 是 Critical
// 修复的回归测试：早期实现把限流判断（RateLimited）和创建（Create）拆成两次
// 独立加锁的调用，并发请求可以全部先通过检查、再各自成功插入，导致 "30s 内最多
// 3 个 pending" 的限流在并发下形同虚设。TryCreate 把两步合并进同一次加锁的
// 临界区，本测试用一批并发调用验证成功创建数恒不超过 AdoptionRateLimitMax。
func TestAdoptionManager_TryCreateConcurrentRequestsNeverExceedCap(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	const attempts = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0

	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if _, ok := mgr.TryCreate("并发控制面"); ok {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, security.AdoptionRateLimitMax, succeeded,
		"%d 个并发 TryCreate 中成功创建的数量必须恰好等于限流上限，不能被竞态绕过", attempts)
}

// TestAdoptionManager_EvictsExpiredRecordsOnNextCreate 是 Important #1
// 修复的回归测试：requests map 此前只增不减，pending/approved/rejected/expired
// 记录会在进程生命周期内无界累积。本测试验证一条记录过期后，下一次 TryCreate
// 会把它连同它占用的内存一起清扫掉——用 Get 返回 ErrAdoptionNotFound 间接证明
// 记录已经从内部 map 中被删除，而不是仅仅状态翻转为 expired。
func TestAdoptionManager_EvictsExpiredRecordsOnNextCreate(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	stale := mustTryCreate(t, mgr, "过期的控制面")

	// 尚未过期时，下一次 TryCreate 不应清扫它——GET 仍能读到 pending。
	_, ok := mgr.TryCreate("控制面 A")
	require.True(t, ok)
	got, _, err := mgr.Get(stale.ID)
	require.NoError(t, err)
	assert.Equal(t, security.AdoptionStatePending, got.State, "未过期前不应被清扫")

	clock.Advance(security.AdoptionRequestTTL + time.Second)

	// 触发一次新的 TryCreate，惰性清扫应删除已过期的 stale 记录。
	_, ok = mgr.TryCreate("控制面 B")
	require.True(t, ok)

	_, _, err = mgr.Get(stale.ID)
	require.ErrorIs(t, err, security.ErrAdoptionNotFound, "过期记录必须被清扫出内部 map，而不是无限期保留")
}

// TestAdoptionManager_ClampsSelfReportedName 覆盖 Finding 1(2)：自报名是匿名
// bypass 端点上的攻击者可控输入，必须在唯一的创建入口就截断到
// AdoptionNameMaxRunes，且不能因为截断多字节字符而产生半个 rune。
func TestAdoptionManager_ClampsSelfReportedName(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	long := strings.Repeat("控", 5000)
	req := mustTryCreate(t, mgr, long)

	assert.Equal(t, security.AdoptionNameMaxRunes, len([]rune(req.Name)))
	assert.Equal(t, strings.Repeat("控", security.AdoptionNameMaxRunes), req.Name)
}

// TestAdoptionManager_StripsControlCharsFromName 锁定日志注入防线：自报名会进
// 一行式 [SuperDev] 日志，含换行的名字能伪造出看似独立的日志行。
func TestAdoptionManager_StripsControlCharsFromName(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	req := mustTryCreate(t, mgr, "CP-A\n[SuperDev] security: 伪造的一行\r\t")

	assert.NotContains(t, req.Name, "\n")
	assert.NotContains(t, req.Name, "\r")
	assert.NotContains(t, req.Name, "\t")
	assert.Contains(t, req.Name, "CP-A")
}

// TestAdoptionManager_PairingCodeIsDeterministicAndBound 覆盖 Finding 2(2)：
// 配对码必须由请求 ID 确定性派生（发起方与批准方各自算出同一个码），长度固定，
// 且不同请求几乎不撞码。
func TestAdoptionManager_PairingCodeIsDeterministicAndBound(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	first := mustTryCreate(t, mgr, "CP-A")
	second := mustTryCreate(t, mgr, "CP-A")

	require.Len(t, first.PairingCode, security.AdoptionPairingCodeLength)
	assert.Equal(t, security.PairingCode(first.ID), first.PairingCode, "配对码必须可由请求 ID 重新推导")
	assert.NotEqual(t, first.PairingCode, second.PairingCode, "同名的两条请求必须拿到不同的码")
	for _, r := range first.PairingCode {
		assert.NotContains(t, "IO01", string(r), "配对码字母表刻意排除口头易混字符")
	}
}
