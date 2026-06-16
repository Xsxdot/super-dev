// handler_vscode_test.go 测试 VS Code launch.json 导入和项目初始化配置接口。
//
// 职责：
//   - 验证 GET /api/projects/{id}/vscode-launch 正确解析并返回启动配置
//   - 验证 GET /api/projects/{id}/vscode-launch 在文件不存在时返回空数组
//   - 验证 PUT /api/projects/{id}/setup 正确写入 environments 和 deployments
//
// 边界：
//   - 使用 httptest 不依赖外部网络
//   - 依赖 writeTestConfig 辅助函数创建标准测试配置
package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/vscode"
)

// TestGetVscodeLaunch_ReturnsConfigs 验证存在 .vscode/launch.json 时，
// GET /api/projects/{id}/vscode-launch 返回 200 和解析后的启动配置列表。
func TestGetVscodeLaunch_ReturnsConfigs(t *testing.T) {
	srv, _ := newTestApp(t)

	// 创建项目目录并写入标准配置
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	// 写入 .vscode/launch.json
	vscodDir := filepath.Join(dir, ".vscode")
	require.NoError(t, os.MkdirAll(vscodDir, 0o755))
	launchJSON := `{
		"configurations": [
			{
				"name": "web",
				"type": "go",
				"request": "launch",
				"program": "${workspaceFolder}",
				"cwd": "${workspaceFolder}"
			}
		]
	}`
	require.NoError(t, os.WriteFile(filepath.Join(vscodDir, "launch.json"), []byte(launchJSON), 0o644))

	// POST 注册项目，获取 ID
	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	// GET vscode-launch
	getResp, err := http.Get(srv.URL + "/api/projects/" + created.ID + "/vscode-launch")
	require.NoError(t, err)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var configs []vscode.LaunchConfig
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&configs))
	require.Len(t, configs, 1)
	assert.Equal(t, "web", configs[0].Name)
	assert.Equal(t, "go run .", configs[0].Command)
}

// TestGetVscodeLaunch_NoFile 验证 .vscode/launch.json 不存在时，
// GET /api/projects/{id}/vscode-launch 返回 200 和空数组（非 null）。
func TestGetVscodeLaunch_NoFile(t *testing.T) {
	srv, _ := newTestApp(t)

	// 创建项目目录，不写 .vscode
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	// 注册项目
	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	// GET vscode-launch
	getResp, err := http.Get(srv.URL + "/api/projects/" + created.ID + "/vscode-launch")
	require.NoError(t, err)
	defer getResp.Body.Close()
	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	// 必须是空数组，不能是 null
	var configs []vscode.LaunchConfig
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&configs))
	assert.NotNil(t, configs)
	assert.Len(t, configs, 0)
}

// TestPutProjectSetup_AddsNewService 验证 setup 可新增一个 ID 为空的 service，
// 后端分配 ID 并持久化 name/required/order。
func TestPutProjectSetup_AddsNewService(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	webSvcID := created.Services[0].ID

	setupBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services": []map[string]any{
			{"id": webSvcID, "name": "web", "required": false, "order": 0, "deployments": []any{}},
			{"id": "", "name": "worker", "required": true, "order": 1, "deployments": []map[string]any{
				{"env_name": "dev", "location": "local", "command": "go run ./worker"},
			}},
		},
	})
	require.NoError(t, err)
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))
	require.Len(t, updated.Services, 2)
	var worker *model.Service
	for i := range updated.Services {
		if updated.Services[i].Name == "worker" {
			worker = &updated.Services[i]
		}
	}
	require.NotNil(t, worker, "worker service 应已新增")
	assert.NotEmpty(t, worker.ID, "新 service 应分配 ID")
	assert.True(t, worker.Required)
	assert.Equal(t, 1, worker.Order)
	require.Len(t, worker.Deployments, 1)
	assert.Equal(t, "go run ./worker", worker.Deployments[0].Command)

	var web *model.Service
	for i := range updated.Services {
		if updated.Services[i].Name == "web" {
			web = &updated.Services[i]
		}
	}
	require.NotNil(t, web, "已有 web service 应保留")
	assert.Equal(t, webSvcID, web.ID, "保留的 service 应沿用原 ID")
}

