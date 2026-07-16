// approval_test.go 验证无人值守审批 actor 与短期精确 allowlist。
//
// 职责：
//   - 锁定 actor 只接受 allowlist tool/kind 与完整 plan identity
//   - 锁定 actor 通过正式 Agent 审批接口批准 exact match，且 grant_grace=false
//   - 锁定过期、重复 pending 与任何身份漂移都 fail closed
//
// 边界：
//   - 测试 server 只模拟正式 HTTP 合同，不绕过 ToolCaller
package runtimevalidation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApprovalActorApprovesExactAllowlistedPendingAndRetries(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	var approved atomic.Bool
	var auditReads atomic.Int32
	var postRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals":
			require.Equal(t, "pending", request.URL.Query().Get("status"))
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending")})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			status, token := "pending", ""
			if approved.Load() {
				status, token = "approved", "one-time-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval(status), "approval_token": token,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-1/approve":
			postRequests.Add(1)
			var decision map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&decision))
			require.Equal(t, "runtime-validation:campaign-1", decision["decided_by"])
			require.Equal(t, false, decision["grant_grace"])
			approved.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval("approved"), "grace_granted": false,
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-audit":
			require.Equal(t, "code_debug.evaluate", request.URL.Query().Get("kind"))
			require.Equal(t, fixture.createdAt, request.URL.Query().Get("since"))
			require.Equal(t, "0", request.URL.Query().Get("limit"))
			events := []any{fixture.auditEvent("approved", "approval-1")}
			if auditReads.Add(1) >= 2 {
				events = append(events, fixture.auditEvent("executed", "approval-1"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	result, err := actor.CallTool(ctx, "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, int32(1), postRequests.Load())
	require.Equal(t, 2, delegate.calls)
	require.Equal(t, int32(2), auditReads.Load())
	require.Equal(t, 0, delegate.firstArguments["approval_wait_seconds"])
	require.Equal(t, "one-time-token", delegate.lastArguments["approval_token"])
}

func TestApprovalActorLeavesUnknownPendingUnapproved(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	unknown := fixture.approval("pending")
	unknown["id"] = "approval-unknown"
	unknownPlan := unknown["plan"].(map[string]any)
	unknownPlan["id"] = "plan-unknown"
	unknownPlan["fingerprint"] = "fingerprint-unknown"
	unknownPlan["target"] = map[string]any{"deployment_id": "dep-unknown", "debug_session_id": "debug-unknown"}
	var approved atomic.Bool
	var auditReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals":
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending"), unknown})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			status, token := "pending", ""
			if approved.Load() {
				status, token = "approved", "one-time-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval(status), "approval_token": token,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-1/approve":
			approved.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval("approved"), "grace_granted": false,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-unknown/approve":
			t.Fatal("unknown pending approval must never be approved")
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-audit":
			events := []any{fixture.auditEvent("approved", "approval-1")}
			if auditReads.Add(1) >= 2 {
				events = append(events, fixture.auditEvent("executed", "approval-1"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": events})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.NoError(t, err)
	require.Equal(t, 2, delegate.calls)
}

func TestApprovalActorPendingReadProbeCannotEnterApprovalAllowlist(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	var postRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"approval": fixture.approval("pending")})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals":
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending")})
		case request.Method == http.MethodPost:
			postRequests.Add(1)
			t.Fatal("pending read probe must never be approved")
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	delegate := &pendingApprovalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	approvalID, err := actor.PreparePendingReadProbe(context.Background(), "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.NoError(t, err)
	require.Equal(t, "approval-1", approvalID)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.ErrorContains(t, err, "pending read probe")
	require.Equal(t, 2, delegate.calls)
	require.Equal(t, int32(0), postRequests.Load())
}

func TestApprovalActorRejectsMutationThatBypassesApproval(t *testing.T) {
	t.Parallel()

	delegate := &directSuccessApprovalDelegate{}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: "http://127.0.0.1:57018", CampaignID: "campaign-1",
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.ErrorContains(t, err, "without approval_required")
	require.Equal(t, 0, delegate.arguments["approval_wait_seconds"])
}

func TestApprovalActorAllowsLowRiskRuntimeMutationWithoutPendingApproval(t *testing.T) {
	t.Parallel()

	delegate := &directSuccessApprovalDelegate{}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: "http://127.0.0.1:57018", CampaignID: "campaign-1",
		AllowedKinds: map[string][]string{"start_service": {"runtime.start"}},
	})
	require.NoError(t, err)

	result, err := actor.CallTool(context.Background(), "start_service", map[string]any{"deployment_id": "dep-1"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 0, delegate.arguments["approval_wait_seconds"])
}

func TestApprovalActorRejectsGraceAuditAfterApprovedRetry(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	var approved atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals":
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending")})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			status, token := "pending", ""
			if approved.Load() {
				status, token = "approved", "one-time-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval(status), "approval_token": token,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-1/approve":
			approved.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval("approved"), "grace_granted": false,
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-audit":
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{
				fixture.auditEvent("approved_by_grace", ""),
				fixture.auditEvent("grace_granted", "approval-1"),
			}})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "forbidden grace action")
	require.Equal(t, 1, delegate.calls)
}

func TestApprovalActorRejectsMissingTokenConsumptionAudit(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	var approved atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals":
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending")})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			status, token := "pending", ""
			if approved.Load() {
				status, token = "approved", "one-time-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval(status), "approval_token": token,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-1/approve":
			approved.Store(true)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval": fixture.approval("approved"), "grace_granted": false,
			})
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-audit":
			_ = json.NewEncoder(w).Encode(map[string]any{"events": []any{}})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "token consumption audit count is 0")
	require.Equal(t, 2, delegate.calls)
}

func TestApprovalActorRejectsFingerprintDriftWithoutRetry(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		response := fixture.approval("pending")
		response["plan"].(map[string]any)["fingerprint"] = "different"
		_ = json.NewEncoder(w).Encode(map[string]any{"approval": response})
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "identity drift")
	require.Equal(t, 1, delegate.calls)
}

