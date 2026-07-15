// approval_test.go 验证 fingerprint 精确匹配的真实 Agent HTTP 审批 actor。
//
// 职责：
//   - 锁定 actor 只批准 allowlist tool/kind 与同一 fingerprint
//   - 锁定 grant_grace=false 与一次性 token 重试
//   - 锁定任何身份漂移都 fail closed
//
// 边界：
//   - 测试 server 只模拟正式 HTTP 合同，不绕过 ToolCaller
package runtimevalidation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApprovalActorMatchesFingerprintApprovesWithoutGraceAndRetries(t *testing.T) {
	t.Parallel()

	approved := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/operation-approvals/approval-1":
			status := "pending"
			token := ""
			if approved {
				status, token = "approved", "one-time-token"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval":       map[string]any{"id": "approval-1", "status": status, "plan": map[string]any{"kind": "code_debug.evaluate", "fingerprint": "fingerprint-1"}},
				"approval_token": token,
			})
		case request.Method == http.MethodPost && request.URL.Path == "/api/operation-approvals/approval-1/approve":
			var body map[string]any
			require.NoError(t, json.NewDecoder(request.Body).Decode(&body))
			require.Equal(t, false, body["grant_grace"])
			approved = true
			_ = json.NewEncoder(w).Encode(map[string]any{
				"approval":      map[string]any{"id": "approval-1", "status": "approved", "plan": map[string]any{"kind": "code_debug.evaluate", "fingerprint": "fingerprint-1"}},
				"grace_granted": false,
			})
		default:
			http.NotFound(w, request)
		}
	}))
	t.Cleanup(server.Close)

	delegate := &approvalDelegate{}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	result, err := actor.CallTool(context.Background(), "debug_evaluate", map[string]any{"deployment_id": "dep-1"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.True(t, approved)
	require.Equal(t, 2, delegate.calls)
	require.Equal(t, "one-time-token", delegate.lastArguments["approval_token"])
}

func TestApprovalActorRejectsFingerprintDriftWithoutRetry(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"approval": map[string]any{"id": "approval-1", "status": "pending", "plan": map[string]any{"kind": "code_debug.evaluate", "fingerprint": "different"}},
		})
	}))
	t.Cleanup(server.Close)
	delegate := &approvalDelegate{}
	actor, err := NewApprovalToolCaller(delegate, ApprovalActorOptions{
		AgentURL: server.URL, CampaignID: "campaign-1", HTTPClient: server.Client(),
		AllowedKinds: map[string][]string{"debug_evaluate": {"code_debug.evaluate"}},
	})
	require.NoError(t, err)

	_, err = actor.CallTool(context.Background(), "debug_evaluate", map[string]any{})
	require.ErrorContains(t, err, "fingerprint")
	require.Equal(t, 1, delegate.calls)
}

type approvalDelegate struct {
	calls         int
	lastArguments map[string]any
}

func (d *approvalDelegate) CallTool(_ context.Context, _ string, arguments map[string]any) (ToolCallResult, error) {
	d.calls++
	d.lastArguments = arguments
	if d.calls == 1 {
		return ToolCallResult{IsError: true, StructuredContent: map[string]any{
			"ok": false, "code": "approval_required", "data": map[string]any{
				"approval": map[string]any{"id": "approval-1"},
				"plan":     map[string]any{"kind": "code_debug.evaluate", "fingerprint": "fingerprint-1"},
			},
		}}, nil
	}
	return successToolResult(map[string]any{"result": "visible"}), nil
}
