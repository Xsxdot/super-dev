// Package api 验证 config-migration 预览/应用 HTTP 端点。
//
// 职责：
//   - 验证 legacy 项目 GET 返回可用的迁移预览
//   - 验证 POST 应用迁移后，项目内存态与磁盘格式一起翻转为 split，
//     且随后的 GET 正确报告 not_needed（不会反复提示同一个已迁移项目）
//
// 边界：
//   - 迁移本身（BuildMigrationPlan/ApplyMigration 的字段级正确性、疑似密钥
//     判定、gitignore 改写等）由 agent/config 包的 migrate_test.go 覆盖，
//     本文件只钉住 HTTP 层的路由、状态码与内存态刷新
package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
)

// writeLegacyMigrationProject 在临时目录写一份 legacy 单文件配置
// （.superdev/config.yaml），供迁移端点测试使用。
func writeLegacyMigrationProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".superdev", "config.yaml"), []byte(`
name: migrate-demo
variables:
  API_TOKEN: sk-abcdef1234567890
environments:
  - name: dev
    is_dev: true
services:
  - name: api
    deployments:
      - env: dev
        location: local
        command: sleep 60
        working_dir: .
`), 0o644))
	return root
}

// TestGetConfigMigration_LegacyProjectReturnsPlan 钉住 GET 端点的 preview 半场：
// 项目仍是 legacy 格式时，响应必须是可用的 config.MigrationPlan（而不是
// not_needed 或 404），且 preview 不改动磁盘上的任何文件。
func TestGetConfigMigration_LegacyProjectReturnsPlan(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeLegacyMigrationProject(t)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	project := postJSONForTest[model.Project](t, srv.URL+"/api/projects", map[string]string{"root_path": root}, http.StatusOK)
	require.Equal(t, "legacy", project.ConfigFormat, "前置条件：新项目应保持磁盘原有的 legacy 格式，addProject 不做静默迁移")

	before, err := os.ReadFile(filepath.Join(root, ".superdev", "config.yaml"))
	require.NoError(t, err)

	plan := getJSONForTest[config.MigrationPlan](t, srv.URL+"/api/projects/"+project.ID+"/config-migration", http.StatusOK)
	assert.Equal(t, root, plan.RootPath)
	assert.Equal(t, 1, plan.ServiceCount)
	if assert.Len(t, plan.Suspects, 1) {
		assert.Equal(t, "API_TOKEN", plan.Suspects[0].Key)
		assert.NotContains(t, plan.Suspects[0].Masked, "abcdef1234567890", "预览必须脱敏，不能携带明文密钥")
	}

	after, err := os.ReadFile(filepath.Join(root, ".superdev", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "GET 预览不应改动磁盘上的任何文件")
}

// TestConfigMigration_GetNotFoundForUnknownProject 钉住项目本身不存在时的
// 404——不要和"项目存在但没有 legacy 配置"（同样是 404，但走 ApplyMigration
// 内部的 ErrNotFound 分支）混淆。
func TestConfigMigration_GetNotFoundForUnknownProject(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/projects/does-not-exist/config-migration")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestConfigMigration_ApplyThenGetReturnsNotNeeded 是端到端的核心用例：POST
// 应用迁移后，内存态项目立即拿到新的 config_format（不需要重启/重新 GET
// project 才能看到），紧接着的 GET 迁移预览必须报告 not_needed——证明
// preview→apply 走完一轮之后不会在桌面端反复弹出同一个迁移提示。
func TestConfigMigration_ApplyThenGetReturnsNotNeeded(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	root := writeLegacyMigrationProject(t)
	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	project := postJSONForTest[model.Project](t, srv.URL+"/api/projects", map[string]string{"root_path": root}, http.StatusOK)
	originalID := project.ID

	// 不传任何 decisions：疑似密钥按「不挡、只亮」默认落本机层。
	applied := postJSONForTest[model.Project](t, srv.URL+"/api/projects/"+project.ID+"/config-migration",
		map[string]any{"decisions": []config.MigrationDecision{}}, http.StatusOK)

	assert.Equal(t, "split", applied.ConfigFormat, "迁移后内存态返回的 project 应携带新的 split 格式")
	assert.Equal(t, originalID, applied.ID, "迁移不改变项目身份")
	assert.Len(t, applied.Services, 1)

	// 磁盘层面：project.yaml（共享层）已生成，legacy config.yaml 被改名备份。
	_, err = os.Stat(filepath.Join(root, ".superdev", "project.yaml"))
	assert.NoError(t, err, "迁移后应生成 split 共享层文件")
	_, err = os.Stat(filepath.Join(root, ".superdev", "config.yaml.bak"))
	assert.NoError(t, err, "迁移后 legacy 文件应被改名备份而不是原地保留")

	// 内存态：不用重新拉取项目列表，findProject 立即应该看到新格式。
	app.mu.RLock()
	inMemory, ok := app.findProject(originalID)
	app.mu.RUnlock()
	require.True(t, ok)
	assert.Equal(t, "split", inMemory.ConfigFormat)

	// 迁移已完成：再次 GET 预览必须报告 not_needed，而不是重复给出一份迁移
	// 计划（那会让桌面端反复弹出同一个已经处理过的迁移提示）。
	status := getJSONForTest[map[string]string](t, srv.URL+"/api/projects/"+originalID+"/config-migration", http.StatusOK)
	assert.Equal(t, "not_needed", status["status"])
}

// TestConfigMigration_ApplyPreservesEnvSelected 钉住迁移不会连带丢失用户在
// legacy 项目上已经做过的服务勾选：迁移前 env_selected_service_ids 活在
// config.yaml 里，迁移后搬进 agent 本地 uistate store，POST 响应必须原样带
// 出来——否则用户刚做完一次涉密处置人审，回头发现自己勾选的服务列表被清空。
func TestConfigMigration_ApplyPreservesEnvSelected(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".superdev", "config.yaml"), []byte(`
name: migrate-env-selected
environments:
  - name: dev
    is_dev: true
services:
  - name: api
    required: true
    deployments:
      - env: dev
        location: local
        command: sleep 60
        working_dir: .
  - name: worker
    deployments:
      - env: dev
        location: local
        command: sleep 60
        working_dir: .
env_selected_service_ids:
  dev: [worker]
`), 0o644))

	srv := httptest.NewServer(testServerHandler(app))
	t.Cleanup(srv.Close)

	project := postJSONForTest[model.Project](t, srv.URL+"/api/projects", map[string]string{"root_path": root}, http.StatusOK)
	require.Equal(t, []string{"worker"}, project.EnvSelectedServiceIDs["dev"], "前置条件：legacy 项目的勾选状态应能正常从 config.yaml 读出")

	applied := postJSONForTest[model.Project](t, srv.URL+"/api/projects/"+project.ID+"/config-migration",
		map[string]any{"decisions": []config.MigrationDecision{}}, http.StatusOK)

	assert.Equal(t, "split", applied.ConfigFormat)
	assert.Equal(t, []string{"worker"}, applied.EnvSelectedServiceIDs["dev"],
		"迁移把 env_selected_service_ids 搬进 uistate store 后，POST 响应仍应把它叠加回来，而不是让调用方以为勾选状态丢了")

	// 落盘的迁移目标：project.yaml 不再携带这个字段（已迁为 UI 本地状态），
	// 真正的归宿是 agent 数据目录下的 uistate.json。
	projectYAML, err := os.ReadFile(filepath.Join(root, ".superdev", "project.yaml"))
	require.NoError(t, err)
	assert.NotContains(t, string(projectYAML), "env_selected_service_ids")
	assert.Equal(t, []string{"worker"}, app.uiState.EnvSelected(root)["dev"])
}
