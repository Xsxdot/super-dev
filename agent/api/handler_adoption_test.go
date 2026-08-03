// Package api 验证无凭据接入（agent adoption）HTTP 端点的全链路行为。
//
// 职责：
//   - 验证 Create → 审批出现在 pending → approve → GET 拿 adoption_token →
//     Exchange 拿长期 token 的全链路 happy path
//   - 验证过期、二次 GET、二次 Exchange、reject 后状态、限流 429 等边界
//   - 验证纳管成功后既有控制面凭据仍然有效（纳管与重装的核心区别）
//
// 边界：
//   - 不覆盖 security.AdoptionManager 状态机本身的单元语义，那部分在
//     agent/security/adoption_test.go 白盒覆盖
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/security"
)

// adoptionClock 是 handler_adoption_test.go 专用的可控时钟，注入
// AppConfig.AdoptionNowOverride，驱动 10 分钟接入请求有效期而无需真实等待。
type adoptionClock struct{ now time.Time }

func (c *adoptionClock) Now() time.Time          { return c.now }
func (c *adoptionClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// findPendingAdoptApproval 在 pending 审批列表中找到 agent.adopt 且 fingerprint
// 等于给定接入请求 ID 的那一条，供测试拿到 approval ID 发起 approve/reject。
func findPendingAdoptApproval(t *testing.T, app *App, requestID string) operation.Approval {
	t.Helper()
	rec := httptestDo(t, app, http.MethodGet, "/api/operation-approvals?status=pending", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var approvals []operation.Approval
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &approvals))
	for _, approval := range approvals {
		if approval.Plan.Kind == operation.KindAgentAdopt && approval.Plan.Fingerprint == requestID {
			return approval
		}
	}
	t.Fatalf("no pending agent.adopt approval found for request %s", requestID)
	return operation.Approval{}
}

func createAdoptionRequestForTest(t *testing.T, app *App, name string) map[string]any {
	t.Helper()
	rec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests",
		bytes.NewBufferString(`{"name":"`+name+`"}`), map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp
}

// TestAdoptionAPI_FullHappyPathAndExistingCredentialUnaffected 覆盖 brief
// Step1 的全链路 happy path，并断言纳管成功后既有控制面（本机 local-access-token
// 代表的第一个控制面）的凭据依然有效——这是纳管区别于重装的全部意义。
func TestAdoptionAPI_FullHappyPathAndExistingCredentialUnaffected(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-New")
	requestID := created["id"].(string)
	assert.Equal(t, "pending", created["state"])
	assert.NotEmpty(t, created["expires_at"])

	approval := findPendingAdoptApproval(t, app, requestID)

	approveRec := httptestDo(t, app, http.MethodPost, "/api/operation-approvals/"+approval.ID+"/approve",
		bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusOK, approveRec.Code, approveRec.Body.String())

	getRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil,
		map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, getRec.Code)
	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp))
	assert.Equal(t, "approved", getResp["state"])
	adoptionToken, _ := getResp["adoption_token"].(string)
	require.NotEmpty(t, adoptionToken)

	exchangeRec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests/"+requestID+"/exchange",
		bytes.NewBufferString(`{"adoption_token":"`+adoptionToken+`"}`), map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, exchangeRec.Code, exchangeRec.Body.String())
	var exchangeResp map[string]any
	require.NoError(t, json.Unmarshal(exchangeRec.Body.Bytes(), &exchangeResp))
	longToken, _ := exchangeResp["token"].(string)
	require.NotEmpty(t, longToken)
	record, _ := exchangeResp["record"].(map[string]any)
	require.NotNil(t, record)
	assert.Equal(t, "CP-New", record["name"])

	// VerifyTokenPrincipal 命中且 Name 正确。
	rec, ok := app.securityStore.VerifyTokenPrincipal(longToken)
	require.True(t, ok)
	assert.Equal(t, "CP-New", rec.Name)

	// 关键安全断言：既有控制面（本机 local-access-token）的凭据在纳管完成后仍然有效。
	healthRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/exec/health", nil,
		map[string]string{"Authorization": "Bearer " + app.LocalAccessToken()})
	require.Equal(t, http.StatusOK, healthRec.Code, "既有控制面凭据必须在纳管后继续有效")
}

// TestAdoptionAPI_GetIsOneTimeForToken 验证第二次 GET 不再带出 adoption_token
// （防重放），state 仍正确反映为 approved。
func TestAdoptionAPI_GetIsOneTimeForToken(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-Replay")
	requestID := created["id"].(string)
	approval := findPendingAdoptApproval(t, app, requestID)
	approveRec := httptestDo(t, app, http.MethodPost, "/api/operation-approvals/"+approval.ID+"/approve", bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusOK, approveRec.Code)

	first := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, first.Code)
	var firstResp map[string]any
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResp))
	require.NotEmpty(t, firstResp["adoption_token"])

	second := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, second.Code)
	var secondResp map[string]any
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResp))
	assert.Equal(t, "approved", secondResp["state"])
	_, hasToken := secondResp["adoption_token"]
	assert.False(t, hasToken, "第二次 GET 不应再次带出 adoption_token")
}

