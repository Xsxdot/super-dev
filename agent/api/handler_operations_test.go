// Package api 验证 operation 安全门禁 HTTP API。
//
// 职责：
//   - 验证 preflight、审批、拒绝、读取 token 和审计列表
//   - 验证 agent 层强制审批而不是 MCP 层判断
//
// 边界：
//   - 不通过 MCP 工具调用
//   - 不启动真实服务进程
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
)

// TestApprovalTokenFromRequestOnlyReadsHeader 钉死审批 token 的唯一通道是 HTTP 头。
//
// 为什么要有这条：body 通道曾经存在但只在部分端点生效（handler 先用
// json.NewDecoder 读空了 body），是一条会让调用方拿到「批准了却仍 403」的
// 静默不一致。删掉之后必须有测试挡住它被「顺手加回来」。
func TestApprovalTokenFromRequestOnlyReadsHeader(t *testing.T) {
	t.Run("头存在时返回头的值", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"approval_token":"from_body"}`))
		r.Header.Set("X-SuperDev-Approval-Token", "  from_header  ")
		require.Equal(t, "from_header", approvalTokenFromRequest(r))
	})

	t.Run("只有 body 携带 token 时不认", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"approval_token":"from_body"}`))
		require.Equal(t, "", approvalTokenFromRequest(r))
	})

	t.Run("body 不被消费——后续 handler 仍能读到完整请求体", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"intent":"start_dev"}`))
		_ = approvalTokenFromRequest(r)
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"intent":"start_dev"}`, string(raw))
	})
}

// fakeApprovalTransport 记录审批代理收到的请求，并按预设响应回写。
type fakeApprovalTransport struct {
	mu       sync.Mutex
	gotHost  []string
	gotPath  []string
	gotHeads []http.Header
	respBody string
	respCode int
	doErr    error
}

func (t *fakeApprovalTransport) Do(_ context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	t.mu.Lock()
	t.gotHost = append(t.gotHost, hostID)
	t.gotPath = append(t.gotPath, req.Path)
	t.gotHeads = append(t.gotHeads, req.Headers.Clone())
	t.mu.Unlock()
	if t.doErr != nil {
		return nodetransport.NodeResponse{}, t.doErr
	}
	status := t.respCode
	if status == 0 {
		status = http.StatusOK
	}
	body := t.respBody
	if body == "" {
		body = `{}`
	}
	return nodetransport.NodeResponse{
		StatusCode: status,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func (t *fakeApprovalTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (t *fakeApprovalTransport) SubscribeNodes(context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	ch := make(chan []nodetransport.NodeStatus)
	close(ch)
	return ch, func() {}
}

func (t *fakeApprovalTransport) Covers() []string { return nil }

func (t *fakeApprovalTransport) hosts() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.gotHost...)
}

func (t *fakeApprovalTransport) paths() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.gotPath...)
}

func (t *fakeApprovalTransport) header(name string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.gotHeads) == 0 {
		return ""
	}
	return t.gotHeads[len(t.gotHeads)-1].Get(name)
}