func TestApprovalActorRejectsExpiredPlanBeforeReadingAgentState(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(-time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Fatal("expired approval must be rejected before HTTP lookup")
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "expired")
	require.Equal(t, 1, delegate.calls)
}

func TestApprovalActorRejectsDuplicatePendingForSameOperationTarget(t *testing.T) {
	t.Parallel()

	fixture := newApprovalFixture(time.Now().UTC().Add(5 * time.Minute))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/operation-approvals/approval-1":
			_ = json.NewEncoder(w).Encode(map[string]any{"approval": fixture.approval("pending")})
		case "/api/operation-approvals":
			duplicate := fixture.approval("pending")
			duplicate["id"] = "approval-duplicate"
			_ = json.NewEncoder(w).Encode([]any{fixture.approval("pending"), duplicate})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{fixture: fixture}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "duplicate pending")
	require.Equal(t, 1, delegate.calls)
}

type approvalFixture struct {
	createdAt string
	expiresAt string
}

func newApprovalFixture(expiresAt time.Time) approvalFixture {
	return approvalFixture{
		createdAt: time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
		expiresAt: expiresAt.Format(time.RFC3339Nano),
	}
}

func (f approvalFixture) plan() map[string]any {
	return map[string]any{
		"id": "plan-1", "kind": "code_debug.evaluate", "fingerprint": "fingerprint-1",
		"target":     map[string]any{"deployment_id": "dep-1", "debug_session_id": "debug-1"},
		"created_at": f.createdAt, "expires_at": f.expiresAt,
	}
}

func (f approvalFixture) approval(status string) map[string]any {
	return map[string]any{
		"id": "approval-1", "status": status, "expires_at": f.expiresAt, "plan": f.plan(),
	}
}

func (f approvalFixture) auditEvent(action, approvalID string) map[string]any {
	return map[string]any{
		"kind": "code_debug.evaluate", "action": action, "approval_id": approvalID, "plan": f.plan(),
	}
}

type approvalDelegate struct {
	fixture        approvalFixture
	calls          int
	firstArguments map[string]any
	lastArguments  map[string]any
}

type directSuccessApprovalDelegate struct {
	arguments map[string]any
}

type pendingApprovalDelegate struct {
	fixture approvalFixture
	calls   int
}

func (d *pendingApprovalDelegate) CallTool(_ context.Context, _ string, _ map[string]any) (ToolCallResult, error) {
	d.calls++
	return ToolCallResult{IsError: true, StructuredContent: map[string]any{
		"ok": false, "code": "approval_required", "approval": d.fixture.approval("pending"), "plan": d.fixture.plan(),
	}}, nil
}

func (d *directSuccessApprovalDelegate) CallTool(_ context.Context, _ string, arguments map[string]any) (ToolCallResult, error) {
	d.arguments = arguments
	return successToolResult(map[string]any{"result": "visible"}), nil
}

func (d *approvalDelegate) CallTool(_ context.Context, _ string, arguments map[string]any) (ToolCallResult, error) {
	d.calls++
	if d.calls == 1 {
		d.firstArguments = arguments
	}
	d.lastArguments = arguments
	if d.calls == 1 {
		return ToolCallResult{IsError: true, StructuredContent: map[string]any{
			"ok": false, "code": "approval_required", "approval": d.fixture.approval("pending"), "plan": d.fixture.plan(),
		}}, nil
	}
	return successToolResult(map[string]any{"result": "visible"}), nil
}