// TestAdoptionAPI_SecondExchangeRejected 验证同一 adoption token 第二次兑换
// 必须被拒绝，防止 token 重放领取多份长期凭据。
func TestAdoptionAPI_SecondExchangeRejected(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-Twice")
	requestID := created["id"].(string)
	approval := findPendingAdoptApproval(t, app, requestID)
	httptestDo(t, app, http.MethodPost, "/api/operation-approvals/"+approval.ID+"/approve", bytes.NewBufferString(`{}`))

	getRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp))
	adoptionToken := getResp["adoption_token"].(string)

	firstExchange := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests/"+requestID+"/exchange",
		bytes.NewBufferString(`{"adoption_token":"`+adoptionToken+`"}`), map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, firstExchange.Code)

	secondExchange := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests/"+requestID+"/exchange",
		bytes.NewBufferString(`{"adoption_token":"`+adoptionToken+`"}`), map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusUnauthorized, secondExchange.Code, secondExchange.Body.String())
}

// TestAdoptionAPI_RejectThenGetShowsRejected 验证 reject 后 GET 正确反映
// state==rejected，不带任何 token。
func TestAdoptionAPI_RejectThenGetShowsRejected(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-Reject")
	requestID := created["id"].(string)
	approval := findPendingAdoptApproval(t, app, requestID)

	rejectRec := httptestDo(t, app, http.MethodPost, "/api/operation-approvals/"+approval.ID+"/reject", bytes.NewBufferString(`{}`))
	require.Equal(t, http.StatusOK, rejectRec.Code, rejectRec.Body.String())

	getRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, getRec.Code)
	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp))
	assert.Equal(t, "rejected", getResp["state"])
	_, hasToken := getResp["adoption_token"]
	assert.False(t, hasToken)
}

// TestAdoptionAPI_ExpiredRequestReportsExpiredState 验证接入请求超过 10 分钟
// 有效期后 GET 报告 expired，不再可被批准或兑换出凭据。
func TestAdoptionAPI_ExpiredRequestReportsExpiredState(t *testing.T) {
	clock := &adoptionClock{now: time.Now().UTC()}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), AdoptionNowOverride: clock.Now})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-Late")
	requestID := created["id"].(string)

	clock.Advance(11 * time.Minute)

	getRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, getRec.Code)
	var getResp map[string]any
	require.NoError(t, json.Unmarshal(getRec.Body.Bytes(), &getResp))
	assert.Equal(t, "expired", getResp["state"])
}

// TestAdoptionAPI_CreateRateLimited429 验证 30s 内超过 3 个 pending 接入请求
// 触发 429，防止未持有凭据的一方刷屏骚扰审批列表。
func TestAdoptionAPI_CreateRateLimited429(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	for i := 0; i < 3; i++ {
		rec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests",
			bytes.NewBufferString(`{"name":"CP-Flood"}`), map[string]string{"Authorization": ""})
		require.Equal(t, http.StatusCreated, rec.Code)
	}

	rec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests",
		bytes.NewBufferString(`{"name":"CP-Flood"}`), map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
}

// TestAdoptionAPI_ConcurrentCreateNeverExceedsRateLimit 是 Critical 修复的
// 端到端回归测试：早期的 createAdoptionRequest 把限流检查（RateLimited）和
// 创建（Create）拆成两次独立加锁的调用，并发的匿名 POST 可以全部先通过检查
// 再各自成功插入，"30s 内最多 3 个 pending" 在并发下形同虚设。现在 handler
// 只调用一次原子的 TryCreate，本测试用一批并发请求穿过真实 HTTP 路由验证
// 201 的数量恒不超过限流上限。
func TestAdoptionAPI_ConcurrentCreateNeverExceedsRateLimit(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := newHTTPServerForPackage(t, app)

	const attempts = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	created := 0

	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/security/adoption-requests",
				bytes.NewBufferString(`{"name":"CP-Concurrent"}`))
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusCreated {
				mu.Lock()
				created++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 3, created, "%d 个并发 Create 请求中成功创建的数量必须恰好等于限流上限", attempts)
}

// TestAdoptionAPI_BypassPathsRemainOpen 验证纳管的三个端点都在 bypass 白名单内，
// 无凭据请求不会被 withSecurity 中间件拦下——接入方此刻本就没有凭据。
//
// 注意：exchange 对无效 adoption_token 本身也会返回业务语义的 401（token
// invalid），与中间件的 401 状态码相同，因此不能只比较状态码；这里改为断言
// 响应体不含中间件专属的拒绝文案，与 TestSecurityBypassPathsRemainOpen 同一手法。
func TestAdoptionAPI_BypassPathsRemainOpen(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, "CP-Bypass")
	requestID := created["id"].(string)

	getRec := httptestDoWithHeader(t, app, http.MethodGet, "/api/security/adoption-requests/"+requestID, nil, map[string]string{"Authorization": ""})
	require.Equal(t, http.StatusOK, getRec.Code)
	assert.NotContains(t, getRec.Body.String(), "agent token required")

	exchangeRec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests/"+requestID+"/exchange",
		bytes.NewBufferString(`{"adoption_token":"garbage"}`), map[string]string{"Authorization": ""})
	assert.NotContains(t, exchangeRec.Body.String(), "agent token required")
}

