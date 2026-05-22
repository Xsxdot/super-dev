// Package api_test 提供 HTTP API server 的端到端集成测试。
//
// 职责：
//   - 验证项目管理接口（列表、添加、删除）
//   - 验证服务列表接口
//   - 验证日志查询接口
//
// 边界：
//   - 使用 httptest.NewServer 模拟真实 HTTP 服务
//   - 不依赖外部网络或实际进程启动
package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

// writeTestConfig 在 dir/.superdev/config.yaml 中写入标准测试配置。
//
// 参数：
//   - t: 测试上下文
//   - dir: 项目根目录
//   - name: 项目名称
func writeTestConfig(t *testing.T, dir, name string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	content := "name: " + name + "\nservices:\n  - name: web\n    command: go run .\n"
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(content), 0o644))
}

// newTestApp 创建使用临时目录的 App 实例，并返回对应的测试 HTTP 服务器。
//
// 测试结束时会自动关闭 HTTP 服务器并调用 app.Close() 释放所有资源。
func newTestApp(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dataDir := t.TempDir()
	app, err := api.NewApp(api.AppConfig{
		DataDir: dataDir,
		ProbeOverride: collector.ProbeFunc(func(model.LogSourceType, string) error {
			return nil
		}),
	})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv, dataDir
}

// TestListProjects 验证 GET /api/projects 返回 200 和空列表。
func TestListProjects(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var projects []model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&projects))
	assert.NotNil(t, projects)
	assert.Equal(t, 0, len(projects))
}

// TestAddProject 验证 POST /api/projects 成功后 GET /api/projects 返回 1 个项目。
func TestAddProject(t *testing.T) {
	srv, _ := newTestApp(t)

	// 创建项目目录和配置
	projDir := t.TempDir()
	writeTestConfig(t, projDir, "testapp")

	body := `{"root_path": "` + projDir + `"}`
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "testapp", created.Name)
	assert.NotEmpty(t, created.ID)

	// 确认 GET 返回该项目
	resp2, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer resp2.Body.Close()

	var projects []model.Project
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&projects))
	assert.Equal(t, 1, len(projects))
	assert.Equal(t, "testapp", projects[0].Name)
}

// TestListServices 验证添加项目后 GET /api/services 返回包含 web 服务的列表。
func TestListServices(t *testing.T) {
	srv, _ := newTestApp(t)

	// 添加包含 web 服务的项目
	projDir := t.TempDir()
	writeTestConfig(t, projDir, "testapp")

	body := `{"root_path": "` + projDir + `"}`
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 获取服务列表
	resp2, err := http.Get(srv.URL + "/api/services")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var services []model.Service
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&services))
	require.Equal(t, 1, len(services))
	assert.Equal(t, "web", services[0].Name)
}

// TestPutSelected 验证 PUT /api/projects/{id}/selected 持久化到 config.yaml。
func TestPutSelected(t *testing.T) {
	srv, _ := newTestApp(t)

	projDir := t.TempDir()
	writeTestConfig(t, projDir, "testapp")

	addBody := `{"root_path": "` + projDir + `"}`
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	putBody := `{"names": ["web"]}`
	req, err := http.NewRequest(
		http.MethodPut,
		srv.URL+"/api/projects/"+created.ID+"/selected",
		strings.NewReader(putBody),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	assert.Equal(t, http.StatusOK, putResp.StatusCode)

	getResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer getResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&projects))
	require.Len(t, projects, 1)
	assert.Equal(t, []string{"web"}, projects[0].SelectedServiceIDs)

	cfgData, err := os.ReadFile(filepath.Join(projDir, ".superdev", "config.yaml"))
	require.NoError(t, err)
	var onDisk struct {
		SelectedServiceIDs []string `yaml:"selected_service_ids"`
	}
	require.NoError(t, yaml.Unmarshal(cfgData, &onDisk))
	assert.Equal(t, []string{"web"}, onDisk.SelectedServiceIDs)
}

// TestFetchLogs 验证 GET /api/logs?limit=10 返回 200 和空数组。
func TestFetchLogs(t *testing.T) {
	srv, _ := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/logs?limit=10")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var entries []model.LogEntry
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&entries))
	assert.NotNil(t, entries)
	assert.Equal(t, 0, len(entries))
}

// TestSettingsDefaultsAndPersistence 验证 agent 设置接口返回默认值并能持久化修改。
func TestSettingsDefaultsAndPersistence(t *testing.T) {
	srv, dataDir := newTestApp(t)

	resp, err := http.Get(srv.URL + "/api/settings")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var defaults struct {
		LogRetentionDays int `json:"log_retention_days"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&defaults))
	assert.Equal(t, 7, defaults.LogRetentionDays)

	req, err := http.NewRequest(
		http.MethodPut,
		srv.URL+"/api/settings",
		strings.NewReader(`{"log_retention_days": 14}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	settingsPath := filepath.Join(dataDir, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"log_retention_days": 14`)
}

// TestSettingsRejectsInvalidRetention 验证日志保留天数范围为 1 到 90。
func TestSettingsRejectsInvalidRetention(t *testing.T) {
	srv, _ := newTestApp(t)

	req, err := http.NewRequest(
		http.MethodPut,
		srv.URL+"/api/settings",
		strings.NewReader(`{"log_retention_days": 0}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestNewAppPrunesOldLogsUsingSavedSettings 验证 App 初始化时按持久化设置清理旧日志。
func TestNewAppPrunesOldLogsUsingSavedSettings(t *testing.T) {
	dataDir := t.TempDir()

	settingsPath := filepath.Join(dataDir, "settings.json")
	require.NoError(t, os.WriteFile(settingsPath, []byte(`{"log_retention_days": 3}`), 0o644))

	dbPath := filepath.Join(dataDir, "logs.db")
	s, err := store.New(dbPath)
	require.NoError(t, err)
	old := time.Now().UTC().Add(-5 * 24 * time.Hour)
	recent := time.Now().UTC()
	require.NoError(t, s.AppendBatch([]model.LogEntry{
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: old, Level: "INFO", Message: "old", Stream: "stdout"},
		{ServiceID: "svc-1", RunID: "run-1", Timestamp: recent, Level: "INFO", Message: "recent", Stream: "stdout"},
	}))
	require.NoError(t, s.Close())

	app, err := api.NewApp(api.AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	t.Cleanup(func() { app.Close() })

	check, err := store.New(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { check.Close() })
	got, err := check.Fetch(store.FetchParams{ServiceID: "svc-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "recent", got[0].Message)
}
