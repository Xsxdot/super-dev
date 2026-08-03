// handler_approvals_ws_test.go 验证 /ws/operation-approvals 审批快照推流。
//
// 职责：
//   - 覆盖连接建立即收到初始快照帧（pending/decided 均为空）
//   - 覆盖 HTTP 创建 pending 审批后 ≤1s 内收到含新单的帧
//   - 覆盖 approve 后收到该单进入 decided 且 pending 清空的帧
//   - 覆盖断连后发布者注册表被清理（对齐 wsPortMirrors 读 pump 教训的测试写法）
//
// 边界：
//   - 不覆盖桌面端消费逻辑（Task 6）
//   - 不覆盖 WS 鉴权常开回归（security_handler_test.go 已用 /ws/nodes 覆盖通用契约）
package api

import (
	"net/http"
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