func TestPutProjectSetupPersistsServiceLanguageForExistingAndNewServices(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Len(t, created.Services, 1)
	webSvcID := created.Services[0].ID

	setupBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services": []map[string]any{
			{"id": webSvcID, "name": "web", "language": "node", "required": false, "order": 0, "deployments": []any{}},
			{"id": "", "name": "worker", "language": "python", "required": true, "order": 1, "deployments": []any{}},
		},
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))
	require.Len(t, updated.Services, 2)
	web := findServiceByNameForSetupTest(updated.Services, "web")
	worker := findServiceByNameForSetupTest(updated.Services, "worker")
	require.NotNil(t, web)
	require.NotNil(t, worker)
	assert.Equal(t, model.LanguageNode, web.Language)
	assert.Equal(t, model.LanguagePython, worker.Language)

	loaded, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	loadedWeb := findServiceByNameForSetupTest(loaded.Services, "web")
	loadedWorker := findServiceByNameForSetupTest(loaded.Services, "worker")
	require.NotNil(t, loadedWeb)
	require.NotNil(t, loadedWorker)
	assert.Equal(t, model.LanguageNode, loadedWeb.Language)
	assert.Equal(t, model.LanguagePython, loadedWorker.Language)

	clearBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services": []map[string]any{
			{"id": web.ID, "name": "web", "required": false, "order": 0, "deployments": []any{}},
			{"id": worker.ID, "name": "worker", "language": "", "required": true, "order": 1, "deployments": []any{}},
		},
	})
	require.NoError(t, err)
	req, err = http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(clearBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	clearResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer clearResp.Body.Close()
	require.Equal(t, http.StatusOK, clearResp.StatusCode)

	var cleared model.Project
	require.NoError(t, json.NewDecoder(clearResp.Body).Decode(&cleared))
	clearedWeb := findServiceByNameForSetupTest(cleared.Services, "web")
	clearedWorker := findServiceByNameForSetupTest(cleared.Services, "worker")
	require.NotNil(t, clearedWeb)
	require.NotNil(t, clearedWorker)
	assert.Empty(t, clearedWeb.Language)
	assert.Empty(t, clearedWorker.Language)

	loaded, err = config.NewLoader(dir).Load()
	require.NoError(t, err)
	loadedWeb = findServiceByNameForSetupTest(loaded.Services, "web")
	loadedWorker = findServiceByNameForSetupTest(loaded.Services, "worker")
	require.NotNil(t, loadedWeb)
	require.NotNil(t, loadedWorker)
	assert.Empty(t, loadedWeb.Language)
	assert.Empty(t, loadedWorker.Language)
}

