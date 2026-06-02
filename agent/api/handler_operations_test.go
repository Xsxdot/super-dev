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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/operation"
)

func TestOperationAPI_PreflightApproveRejectAndAudit(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
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

func TestOperationAPI_ReadOnlyDeploymentDeniedEvenWithApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(true, true))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/start", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "operation_denied", resp["code"])
	assert.Nil(t, resp["approval"])
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
