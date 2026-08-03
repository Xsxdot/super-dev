// Package security_test 验证无凭据接入（agent adoption）的状态机。
package security_test

import (
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

	req := mgr.Create("新控制面")
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

	req := mgr.Create("  ")
	assert.NotEmpty(t, req.Name)
	assert.NotEqual(t, "  ", req.Name)
}

// TestAdoptionManager_ExpiredRequestRejectsApprove 验证接入请求超过 10 分钟
// 有效期后，Approve 必须拒绝而不是静默批准一个早已过期的请求。
func TestAdoptionManager_ExpiredRequestRejectsApprove(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	req := mgr.Create("迟到的控制面")
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

	req := mgr.Create("控制面 X")
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

	req := mgr.Create("控制面 Y")
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

	req := mgr.Create("控制面 Z")
	_, _, err = mgr.Exchange("guess-" + req.ID)
	require.ErrorIs(t, err, security.ErrAdoptionTokenInvalid)
}

// TestAdoptionManager_RejectThenGetShowsRejected 验证 reject 后 GET 能正确反映
// state==rejected，且不带出任何 token。
func TestAdoptionManager_RejectThenGetShowsRejected(t *testing.T) {
	store := newTestStore(t)
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{})

	req := mgr.Create("被拒绝的控制面")
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

	req := mgr.Create("控制面 W")
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
// RateLimited 报告 true；窗口滑出后自动恢复为 false。
func TestAdoptionManager_RateLimited(t *testing.T) {
	store := newTestStore(t)
	clock := &testClock{now: time.Now().UTC()}
	mgr := security.NewAdoptionManager(store, security.AdoptionManagerOptions{Now: clock.Now})

	for i := 0; i < security.AdoptionRateLimitMax; i++ {
		require.False(t, mgr.RateLimited(), "第 %d 个 pending 请求前不应限流", i+1)
		mgr.Create("控制面")
	}
	assert.True(t, mgr.RateLimited(), "达到上限后应报告限流")

	clock.Advance(security.AdoptionRateLimitWindow + time.Second)
	assert.False(t, mgr.RateLimited(), "窗口滑出后应自动恢复")
}