// TestPutProjectSetup_AppliesEnvironmentsAndDeployments 验证 PUT /api/projects/{id}/setup
// 正确写入 environments 和 service deployments，并分配 ID。
func TestPutProjectSetup_AppliesEnvironmentsAndDeployments(t *testing.T) {
	srv, _ := newTestApp(t)

	// 注册项目（writeTestConfig 创建含 "web" 服务的配置）
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.NotEmpty(t, created.ID)
	require.Len(t, created.Services, 1)
	webSvcID := created.Services[0].ID
	require.NotEmpty(t, webSvcID)

	// PUT /setup
	setupBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{
			{"name": "dev", "is_dev": true, "order": 0},
		},
		"services": []map[string]any{
			{
				"id":   webSvcID,
				"name": "web",
				"deployments": []map[string]any{
					{
						"env_name": "dev",
						"location": "local",
						"command":  "go run .",
						"work_dir": dir,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPut,
		srv.URL+"/api/projects/"+created.ID+"/setup",
		bytes.NewReader(setupBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	// 验证响应体：environments 和 deployments
	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))

	require.Len(t, updated.Environments, 1)
	assert.Equal(t, "dev", updated.Environments[0].Name)
	assert.True(t, updated.Environments[0].IsDev)

	require.Len(t, updated.Services, 1)
	assert.Equal(t, "web", updated.Services[0].Name)
	require.Len(t, updated.Services[0].Deployments, 1)
	assert.Equal(t, "dev", updated.Services[0].Deployments[0].EnvName)
	assert.NotEmpty(t, updated.Services[0].Deployments[0].ID, "deployment ID 应由 assignIDs 分配")

	// 验证内存已更新：GET /api/projects 应反映新的 environments
	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	require.Len(t, projects, 1)
	assert.Len(t, projects[0].Environments, 1)
}

func TestPutProjectSetupRejectsUnknownRemoteHost(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Len(t, created.Services, 1)

	setupBody, err := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services": []map[string]any{{
			"id":       created.Services[0].ID,
			"name":     "web",
			"required": false,
			"order":    0,
			"deployments": []map[string]any{{
				"env_name":     "dev",
				"location":     "remote",
				"control_mode": "managed",
				"host_ids":     []string{"ghost"},
				"runtime":      map[string]any{"type": "systemd", "service_name": "web"},
				"logs":         map[string]any{"type": "journalctl", "target": "web.service"},
			}},
		}},
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, putResp.StatusCode)
	var body map[string]string
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&body))
	assert.Contains(t, body["error"], "unknown remote host ghost")
}

func TestPutProjectSetupPreservesProjectVariablesAndPipelines(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "demo")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	setupBody := `{
	  "variables": {"app_name":"demo"},
	  "environments": [{"name":"dev","is_dev":true,"order":0}],
	  "services": [{
	    "id": "",
	    "name": "api",
	    "required": true,
	    "order": 0,
	    "deployments": [{
	      "env_name": "dev",
	      "location": "local",
	      "runtime": {"type":"command","command":"go run ."},
	      "logs": {"type":"process"}
	    }]
	  }],
	  "pipelines": [{
	    "id": "deploy-dev",
	    "name": "Deploy Dev",
	    "services": ["api"],
	    "roles": {"api_targets": {"from_service":"api"}},
	    "pipeline": {"build":[{"name":"Build","type":"local_command"}]}
	  }]
	}`
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", strings.NewReader(setupBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	loaded, err := config.NewLoader(dir).Load()
	require.NoError(t, err)
	assert.Equal(t, "demo", loaded.Variables["app_name"])
	require.Len(t, loaded.Pipelines, 1)
	assert.Equal(t, "deploy-dev", loaded.Pipelines[0].ID)
	require.NotNil(t, loaded.Services[0].Deployments[0].Runtime)
	assert.Equal(t, model.RuntimeTypeCommand, loaded.Services[0].Deployments[0].Runtime.Type)
	require.NotNil(t, loaded.Services[0].Deployments[0].Logs)
	assert.Equal(t, model.LogKindProcess, loaded.Services[0].Deployments[0].Logs.Type)
}

// TestPutProjectSetup_DeletesAbsentService 验证请求中不出现的 service 被删除（未运行时）。
func TestPutProjectSetup_DeletesAbsentService(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Len(t, created.Services, 1)

	// 提交一份不含任何 service 的配置 —— web 应被删除
	setupBody, _ := json.Marshal(map[string]any{
		"environments": []map[string]any{{"name": "dev", "is_dev": true, "order": 0}},
		"services":     []any{},
	})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/projects/"+created.ID+"/setup", bytes.NewReader(setupBody))
	req.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer putResp.Body.Close()
	require.Equal(t, http.StatusOK, putResp.StatusCode)

	var updated model.Project
	require.NoError(t, json.NewDecoder(putResp.Body).Decode(&updated))
	assert.Len(t, updated.Services, 0, "未出现在请求中的 service 应被删除")
}

func findServiceByNameForSetupTest(services []model.Service, name string) *model.Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	return nil
}
