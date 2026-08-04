// handler_approvals_ws_test.go 验证 /ws/operation-approvals 审批快照推流。
//
// 职责：
//   - 覆盖连接建立即收到初始快照帧（pending/decided 均为空）
//   - 覆盖 HTTP 创建 pending 审批后 ≤1s 内收到含新单的帧
//   - 覆盖 approve 后收到该单进入 decided 且 pending 清空的帧
//   - 覆盖断连后发布者注册表被清理（对齐 wsPortMirrors 读 pump 教训的测试写法）
//   - 覆盖 spec §12「双控制面」验收四项串起来的集成语义（Task 11）：双投递、
//     先裁决生效、灰化数据面、记录裁决方
//
// 边界：
//   - 不覆盖桌面端消费逻辑（Task 6）
//   - 不覆盖 WS 鉴权常开回归（security_handler_test.go 已用 /ws/nodes 覆盖通用契约）
package api

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/operation"
)

// TestWsOperationApprovals_SnapshotFanOut 覆盖 brief Step 1 的三项快照场景：
// 初始帧 → 创建 pending 后的新帧 → approve 后 pending 清空且单落 decided 的新帧。
func TestWsOperationApprovals_SnapshotFanOut(t *testing.T) {
	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := newHTTPServerForPackage(t, app)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/operation-approvals?access_token=" + app.LocalAccessToken()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	// 1. 初始快照帧：此时没有任何审批，pending/decided 均为空。
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	var initial approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&initial))
	assert.Empty(t, initial.Pending)
	assert.Empty(t, initial.Decided)

	// 2. HTTP 创建 pending：preflight 触发 approval_required（runtime.restart 默认需审批）。
	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var afterCreate approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&afterCreate), "expected a snapshot frame within 1s of creating a pending approval")
	require.Len(t, afterCreate.Pending, 1)
	assert.Equal(t, approvalID, afterCreate.Pending[0].ID)
	assert.Equal(t, operation.ApprovalPending, afterCreate.Pending[0].Status)
	assert.Empty(t, afterCreate.Pending[0].TokenHash, "快照必须脱敏 token 哈希")
	assert.Empty(t, afterCreate.Decided)

	// 3. approve 后：该单从 pending 消失，出现在 decided 段。
	_ = postJSONForTest[operationApprovalDecisionResponse](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"note": "approved for ws fan-out test",
	}, http.StatusOK)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var afterApprove approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&afterApprove), "expected a snapshot frame within 1s of approving")
	assert.Empty(t, afterApprove.Pending)
	require.Len(t, afterApprove.Decided, 1)
	assert.Equal(t, approvalID, afterApprove.Decided[0].ID)
	assert.Equal(t, operation.ApprovalApproved, afterApprove.Decided[0].Status)
	assert.NotEmpty(t, afterApprove.Decided[0].DecidedBy)
	assert.Empty(t, afterApprove.Decided[0].TokenHash, "快照必须脱敏 token 哈希")
}

