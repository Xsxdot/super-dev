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
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/vscode"
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
