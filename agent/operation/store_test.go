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
	"testing"

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

func TestApprovalStoreIssuesTokenOnceAndConsumesIt(t *testing.T) {
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

	secondToken, _, err := store.IssueToken(context.Background(), approval.ID)
	require.NoError(t, err)
	assert.Empty(t, secondToken)

	used, err := store.ConsumeToken(context.Background(), token, detail.Plan.Fingerprint)
	require.NoError(t, err)
	assert.Equal(t, approval.ID, used.ID)
	assert.Equal(t, ApprovalUsed, used.Status)

	_, err = store.ConsumeToken(context.Background(), token, detail.Plan.Fingerprint)
	assert.ErrorIs(t, err, ErrApprovalTokenConsumed)
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

func storePlan(kind string, fp string) Plan {
	return Plan{
		ID:          "op_1",
		Kind:        kind,
		Target:      Target{ProjectID: "proj-1", DeploymentID: "api-prod"},
		RiskLevel:   RiskHigh,
		Fingerprint: fp,
	}
}