// TestForeignApprovalDetailRoutesToOrigin 钉死取详情（含签发 token）路由回源节点。
func TestForeignApprovalDetailRoutesToOrigin(t *testing.T) {
	tr := &fakeApprovalTransport{respCode: http.StatusOK,
		respBody: `{"approval":{"id":"opa_x","status":"approved"},"approval_token":"tok_remote"}`}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	seedHomedProject(t, app, "proj-1", "h1")
	app.approvalAggregator.ApplyForTest(map[string]remoteApprovals{
		"h1": {Reachable: true, Snapshot: approvalsSnapshot{
			Pending: []operation.Approval{foreignPending("opa_x", "proj-1")},
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/operation-approvals/opa_x", nil)
	req.SetPathValue("id", "opa_x")
	app.getOperationApproval(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "tok_remote", "源节点签发的 token 必须原样回写")
	require.Equal(t, "h1", app.approvalTokenOrigin.OriginOf("tok_remote"), "代理取回的 token 必须登记源节点")
	require.Equal(t, []string{"h1"}, tr.hosts())
	require.Equal(t, []string{"/api/operation-approvals/opa_x"}, tr.paths())
}

// TestForeignApprovalApproveRoutesToOrigin 钉死裁决路由回源节点。
func TestForeignApprovalApproveRoutesToOrigin(t *testing.T) {
	tr := &fakeApprovalTransport{respCode: http.StatusOK,
		respBody: `{"approval":{"id":"opa_x","status":"approved"}}`}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	seedHomedProject(t, app, "proj-1", "h1")
	app.approvalAggregator.ApplyForTest(map[string]remoteApprovals{
		"h1": {Reachable: true, Snapshot: approvalsSnapshot{
			Pending: []operation.Approval{foreignPending("opa_x", "proj-1")},
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/opa_x/approve", strings.NewReader(`{}`))
	req.SetPathValue("id", "opa_x")
	app.approveOperationApproval(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"h1"}, tr.hosts())
	require.Equal(t, []string{"/api/operation-approvals/opa_x/approve"}, tr.paths())
	audits, err := app.operationAudit.List(context.Background(), operation.AuditFilter{ApprovalID: "opa_x"})
	require.NoError(t, err)
	require.Len(t, audits, 1)
	require.Equal(t, operation.AuditApprovalProxied, audits[0].Action)
	require.Equal(t, "h1", audits[0].Data["origin_host_id"])
}

// TestForeignApprovalOriginUnreachableFailsLoudly 钉死不静默回落本机。
//
// 为什么这条最重要：静默回落等于「在本机批准了一个本机根本没有的审批」——
// 本机 store 里没有 opa_x，回落处理只会得到 404 或者更糟地新建一条，
// 而用户以为自己已经批过了。与归属路由既有的「转发失败绝不静默回落本机」同源。
func TestForeignApprovalOriginUnreachableFailsLoudly(t *testing.T) {
	tr := &fakeApprovalTransport{doErr: errors.New("dial tcp: connection refused")}
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeTransportOverride: tr})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	seedHomedProject(t, app, "proj-1", "h1")
	app.approvalAggregator.ApplyForTest(map[string]remoteApprovals{
		"h1": {Reachable: true, Snapshot: approvalsSnapshot{
			Pending: []operation.Approval{foreignPending("opa_x", "proj-1")},
		}},
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/opa_x/approve", strings.NewReader(`{}`))
	req.SetPathValue("id", "opa_x")
	app.approveOperationApproval(rec, req)

	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.Contains(t, rec.Body.String(), "home_unreachable")
}

func TestOperationAPI_PreflightApproveRejectAndAudit(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	preflight := postJSONForTest[operation.Plan](t, srv.URL+"/api/operations/preflight", map[string]any{
		"kind":          operation.OperationRuntimeRestart,
		"deployment_id": "api-prod",
	}, http.StatusOK)
	assert.True(t, preflight.RequiresApproval)
	assert.False(t, preflight.Denied)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)
	assert.Equal(t, "approval_required", resp["code"])
	approval := resp["approval"].(map[string]any)
	approvalID := approval["id"].(string)

	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "safe test",
	}, http.StatusOK)

	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)
	require.NotEmpty(t, detail.ApprovalToken)
	assert.Empty(t, detail.Approval.TokenHash)

	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)
	assert.Equal(t, "starting", ok["status"])

	audit := getJSONForTest[operationAuditListResponse](t, srv.URL+"/api/operation-audit?approval_id="+approvalID, http.StatusOK)
	assert.GreaterOrEqual(t, len(audit.Events), 2)
}

// 裁决身份必须服务器侧从已验证凭据推导，绝不信任请求体自报的 decided_by；
// 且 approved 是终态，第二条凭据再次裁决（无论 approve 还是 reject）必须收到
// 稳定的 409 approval_already_decided，而不是静默覆盖第一个裁决者的身份。
func TestOperationAPI_DecidedByServerSideAndConflict409(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()

	// 追加一条独立于本机 local-access-token 的远程凭据记录，模拟第二个控制面。
	_, err = app.securityStore.AppendTokenRecord("CP-B", "remote-secret-token")
	require.NoError(t, err)

	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	// testServerHandler 在请求未显式带 Authorization 时默认注入本机 token，
	// 这里借此触发一条待审批请求。
	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)

	// 本机凭据 approve；请求体里乱填 decided_by，服务器侧必须忽略它，
	// 改用 security.PrincipalFrom 从已验证凭据推导出的展示名「本机」。
	approveResp := postJSONForTest[operationApprovalDecisionResponse](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "totally-fake-name",
		"note":       "approved via local",
	}, http.StatusOK)
	assert.Equal(t, "本机", approveResp.Approval.DecidedBy)

	audit := getJSONForTest[operationAuditListResponse](t, srv.URL+"/api/operation-audit?approval_id="+approvalID, http.StatusOK)
	var approvedEvent *operation.AuditEvent
	for i := range audit.Events {
		if audit.Events[i].Action == operation.AuditApproved {
			approvedEvent = &audit.Events[i]
			break
		}
	}
	require.NotNil(t, approvedEvent, "expected an approved audit event")
	assert.Equal(t, "本机", approvedEvent.Data["decided_by"])
	assert.Equal(t, "local", approvedEvent.Data["principal_type"])

	// 用另一条远程凭据尝试 reject 同一单：approved 已是终态，必须 409 而不是
	// 覆盖或翻案第一个裁决者；请求体的 decided_by 同样被忽略，body.decided_by
	// 必须是真正的胜者「本机」，而不是这次请求自报的值。
	conflict := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/operation-approvals/"+approvalID+"/reject", map[string]any{
		"decided_by": "someone-else",
		"note":       "trying to override",
	}, map[string]string{
		"Authorization": "Bearer remote-secret-token",
	}, http.StatusConflict)
	assert.Equal(t, "approval_already_decided", conflict["code"])
	assert.Equal(t, "本机", conflict["decided_by"])
}