// TestWsOperationApprovals_DualControlPlaneAcceptance 是 Task 11 的集成测试，
// 把 spec §12「双控制面」的四项验收在一个测试里连起来验证（对应
// task-11-brief.md 的验收描述），各步骤都用真实 HTTP server + 真实 WS 连接 +
// 两条真实凭据（不伪造 Principal 塞 ctx）：
//
//  1. 双投递：CP-A、CP-B 两个 WS 订阅方同时在线，创建 pending 后两边都必须
//     收到含新单的帧。
//  2. 先裁决生效：CP-A approve 成功后，CP-B 再 reject 同一单必须得到 409
//     approval_already_decided，响应体 decided_by 回显真正的胜者 CP-A（而不是
//     败者 CP-B 或本机)。
//  3. 灰化数据面：两边随后各自的帧里该单都落在 decided 段，且
//     Decided[0].DecidedBy == "CP-A"。
//  4. 记录裁决方：approve 产生的审计事件 Data 里的 decided_by/principal_type/
//     principal_id 记录的是 CP-A 这条远程凭据，而不是败者或本机。
func TestWsOperationApprovals_DualControlPlaneAcceptance(t *testing.T) {
	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()

	// 追加两条独立凭据记录，模拟两个真实控制面各自持有的长期 token
	// （Task 1: AppendTokenRecord；下游 withSecurity 命中后据此推导 Principal）。
	recA, err := app.securityStore.AppendTokenRecord("CP-A", "cp-a-secret-token")
	require.NoError(t, err)
	_, err = app.securityStore.AppendTokenRecord("CP-B", "cp-b-secret-token")
	require.NoError(t, err)

	// 故意不用 newHTTPServerForPackage/testServerHandler：那层包装会在请求缺失
	// Authorization 头时自动注入本机 token，WS dial 只带 access_token query 参数、
	// 不带 Authorization 头，会被那层包装的默认注入吃掉——两个连接会都被鉴权成
	// 「本机」，测不出真正的双控制面 access_token 鉴权路径。这里绑定裸
	// app.Handler()，凡是需要鉴权的请求都显式带凭据。
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	dial := func(accessToken string) *websocket.Conn {
		t.Helper()
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/operation-approvals?access_token=" + accessToken
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	readFrame := func(conn *websocket.Conn) approvalsSnapshot {
		t.Helper()
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		var snap approvalsSnapshot
		require.NoError(t, conn.ReadJSON(&snap), "expected a snapshot frame within 2s")
		return snap
	}

	// --- 1. 双投递：两个控制面同时在线 ---
	connA := dial("cp-a-secret-token")
	connB := dial("cp-b-secret-token")

	initialA := readFrame(connA)
	initialB := readFrame(connB)
	assert.Empty(t, initialA.Pending)
	assert.Empty(t, initialB.Pending)

	// preflight 触发 approval_required（runtime.restart 默认需审批），落地一条
	// pending 审批；创建方身份与本测试无关，显式带本机 token 即可（这里绑定的
	// 是裸 app.Handler()，没有 testServerHandler 的默认注入）。
	required := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, map[string]string{
		"Authorization": "Bearer " + app.LocalAccessToken(),
	}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)

	afterCreateA := readFrame(connA)
	afterCreateB := readFrame(connB)
	require.Len(t, afterCreateA.Pending, 1, "CP-A must receive the new pending approval (dual delivery)")
	require.Len(t, afterCreateB.Pending, 1, "CP-B must receive the same pending approval (dual delivery)")
	assert.Equal(t, approvalID, afterCreateA.Pending[0].ID)
	assert.Equal(t, approvalID, afterCreateB.Pending[0].ID)

	// --- 2. 先裁决生效：CP-A approve 成功，CP-B 随后 reject 同一单必须 409 ---
	approveResp := postJSONWithHeadersForTest[operationApprovalDecisionResponse](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"note": "approved by CP-A",
	}, map[string]string{
		"Authorization": "Bearer cp-a-secret-token",
	}, http.StatusOK)
	assert.Equal(t, "CP-A", approveResp.Approval.DecidedBy)

	// approved 已是终态：CP-B 无论 approve 还是 reject 都不能覆盖或翻案，
	// 必须收到稳定的 409，且响应体回显真正裁决成功的胜者 CP-A。
	conflict := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/operation-approvals/"+approvalID+"/reject", map[string]any{
		"note": "CP-B trying to reject an already-decided approval",
	}, map[string]string{
		"Authorization": "Bearer cp-b-secret-token",
	}, http.StatusConflict)
	assert.Equal(t, "approval_already_decided", conflict["code"])
	assert.Equal(t, "CP-A", conflict["decided_by"])

	// --- 3. 灰化数据面：两边随后的帧里该单落在 decided 段，DecidedBy=="CP-A" ---
	// CP-B 的 reject 走的是 409 早退路径，不调用 signalApprovalsPublishers，
	// 所以两边各自只会再收到 approve 那一次广播触发的帧，不会因为 reject
	// 尝试额外多收一帧。
	afterApproveA := readFrame(connA)
	afterApproveB := readFrame(connB)
	assert.Empty(t, afterApproveA.Pending)
	assert.Empty(t, afterApproveB.Pending)
	require.Len(t, afterApproveA.Decided, 1)
	require.Len(t, afterApproveB.Decided, 1)
	assert.Equal(t, approvalID, afterApproveA.Decided[0].ID)
	assert.Equal(t, approvalID, afterApproveB.Decided[0].ID)
	assert.Equal(t, operation.ApprovalApproved, afterApproveA.Decided[0].Status)
	assert.Equal(t, "CP-A", afterApproveA.Decided[0].DecidedBy)
	assert.Equal(t, "CP-A", afterApproveB.Decided[0].DecidedBy)

	// --- 4. 记录裁决方：审计事件 Data 里记录的 principal 信息是 CP-A ---
	audit := getJSONWithHeadersForTest[operationAuditListResponse](t, srv.URL+"/api/operation-audit?approval_id="+approvalID, map[string]string{
		"Authorization": "Bearer " + app.LocalAccessToken(),
	}, http.StatusOK)
	var approvedEvent *operation.AuditEvent
	for i := range audit.Events {
		if audit.Events[i].Action == operation.AuditApproved {
			approvedEvent = &audit.Events[i]
			break
		}
	}
	require.NotNil(t, approvedEvent, "expected an approved audit event")
	assert.Equal(t, "CP-A", approvedEvent.Data["decided_by"])
	assert.Equal(t, "remote", approvedEvent.Data["principal_type"])
	assert.Equal(t, recA.ID, approvedEvent.Data["principal_id"])
}