// TestAdoptionAPI_RejectsOversizedBody 覆盖 Finding 1(1)：两个匿名 adoption
// 端点的请求体都必须过 http.MaxBytesReader；否则任何能连到 agent 端口的人都能
// 用一个超大 body 把服务端内存/磁盘拖下水。
func TestAdoptionAPI_RejectsOversizedBody(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	huge := strings.Repeat("A", maxAdoptionRequestBytes+1024)

	createRec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests",
		bytes.NewBufferString(`{"name":"`+huge+`"}`), map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusBadRequest, createRec.Code, createRec.Body.String())

	exchangeRec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests/whatever/exchange",
		bytes.NewBufferString(`{"adoption_token":"`+huge+`"}`), map[string]string{"Authorization": ""})
	assert.Equal(t, http.StatusBadRequest, exchangeRec.Code, exchangeRec.Body.String())
}

// TestAdoptionAPI_PendingApprovalCapRejectsFlood 覆盖 Finding 1(3)：
// AdoptionManager 的 30s 窗口限流拦不住「低频但持续」的刷单，落盘的
// pending agent.adopt 审批总数必须另有硬上限。
func TestAdoptionAPI_PendingApprovalCapRejectsFlood(t *testing.T) {
	clock := &adoptionClock{now: time.Now().UTC()}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), AdoptionNowOverride: clock.Now})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	post := func() int {
		rec := httptestDoWithHeader(t, app, http.MethodPost, "/api/security/adoption-requests",
			bytes.NewBufferString(`{"name":"CP-Flood"}`), map[string]string{"Authorization": ""})
		return rec.Code
	}

	// 每批 3 条打满窗口限流，随后推进时钟越过 30s 窗口——窗口限流放行，
	// 但落盘的 pending 审批一直累积。
	accepted := 0
	for batch := 0; batch < 4; batch++ {
		for i := 0; i < 3; i++ {
			if post() == http.StatusCreated {
				accepted++
			}
		}
		clock.Advance(security.AdoptionRateLimitWindow + time.Second)
	}

	assert.Equal(t, maxPendingAdoptApprovals, accepted,
		"跨窗口持续刷单时，能落盘的 pending agent.adopt 审批数必须被硬上限卡住")
	assert.Equal(t, http.StatusTooManyRequests, post())
}

// TestAdoptionAPI_ApprovalCarriesServerDerivedOrigin 覆盖 Finding 2：审批行上
// 必须带服务器侧推导的来源和配对码，自报名只能是次要上下文——否则攻击者把
// 自报名填成真实桌面用的那个字符串，操作员无从分辨该批哪一行。
func TestAdoptionAPI_ApprovalCarriesServerDerivedOrigin(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	// 攻击者自报成真实桌面用的展示名。
	created := createAdoptionRequestForTest(t, app, "SuperDev Desktop")
	requestID := created["id"].(string)
	pairingCode, _ := created["pairing_code"].(string)
	require.Len(t, pairingCode, security.AdoptionPairingCodeLength, "Create 响应必须把配对码回给接入方")
	assert.Equal(t, security.PairingCode(requestID), pairingCode)

	approval := findPendingAdoptApproval(t, app, requestID)
	// httptest.NewRequest 的默认 RemoteAddr 是 192.0.2.1:1234。
	assert.Equal(t, "192.0.2.1", approval.Plan.Target.RequestOrigin, "来源必须来自连接对端地址")
	assert.Equal(t, pairingCode, approval.Plan.Target.PairingCode)
	assert.Equal(t, "192.0.2.1", approval.RequestedBy, "requested_by 必须是服务器侧事实，不是自报名")
	assert.Equal(t, "SuperDev Desktop", approval.RequesterLabel, "自报名保留为上下文")
	assert.Contains(t, approval.Plan.TargetSummary, "192.0.2.1")
	assert.Contains(t, approval.Plan.TargetSummary, pairingCode)
	assert.Contains(t, approval.Plan.TargetSummary, "self-reported", "自报名必须被显式标注为自报")
}

// TestAdoptionAPI_ApprovalClampsSelfReportedName 验证攻击者可控的超长自报名
// 不会被原样带进落盘的审批记录。
func TestAdoptionAPI_ApprovalClampsSelfReportedName(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	created := createAdoptionRequestForTest(t, app, strings.Repeat("X", 3000))
	requestID := created["id"].(string)

	approval := findPendingAdoptApproval(t, app, requestID)
	assert.LessOrEqual(t, len([]rune(approval.RequesterLabel)), security.AdoptionNameMaxRunes)
	assert.NotContains(t, approval.Plan.TargetSummary, strings.Repeat("X", security.AdoptionNameMaxRunes+1))
}
