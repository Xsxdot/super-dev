// Package operation 验证本机审批和审计持久化。
//
// 职责：
//   - 验证 pending approval 去重、批准、token 发放和消费
//   - 验证 audit 事件持久化和过滤
//
// 边界：
//   - 不调用 HTTP API
//   - 不执行被授权的 operation
package operation

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalStoreReusesPendingApprovalForSameFingerprint(t *testing.T) {
	store := NewApprovalFileStore(t.TempDir() + "/operation-approvals.json")
	plan := storePlan("runtime.restart", "fp-1")

	first, err := store.FindOrCreatePending(context.Background(), plan, "mcp", "Codex")
	require.NoError(t, err)
	second, err := store.FindOrCreatePending(context.Background(), plan, "mcp", "Codex")
	require.NoError(t, err)

	assert.Equal(t, first.ID, second.ID)
	assert.Equal(t, ApprovalPending, second.Status)
}

func TestApprovalStoreIssuesAndConsumesToken(t *testing.T) {
	store := NewApprovalFileStore(t.TempDir() + "/operation-approvals.json")
	approval, err := store.FindOrCreatePending(context.Background(), storePlan("runtime.restart", "fp-1"), "mcp", "Codex")
	require.NoError(t, err)

	approved, err := store.Approve(context.Background(), approval.ID, "user", "ok")
	require.NoError(t, err)
	assert.Equal(t, ApprovalApproved, approved.Status)

	token, detail, err := store.IssueToken(context.Background(), approval.ID)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
	assert.Equal(t, ApprovalApproved, detail.Status)

	used, err := store.ConsumeToken(context.Background(), token, detail.Plan.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, approval.ID, used.ID)
	assert.Equal(t, ApprovalUsed, used.Status)

	_, err = store.ConsumeToken(context.Background(), token, detail.Plan.Fingerprint)
	assert.ErrorIs(t, err, ErrApprovalTokenConsumed)
}

func TestApprovalStoreReissuesUnconsumedToken(t *testing.T) {
	store := NewApprovalFileStore(t.TempDir() + "/operation-approvals.json")
	approval, err := store.FindOrCreatePending(context.Background(), storePlan("runtime.restart", "fp-1"), "desktop", "SuperDev Desktop")
	require.NoError(t, err)
	_, err = store.Approve(context.Background(), approval.ID, "user", "ok")
	require.NoError(t, err)

	firstToken, detail, err := store.IssueToken(context.Background(), approval.ID)
	require.NoError(t, err)
	require.NotEmpty(t, firstToken)

	secondToken, secondDetail, err := store.IssueToken(context.Background(), approval.ID)
	require.NoError(t, err)
	require.NotEmpty(t, secondToken)
	assert.NotEqual(t, firstToken, secondToken)
	assert.Equal(t, detail.ID, secondDetail.ID)

	_, err = store.ConsumeToken(context.Background(), firstToken, detail.Plan.Fingerprint)
	assert.ErrorIs(t, err, ErrApprovalTokenInvalid)
	used, err := store.ConsumeToken(context.Background(), secondToken, detail.Plan.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, ApprovalUsed, used.Status)
}

func TestApprovalStoreRejectAndTokenMismatch(t *testing.T) {
	store := NewApprovalFileStore(t.TempDir() + "/operation-approvals.json")
	approval, err := store.FindOrCreatePending(context.Background(), storePlan("runtime.restart", "fp-1"), "mcp", "Codex")
	require.NoError(t, err)

	rejected, err := store.Reject(context.Background(), approval.ID, "user", "not now")
	require.NoError(t, err)
	assert.Equal(t, ApprovalRejected, rejected.Status)

	_, _, err = store.IssueToken(context.Background(), approval.ID)
	assert.ErrorIs(t, err, ErrApprovalRejected)

	approval2, err := store.FindOrCreatePending(context.Background(), storePlan("runtime.restart", "fp-2"), "mcp", "Codex")
	require.NoError(t, err)
	_, err = store.Approve(context.Background(), approval2.ID, "user", "ok")
	require.NoError(t, err)
	token, _, err := store.IssueToken(context.Background(), approval2.ID)
	require.NoError(t, err)
	_, err = store.ConsumeToken(context.Background(), token, "different-fingerprint")
	assert.ErrorIs(t, err, ErrApprovalTokenInvalid)
}

