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
	srv := httptest.NewServer(testServerHandler(app))
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

func TestConfigChangePreviewRejectsUnknownRemoteHost(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	preview := postJSONForTest[configchange.PreviewResult](t, srv.URL+"/api/config-changes/preview", map[string]any{
		"kind":       configchange.KindServiceUpsert,
		"project_id": project.ID,
		"service": map[string]any{
			"name": "worker",
			"deployments": []map[string]any{{
				"env_name":     "dev",
				"location":     "remote",
				"control_mode": "managed",
				"host_ids":     []string{"ghost"},
				"runtime":      map[string]any{"type": "systemd", "service_name": "worker"},
				"logs":         map[string]any{"type": "journalctl", "target": "worker.service"},
			}},
		},
	}, http.StatusOK)

	assert.False(t, preview.Validation.OK)
	assert.Contains(t, preview.Validation.Errors, "service worker deployment dev references unknown remote host ghost")
	assert.True(t, preview.Plan.Denied)
}

func TestConfigChangeApplyRequiresApprovalThenSaves(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(testServerHandler(app))
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
	srv := httptest.NewServer(testServerHandler(app))
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
	srv := httptest.NewServer(testServerHandler(app))
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

// TestConfigChangeProjectUpsertBackfillsConfigFormat 钉住
// resolveConfigChangeProject 的 ErrNotFound 分支曾经的缺口：手工拼出的骨架
// Project 不经过 Loader.Load，ConfigFormat 天然为空；saveConfigChangeProject
// 落盘后必须回填成 Loader 探测到的真实磁盘格式（全新目录 → split），否则后续
// PUT env-selected 会读到 ConfigFormat=="" 误走 legacy 分支，
// Loader.Save 按磁盘真实格式（已经是 split）落盘时静默丢弃
// env_selected_service_ids——与 addProject 的同类缺口对称（见
// TestAddProject_EmptyDirConfigFormatIsSplit）。
func TestConfigChangeProjectUpsertBackfillsConfigFormat(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := t.TempDir()
	srv := httptest.NewServer(testServerHandler(app))
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
	assert.Equal(t, "split", applied.Project.ConfigFormat, "apply 响应应带上刚落盘探测到的格式")

	// putEnvSelected 读的是 a.projects（内存态），不是 apply 的响应体，所以真正
	// 要钉住的是 GET /api/projects 里的值。
	projects := getJSONForTest[[]model.Project](t, srv.URL+"/api/projects", http.StatusOK)
	require.Len(t, projects, 1)
	assert.Equal(t, "split", projects[0].ConfigFormat, "内存中的项目必须带上刚落盘的格式，否则 putEnvSelected 会误走 legacy 分支")
}

// TestResolveConfigChangeProjectPrefersProjectID 钉住归属路由下的跨机解析：
// 配置写入被转发到归属机后，请求体里带的 root_path 是控制面的检出路径，与
// 归属机自己的检出路径天然不同。resolveConfigChangeProject 必须在 project_id
// 命中时直接返回该项目，不再叠加 root_path 相等性——否则归属机会漏过自己的
// 项目：service/pipeline upsert 退化成 404 not found，project upsert 落入骨架
// 分支在归属机上以控制面路径误建一个虚假项目，破坏「配置写入落到目标机」的承诺。
// 单机测试恰好 root_path 始终相符，天然照不出这个缺口。
func TestResolveConfigChangeProjectPrefersProjectID(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	const homePath = "/home/checkout/app"
	const controlPlanePath = "/control-plane/workspace/app"
	app.mu.Lock()
	app.appendProjectLocked(model.Project{ID: "proj-x", Name: "demo", RootPath: homePath})
	app.mu.Unlock()

	// 跨机形状：project_id 命中归属机项目，但 root_path 是控制面路径（与归属机不符）。
	// 修复前：service upsert 会因 root_path 不符被 skip → 落入 not found。
	got, status, msg := app.resolveConfigChangeProject(configchange.ChangeRequest{
		Kind:      configchange.KindServiceUpsert,
		ProjectID: "proj-x",
		RootPath:  controlPlanePath,
	})
	require.Equal(t, http.StatusOK, status, "project_id 命中时不应因 root_path 不符返回 %d: %s", status, msg)
	assert.Equal(t, "proj-x", got.ID)
	assert.Equal(t, homePath, got.RootPath, "必须解析到归属机上的真实项目，而不是控制面路径的骨架项目")

	// project_id-only 仍解析（未提供 root_path，纯 ID 命中，行为不变）。
	got, status, _ = app.resolveConfigChangeProject(configchange.ChangeRequest{
		Kind:      configchange.KindServiceUpsert,
		ProjectID: "proj-x",
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "proj-x", got.ID)

	// root_path-only 回退匹配仍解析（project_id 为空时走 root_path 相等，行为不变）。
	got, status, _ = app.resolveConfigChangeProject(configchange.ChangeRequest{
		Kind:     configchange.KindServiceUpsert,
		RootPath: homePath,
	})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "proj-x", got.ID)

	// ID 不符但 root_path 相符：维持既有 AND 语义——ID 一旦提供且不符即 skip，
	// 不得因 root_path 相符而命中（本次修复不能把这条也放开，否则回归）。
	_, status, _ = app.resolveConfigChangeProject(configchange.ChangeRequest{
		Kind:      configchange.KindServiceUpsert,
		ProjectID: "other-id",
		RootPath:  homePath,
	})
	assert.Equal(t, http.StatusNotFound, status, "ID 提供且不符时应维持 skip 语义，不得因 root_path 相符命中")

	// 真正不存在：project_id 不命中且非 project upsert → not found（行为不变）。
	_, status, _ = app.resolveConfigChangeProject(configchange.ChangeRequest{
		Kind:      configchange.KindServiceUpsert,
		ProjectID: "ghost",
	})
	assert.Equal(t, http.StatusNotFound, status)
}

func TestConfigChangeOperationPreflightReturnsPlan(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeConfigChangeProject(t)
	project := addProjectFromRootForConfigChange(t, app, root)
	srv := httptest.NewServer(testServerHandler(app))
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
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)
	return postJSONForTest[model.Project](t, srv.URL+"/api/projects", map[string]string{"root_path": root}, http.StatusOK)
}

// readConfigFileForTest 读取当前格式的主配置文件。多数用例经 writeConfigChangeProject
// 预置 config.yaml（legacy），但通过 agent 全新创建的项目默认落 split 格式
// （project.yaml），因此这里按文件是否存在探测，而非固定读 config.yaml。
func readConfigFileForTest(t *testing.T, root string) []byte {
	t.Helper()
	path := filepath.Join(root, ".superdev", "config.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(root, ".superdev", "project.yaml")
	}
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}
