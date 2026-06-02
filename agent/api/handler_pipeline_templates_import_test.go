// Package api 验证模板导入接入 operation 审批。
//
// 职责：
//   - 验证 preview 失败时拒绝且不创建审批
//   - 验证 preview 成功但无 token 时需要审批
//   - 验证批准 token 允许导入模板
//
// 边界：
//   - 不执行流水线
package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/operation"
)

func TestPipelineTemplateImportRequiresApprovalAfterPreview(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	path := writeTemplateForImportTest(t, t.TempDir(), "custom-import")

	resp := postJSONForRawTest(t, srv.URL+"/api/pipeline/templates/import", map[string]any{"path": path}, http.StatusForbidden)

	assert.Equal(t, "approval_required", resp["code"])
	assert.NotNil(t, resp["approval"])
}

func TestPipelineTemplateImportWithApprovalTokenSucceeds(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	path := writeTemplateForImportTest(t, t.TempDir(), "custom-import-ok")

	required := postJSONForRawTest(t, srv.URL+"/api/pipeline/templates/import", map[string]any{"path": path}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[map[string]any](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "verified template",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	imported := postJSONWithHeadersForTest[pipelineTemplateSummary](t, srv.URL+"/api/pipeline/templates/import", map[string]any{"path": path}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.Equal(t, "user", imported.Source)
	assert.Equal(t, "custom-import-ok", imported.ID)
}

func TestPipelineTemplateImportPreviewFailureDoesNotCreateApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "broken.yaml")
	require.NoError(t, os.WriteFile(path, []byte("name: Missing ID\n"), 0o600))

	resp := postJSONForRawTest(t, srv.URL+"/api/pipeline/templates/import", map[string]any{"path": path}, http.StatusBadRequest)
	assert.Contains(t, resp["error"], "failed to import template")

	list := getJSONForTest[[]operation.Approval](t, srv.URL+"/api/operation-approvals", http.StatusOK)
	assert.Empty(t, list)
}

func writeTemplateForImportTest(t *testing.T, dir string, id string) string {
	t.Helper()
	path := filepath.Join(dir, id+".yaml")
	yaml := "id: " + id + "\nname: Custom Import\nversion: 1.0.0\nsteps:\n  - name: Echo\n    type: local_command\n    with:\n      command: echo ok\n"
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o600))
	return path
}