func TestApprovalFirstDecisionWins(t *testing.T) {
	store := NewApprovalFileStore(t.TempDir() + "/operation-approvals.json")
	approval, err := store.FindOrCreatePending(context.Background(), storePlan("runtime.restart", "fp-1"), "mcp", "Codex")
	require.NoError(t, err)

	_, err = store.Approve(context.Background(), approval.ID, "CP-A", "")
	require.NoError(t, err)

	token, _, err := store.IssueToken(context.Background(), approval.ID)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// 二次 approve：第二个控制面再点一次批准，必须被拒绝，不能覆盖胜者身份。
	_, err = store.Approve(context.Background(), approval.ID, "CP-B", "")
	assert.ErrorIs(t, err, ErrApprovalAlreadyDecided)

	// 翻案 reject：已 approve 的单不可被拒绝改判，且已发出的 token 不能被吊销。
	_, err = store.Reject(context.Background(), approval.ID, "CP-B", "")
	assert.ErrorIs(t, err, ErrApprovalAlreadyDecided)

	got, err := store.Get(context.Background(), approval.ID)
	require.NoError(t, err)
	assert.Equal(t, ApprovalApproved, got.Status)
	assert.Equal(t, "CP-A", got.DecidedBy)

	// 已发出的一次性 token 未被 Reject 吊销，仍可正常消费。
	used, err := store.ConsumeToken(context.Background(), token, approval.Plan.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, ApprovalUsed, used.Status)
}

func TestAuditStoreAppendsAndFilters(t *testing.T) {
	store := NewAuditFileStore(t.TempDir()+"/operation-audit.json", 100)
	plan := storePlan("runtime.restart", "fp-1")
	_, err := store.Append(context.Background(), AuditEvent{
		Kind:       plan.Kind,
		Action:     AuditApprovalRequired,
		ApprovalID: "opa_1",
		Plan:       plan,
		Summary:    "approval required",
	})
	require.NoError(t, err)

	events, err := store.List(context.Background(), AuditFilter{ProjectID: "proj-1", Kind: "runtime.restart", ApprovalID: "opa_1", Limit: 10})
	require.NoError(t, err)

	require.Len(t, events, 1)
	assert.Equal(t, AuditApprovalRequired, events[0].Action)
	assert.NotEmpty(t, events[0].ID)
}