func TestOperationAPI_ReissuesTokenForApprovedUnconsumedApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
	}, http.StatusOK)

	first := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)
	second := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	require.NotEmpty(t, first.ApprovalToken)
	require.NotEmpty(t, second.ApprovalToken)
	assert.NotEqual(t, first.ApprovalToken, second.ApprovalToken)
	invalid := postJSONWithHeadersForTest[map[string]any](t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": first.ApprovalToken,
	}, http.StatusForbidden)
	assert.Equal(t, "approval_token_invalid", invalid["code"])
	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": second.ApprovalToken,
	}, http.StatusOK)
	assert.Equal(t, "starting", ok["status"])
}

func TestOperationAPI_ReadOnlyDeploymentDeniedEvenWithApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(true, true))
	app.mu.Unlock()
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/start", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "operation_denied", resp["code"])
	assert.Nil(t, resp["approval"])
}

func TestApplyApprovalPolicyOverrides(t *testing.T) {
	policy := config.ApprovalPolicy{
		ConfigUpsert: false, PipelineUpsert: true, PipelineRun: false, TemplateImport: true,
		BrowserDebugOpen: false, CodeDebugOpen: false, CodeDebugEvaluate: true, GraceMinutes: 15,
	}
	cases := []struct {
		kind string
		want bool
	}{
		{operation.OperationConfigProjectUpsert, false},
		{operation.OperationConfigServiceUpsert, false},
		{operation.OperationConfigPipelineUpsert, true},
		{operation.OperationPipelineRun, false},
		{operation.OperationTemplateImport, true},
		{operation.OperationBrowserDebugOpen, false},
		{operation.OperationCodeDebugOpen, false},
		{operation.OperationCodeDebugEvaluate, true},
	}
	for _, c := range cases {
		plan := operation.Plan{Kind: c.kind, RequiresApproval: true}
		got := applyApprovalPolicy(plan, policy)
		if got.RequiresApproval != c.want {
			t.Fatalf("kind %s: requires=%v, want %v", c.kind, got.RequiresApproval, c.want)
		}
	}
}

func TestOperationAPIPreflightCodeDebugEvaluate(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	plan := postJSONForTest[operation.Plan](t, srv.URL+"/api/operations/preflight", map[string]any{
		"kind":             operation.OperationCodeDebugEvaluate,
		"project_id":       "p1",
		"project_name":     "demo",
		"deployment_id":    "dep-api",
		"debug_session_id": "cds_123",
		"expression_hash":  "sha256:abc",
	}, http.StatusOK)

	assert.Equal(t, operation.OperationCodeDebugEvaluate, plan.Kind)
	assert.True(t, plan.RequiresApproval)
	assert.Equal(t, "cds_123", plan.Target.DebugSessionID)
	assert.NotContains(t, plan.Fingerprint, "password")
}

