// Package api 验证 MCP config upsert 的 agent HTTP API。
//
// 职责：
//   - 验证配置 preview/apply 只通过 agent 保存配置
//   - 验证审批前不落盘
//   - 验证不支持删除
//
// 边界：
//   - 不通过 MCP tool 调用
//   - 不启动真实服务进程
package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/configchange"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestConfigChangePreviewDoesNotWriteConfig(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	before, err := os.ReadFile(filepath.Join(root, ".superdev", "config.yaml"))
	require.NoError(t, err)
	preview := postJSONForTest[configchange.PreviewResult](t, srv.URL+"/api/config-changes/preview", map[string]any{
		"kind":       configchange.KindServiceUpsert,
		"project_id": project.ID,
		"service": map[string]any{
			"name": "api",
			"deployments": []map[string]any{{
				"env_name": "dev",
				"location": "local",
				"runtime":  map[string]any{"type": "command", "command": "go run ./cmd/api"},
				"logs":     map[string]any{"type": "process"},
			}},
		},
	}, http.StatusOK)

	assert.True(t, preview.Validation.OK)
	assert.True(t, preview.Plan.RequiresApproval)
	after, err := os.ReadFile(filepath.Join(root, ".superdev", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}

func TestConfigChangeApplyRequiresApprovalThenSaves(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	body := map[string]any{
		"kind":       configchange.KindPipelineUpsert,
		"project_id": project.ID,
		"pipeline": map[string]any{
			"id":       "deploy-dev",
			"name":     "Deploy Dev",
			"services": []string{"worker"},
			"pipeline": map[string]any{"build": []map[string]any{{"name": "Build", "type": "local_command", "with": map[string]any{"command": "go build ./..."}}}},
		},
	}

	required := postJSONForRawTest(t, srv.URL+"/api/config-changes/apply", body, http.StatusForbidden)
	assert.Equal(t, "approval_required", required["code"])
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[map[string]any](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "test",
		"note":       "approve config",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	applied := postJSONWithHeadersForTest[configchange.PreviewResult](t, srv.URL+"/api/config-changes/apply", body, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.True(t, applied.Validation.OK)
	assert.Contains(t, string(readConfigFileForTest(t, root)), "deploy-dev")
}

func TestConfigChangeApplyRejectsUnsupportedOperations(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/config-changes/apply", map[string]any{
		"kind":       configchange.KindServiceUpsert,
		"project_id": project.ID,
		"delete":     true,
		"service":    map[string]any{"name": "worker"},
	}, http.StatusForbidden)

	assert.Equal(t, "operation_denied", resp["code"])
	assert.NotContains(t, string(readConfigFileForTest(t, root)), "operation_denied")
}

func TestConfigChangeProjectUpsertCanCreateProjectThroughAgent(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := t.TempDir()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	body := map[string]any{
		"kind":      configchange.KindProjectUpsert,
		"root_path": root,
		"project": map[string]any{
			"name": "created-by-agent",
			"environments": []map[string]any{{
				"name":   "dev",
				"is_dev": true,
			}},
		},
	}

	required := postJSONForRawTest(t, srv.URL+"/api/config-changes/apply", body, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[map[string]any](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	applied := postJSONWithHeadersForTest[configchange.PreviewResult](t, srv.URL+"/api/config-changes/apply", body, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)
	projects := getJSONForTest[[]model.Project](t, srv.URL+"/api/projects", http.StatusOK)

	assert.Equal(t, "created-by-agent", applied.Project.Name)
	assert.Contains(t, string(readConfigFileForTest(t, root)), "created-by-agent")
	require.Len(t, projects, 1)
	assert.Equal(t, applied.Project.ID, projects[0].ID)
}

func TestConfigChangeOperationPreflightReturnsPlan(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	plan := postJSONForTest[map[string]any](t, srv.URL+"/api/operations/preflight", map[string]any{
		"kind":       configchange.KindPipelineUpsert,
		"project_id": project.ID,
		"pipeline": map[string]any{
			"id":       "deploy-dev",
			"name":     "Deploy Dev",
			"services": []string{"worker"},
			"pipeline": map[string]any{},
		},
	}, http.StatusOK)

	assert.Equal(t, configchange.KindPipelineUpsert, plan["kind"])
	assert.Equal(t, true, plan["requires_approval"])
}

func writeConfigChangeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".superdev", "config.yaml"), []byte(`
name: config-demo
environments:
  - id: env-dev
    name: dev
    is_dev: true
services:
  - id: svc-worker
    name: worker
    deployments:
      - id: dep-worker-dev
        env: dev
        location: local
        command: go run ./worker
`), 0o644))
	return root
}

func addProjectFromRootForConfigChange(t *testing.T, app *App, root string) model.Project {
	t.Helper()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return postJSONForTest[model.Project](t, srv.URL+"/api/projects", map[string]string{"root_path": root}, http.StatusOK)
}

func readConfigFileForTest(t *testing.T, root string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, ".superdev", "config.yaml"))
	require.NoError(t, err)
	return data
}