func TestAuditStoreTrimPreservesUnfinishedPreparedPlan(t *testing.T) {
	store := NewAuditFileStore(t.TempDir()+"/operation-audit.json", 2)
	preparedPlan := storePlan(OperationTunnelInvalidate, "prepared-fingerprint")
	preparedPlan.ID = "op_prepared"
	_, err := store.Append(context.Background(), AuditEvent{
		Action: AuditPrepared,
		Plan:   preparedPlan,
	})
	require.NoError(t, err)

	for _, id := range []string{"op_completed_1", "op_completed_2", "op_completed_3"} {
		plan := storePlan("runtime.restart", id)
		plan.ID = id
		_, err = store.Append(context.Background(), AuditEvent{
			Action: AuditExecuted,
			Plan:   plan,
		})
		require.NoError(t, err)
	}

	events, err := store.List(context.Background(), AuditFilter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Contains(t, []string{events[0].Plan.ID, events[1].Plan.ID}, preparedPlan.ID)
}

func TestAuditStoreTrimReleasesPreparedPlanAfterTerminalEvent(t *testing.T) {
	store := NewAuditFileStore(t.TempDir()+"/operation-audit.json", 2)
	preparedPlan := storePlan(OperationTunnelInvalidate, "prepared-fingerprint")
	preparedPlan.ID = "op_prepared"
	_, err := store.Append(context.Background(), AuditEvent{Action: AuditPrepared, Plan: preparedPlan})
	require.NoError(t, err)
	_, err = store.Append(context.Background(), AuditEvent{Action: AuditExecuted, Plan: preparedPlan})
	require.NoError(t, err)

	for _, id := range []string{"op_new_1", "op_new_2"} {
		plan := storePlan("runtime.restart", id)
		plan.ID = id
		_, err = store.Append(context.Background(), AuditEvent{Action: AuditExecuted, Plan: plan})
		require.NoError(t, err)
	}

	events, err := store.List(context.Background(), AuditFilter{})
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.NotContains(t, []string{events[0].Plan.ID, events[1].Plan.ID}, preparedPlan.ID)
}

func storePlan(kind string, fp string) Plan {
	return Plan{
		ID:          "op_1",
		Kind:        kind,
		Target:      Target{ProjectID: "proj-1", DeploymentID: "api-prod"},
		RiskLevel:   RiskHigh,
		Fingerprint: fp,
	}
}

// TestTrimApprovalsKeepsPendingAndUnconsumedApproved 是 Finding 1 保留谓词的
// 核心断言：一次能淘汰终态记录的裁剪，绝不能顺手带走仍在承载中的两类记录——
// 待裁决的 pending，以及已批准但一次性 token 尚未被消费的 approved。
func TestTrimApprovalsKeepsPendingAndUnconsumedApproved(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	future := now.Add(10 * time.Minute)

	approvals := []Approval{
		{ID: "pending-live", Status: ApprovalPending, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), ExpiresAt: future},
		{ID: "approved-unconsumed", Status: ApprovalApproved, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), ExpiresAt: future},
	}
	// 6 条终态尾巴：预算 3，其中最旧的必须被淘汰。
	for i := 0; i < 6; i++ {
		stamp := now.Add(time.Duration(i) * time.Minute)
		approvals = append(approvals, Approval{
			ID: "terminal-" + strconv.Itoa(i), Status: ApprovalUsed,
			CreatedAt: stamp, UpdatedAt: stamp, ExpiresAt: future,
		})
	}

	kept := trimApprovals(approvals, 3, now)

	ids := map[string]bool{}
	for _, approval := range kept {
		ids[approval.ID] = true
	}
	assert.True(t, ids["pending-live"], "pending 绝不能被保留上限淘汰")
	assert.True(t, ids["approved-unconsumed"], "已批准但 token 未消费的单绝不能被淘汰")
	assert.False(t, ids["terminal-0"], "最旧的终态记录应当被淘汰")
	assert.True(t, ids["terminal-5"], "最新的终态记录必须保留")
	assert.Len(t, kept, 5, "2 条承载中 + 名额内的 3 条终态尾巴")
}

// TestTrimApprovalsDropsExpiredPendingAndApproved 锁定另一半语义：过了 ExpiresAt
// 的 pending/approved 既不能再被裁决也不能再被兑现，不算承载中，允许被淘汰。
func TestTrimApprovalsDropsExpiredPendingAndApproved(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute)

	approvals := []Approval{
		{ID: "pending-expired", Status: ApprovalPending, CreatedAt: past, UpdatedAt: past, ExpiresAt: past},
		{ID: "approved-expired", Status: ApprovalApproved, CreatedAt: past, UpdatedAt: past, ExpiresAt: past},
		{ID: "terminal-new", Status: ApprovalRejected, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}

	kept := trimApprovals(approvals, 1, now)
	require.Len(t, kept, 1)
	assert.Equal(t, "terminal-new", kept[0].ID)
}

// TestApprovalStoreBoundsGrowthOnRepeatedCreate 覆盖 Finding 1 的真实攻击面：
// 匿名调用方反复触发 FindOrCreatePending（每次一个新 fingerprint）时，落盘记录
// 必须被保留上限约束住，而不是无界增长。
func TestApprovalStoreBoundsGrowthOnRepeatedCreate(t *testing.T) {
	path := t.TempDir() + "/operation-approvals.json"
	// 直接构造以注入小预算：默认 defaultApprovalRetention=1000，测试里没必要真的写一千条。
	store := &ApprovalFileStore{path: path, limit: 4}

	for i := 0; i < 40; i++ {
		plan := storePlan("runtime.restart", "fp-"+strconv.Itoa(i))
		approval, err := store.FindOrCreatePending(context.Background(), plan, "anon", "anon")
		require.NoError(t, err)
		// 立刻拒绝，把它变成终态尾巴，模拟「审批被处理掉之后记录仍永久堆积」。
		_, err = store.Reject(context.Background(), approval.ID, "user", "")
		require.NoError(t, err)
	}

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var st approvalState
	require.NoError(t, json.Unmarshal(raw, &st))
	assert.LessOrEqual(t, len(st.Approvals), 5, "保留上限必须约束住落盘记录数（预算 4 + 当次新建的 1 条）")
	assert.NotEmpty(t, st.Approvals)
}