// TestWsOperationApprovals_UnregistersPublisherOnDisconnect 验证连接断开后
// approvalsPublisher 从 App 级注册表摘除，不留下 goroutine/订阅项泄漏——
// 对齐 wsPortMirrors 的读 pump 教训（handler_port_mirrors.go:73-85）：没有读
// pump 就读不到客户端的 close 帧，这里用注册表长度收敛作为泄漏检测手段。
func TestWsOperationApprovals_UnregistersPublisherOnDisconnect(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/operation-approvals?access_token=" + app.LocalAccessToken()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	var initial approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&initial))

	require.Eventually(t, func() bool {
		app.approvalsPublisherMu.Lock()
		defer app.approvalsPublisherMu.Unlock()
		return len(app.approvalsPublishers) == 1
	}, time.Second, 10*time.Millisecond, "publisher should be registered while connected")

	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		app.approvalsPublisherMu.Lock()
		defer app.approvalsPublisherMu.Unlock()
		return len(app.approvalsPublishers) == 0
	}, time.Second, 10*time.Millisecond, "publisher registry must be cleaned up after disconnect")
}

// TestWsOperationApprovals_NoGoroutineLeakAfterDisconnect 覆盖 wsOperationApprovals
// 内显式派生 cancel context 的修复：WS 升级（hijack）后 r.Context() 不保证会因
// 连接关闭而 Done（同 handler_port_mirrors.go:73-85 的教训），若把 r.Context()
// 直接透传给 approvalsPublisher.Subscribe，断连后它内部的后台 goroutine 会永远
// 阻塞在自己的 select 上——unregister 只摘掉注册表条目，并不会唤醒它。
// 用多轮连接/断开后 goroutine 数收敛回基线，验证显式 cancel 确实唤醒了它。
func TestWsOperationApprovals_NoGoroutineLeakAfterDisconnect(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/operation-approvals?access_token=" + app.LocalAccessToken()

	runtime.GC()
	baseline := runtime.NumGoroutine()

	const rounds = 5
	for i := 0; i < rounds; i++ {
		conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		require.NoError(t, err)
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
		var initial approvalsSnapshot
		require.NoError(t, conn.ReadJSON(&initial))
		require.NoError(t, conn.Close())
	}

	require.Eventually(t, func() bool {
		runtime.GC()
		// 留一点余量给测试运行时本身的调度抖动，不要求与基线严格相等。
		return runtime.NumGoroutine() <= baseline+2
	}, 3*time.Second, 50*time.Millisecond, "goroutines spawned by wsOperationApprovals must not leak after disconnect")
}

// TestWsOperationApprovals_TokenConsumptionBroadcasts 覆盖 Finding 4：
// approved → used 是每次「已批准操作真正被执行」时发生的急切状态写入，必须像
// approve/reject 一样即时广播。没有这个 signal 点，所有订阅方的 decided 段会
// 一直显示 approved，直到碰巧有别的审批事件把快照顶一次。
func TestWsOperationApprovals_TokenConsumptionBroadcasts(t *testing.T) {
	app := newTestAppForPackage(t)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := newHTTPServerForPackage(t, app)

	// 先把审批推进到 approved 并领到一次性 token，再建立 WS 连接——这样连接
	// 建立后收到的第一帧基线就是 approved，之后收到的任何帧都只可能由本测试
	// 触发的 token 消费引起，不会与创建/裁决的信号混淆。
	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operationApprovalDecisionResponse](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"note": "approved for token consumption broadcast test",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)
	require.NotEmpty(t, detail.ApprovalToken)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/operation-approvals?access_token=" + app.LocalAccessToken()
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	var baseline approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&baseline))
	require.Len(t, baseline.Decided, 1)
	require.Equal(t, operation.ApprovalApproved, baseline.Decided[0].Status)

	// 带 token 执行被批准的操作：ConsumeToken 把该单从 approved 翻成 used。
	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)
	require.Equal(t, "starting", ok["status"])

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var afterConsume approvalsSnapshot
	require.NoError(t, conn.ReadJSON(&afterConsume), "expected a snapshot frame within 1s of consuming the approval token")
	require.Len(t, afterConsume.Decided, 1)
	assert.Equal(t, approvalID, afterConsume.Decided[0].ID)
	assert.Equal(t, operation.ApprovalUsed, afterConsume.Decided[0].Status)
	assert.Empty(t, afterConsume.Decided[0].TokenHash, "快照必须脱敏 token 哈希")
}