func TestApplyApprovalPolicyNeverOverridesDenied(t *testing.T) {
	policy := config.ApprovalPolicy{ConfigUpsert: false, GraceMinutes: 15}
	plan := operation.Plan{Kind: operation.OperationConfigProjectUpsert, Denied: true, RequiresApproval: false}
	got := applyApprovalPolicy(plan, policy)
	if !got.Denied {
		t.Fatal("denied must stay denied")
	}
	// 开关为 false 也不得把 denied 操作变成可执行
	if got.RequiresApproval {
		t.Fatal("denied plan requires_approval should remain as-is, not be re-enabled")
	}
}

func TestApplyApprovalPolicyLeavesRuntimeUntouched(t *testing.T) {
	policy := config.ApprovalPolicy{ConfigUpsert: false, GraceMinutes: 15}
	plan := operation.Plan{Kind: operation.OperationRuntimeStart, RequiresApproval: true}
	got := applyApprovalPolicy(plan, policy)
	if !got.RequiresApproval {
		t.Fatal("runtime.* must not be affected by switches")
	}
}

func TestAuthorizeOperationGraceHit(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	ctx := context.Background()
	if _, err := app.operationGrace.GrantGrace(ctx, "p1", "u", "a1", time.Minute); err != nil {
		t.Fatal(err)
	}
	plan := operation.Plan{Kind: operation.OperationConfigServiceUpsert, RequiresApproval: true, Target: operation.Target{ProjectID: "p1"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	allowed, approval := app.authorizeOperation(rec, req, plan)
	if !allowed {
		t.Fatal("grace hit must allow")
	}
	if approval != nil {
		t.Fatal("grace hit must not create approval")
	}
}

func TestApproveGrantsGraceWindow(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	ctx := context.Background()
	plan := operation.Plan{Kind: operation.OperationConfigServiceUpsert, RequiresApproval: true, Target: operation.Target{ProjectID: "p1"}}
	pending, err := app.operationApprovals.FindOrCreatePending(ctx, plan, "ai", "AI")
	require.NoError(t, err)

	body := []byte(`{"decided_by":"user","grant_grace":true}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/operation-approvals/"+pending.ID+"/approve", bytes.NewReader(body))
	req.SetPathValue("id", pending.ID)
	app.approveOperationApproval(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	_, ok, err := app.operationGrace.ActiveGrace(ctx, "p1")
	require.NoError(t, err)
	if !ok {
		t.Fatal("approve with grant_grace must open grace window")
	}
}

func TestApproveGrantGraceNoProjectIgnored(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	ctx := context.Background()
	plan := operation.Plan{Kind: operation.OperationTemplateImport, RequiresApproval: true, Target: operation.Target{TemplatePath: "/x.yaml"}}
	pending, err := app.operationApprovals.FindOrCreatePending(ctx, plan, "ai", "AI")
	require.NoError(t, err)

	body := []byte(`{"decided_by":"user","grant_grace":true}`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/x", bytes.NewReader(body))
	req.SetPathValue("id", pending.ID)
	app.approveOperationApproval(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	if resp["grace_granted"] != false {
		t.Fatalf("no-project approval must report grace_granted=false, got %v", resp["grace_granted"])
	}
}

func operationAPIProject(isDev bool, readOnly bool) model.Project {
	return model.Project{
		ID:   "proj-op",
		Name: "op-demo",
		Environments: []model.Environment{{
			ID: "env-prod", Name: "prod", IsDev: isDev, Order: 0,
		}},
		Services: []model.Service{{
			ID:        "svc-api",
			ProjectID: "proj-op",
			Name:      "api",
			Deployments: []model.Deployment{{
				ID:       "api-prod",
				EnvName:  "prod",
				Location: model.LocationLocal,
				Command:  "sleep 1",
				ReadOnly: readOnly,
			}},
		}},
	}
}

func getJSONForTest[T any](t *testing.T, url string, status int) T {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, status, resp.StatusCode)
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

// getJSONWithHeadersForTest 同 getJSONForTest，但允许显式带请求头——用于绑定
// 裸 app.Handler()（没有 testServerHandler 默认注入本机 token）的测试场景，
// 调用方必须自己带凭据。
func getJSONWithHeadersForTest[T any](t *testing.T, url string, headers map[string]string, status int) T {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, status, resp.StatusCode)
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func postJSONForRawTest(t *testing.T, url string, body any, status int) map[string]any {
	t.Helper()
	return postJSONWithHeadersForTest[map[string]any](t, url, body, nil, status)
}

func postJSONWithHeadersForTest[T any](t *testing.T, url string, body any, headers map[string]string, status int) T {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, status, resp.StatusCode)
	var out T
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}
