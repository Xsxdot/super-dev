// Package api 验证 deployment 运行态端点接入 operation 安全门禁。
//
// 职责：
//   - 验证 dev local deployment 仍可直接启停
//   - 验证 non-dev local deployment 需要审批
//
// 边界：
//   - 不通过 MCP 工具调用
//   - 只使用 harmless command
package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentRuntimeEndpoint_AllowsDevLocalWithoutApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(true, false))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/start", map[string]any{}, http.StatusOK)

	assert.Equal(t, "starting", resp["status"])
}

func TestDeploymentRuntimeEndpoint_RequiresApprovalForNonDevLocal(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "approval_required", resp["code"])
	assert.NotNil(t, resp["plan"])
	assert.NotNil(t, resp["approval"])
}
