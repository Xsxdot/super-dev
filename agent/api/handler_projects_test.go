// handler_projects_test.go 验证项目探测与创建分离的行为。
package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// TestProbeProject_EmptyDir 验证探测无 .superdev/config.yaml 的目录返回空骨架，
// 且不写注册表（GET /api/projects 仍为空）。
func TestProbeProject_EmptyDir(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()

	probeURL := srv.URL + "/api/projects/probe?root_path=" + url.QueryEscape(dir)
	resp, err := http.Get(probeURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var probed model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&probed))
	assert.Empty(t, probed.Services, "空目录应返回无 service")
	assert.NotEmpty(t, probed.Name, "Name 应取目录名")
	assert.Equal(t, dir, probed.RootPath)

	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	assert.Len(t, projects, 0, "探测不应登记项目")
}

// TestProbeProject_ExistingConfig 验证探测已有 config 的目录返回解析后的 project。
func TestProbeProject_ExistingConfig(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeTestConfig(t, dir, "myapp")

	probeURL := srv.URL + "/api/projects/probe?root_path=" + url.QueryEscape(dir)
	resp, err := http.Get(probeURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var probed model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&probed))
	assert.Equal(t, "myapp", probed.Name)
	require.Len(t, probed.Services, 1)
	assert.Equal(t, "web", probed.Services[0].Name)
}

// TestAddProject_EmptyDirCreatesSkeleton 验证 addProject 对无 config 的目录
// 不报错，落地一个空骨架项目（含 ID，写入注册表）。
func TestAddProject_EmptyDirCreatesSkeleton(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.NotEmpty(t, created.ID)
	assert.Empty(t, created.Services)

	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	assert.Len(t, projects, 1, "addProject 应落地项目")
}

// TestAddProject_EmptyDirConfigFormatIsSplit 钉住一个曾经的落地缺口：
// 空目录首次 Save 后磁盘格式是 split（DetectFormat 对无 config.yaml/project.yaml
// 的目录默认返回 split），内存里 a.projects 中的 Project.ConfigFormat 必须同步
// 反映这一点。否则 putEnvSelected 会读到 ConfigFormat=="" 误走 legacy 分支，
// Loader.Save 按磁盘真实格式（split）落盘时静默丢弃 env_selected_service_ids，
// PUT env-selected 端点会在“刚新建项目、agent 还没重启过”这段窗口期说谎。
func TestAddProject_EmptyDirConfigFormatIsSplit(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "split", created.ConfigFormat, "addProject 响应应带上刚落盘探测到的格式")

	// putEnvSelected 读的是 a.projects（内存态），不是这次 POST 的响应体，
	// 所以真正要钉住的是 GET /api/projects 里的值。
	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	require.Len(t, projects, 1)
	assert.Equal(t, "split", projects[0].ConfigFormat, "内存中的项目必须带上刚落盘的格式，否则 putEnvSelected 会误走 legacy 分支")
}

// TestAddProject_BuildsDeploymentLogBackends 验证新增项目后日志接口立刻可用。
//
// 这个场景覆盖运行期添加项目：loadRegisteredProjects 会在 agent 启动时构造
// backends，但 POST /api/projects 也必须同步构造，否则 MCP tail_logs 会 404。
func TestAddProject_BuildsDeploymentLogBackends(t *testing.T) {
	srv, _ := newTestApp(t)
	dir := t.TempDir()
	writeProjectWithDeployment(t, dir)

	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	logsResp, err := http.Get(srv.URL + "/api/deployments/web-dev/logs")
	require.NoError(t, err)
	defer logsResp.Body.Close()
	require.Equal(t, http.StatusOK, logsResp.StatusCode)

	var body struct {
		Items []model.LogEntry `json:"items"`
	}
	require.NoError(t, json.NewDecoder(logsResp.Body).Decode(&body))
	assert.NotNil(t, body.Items)
}

// TestAddProject_RewritesCopiedProjectIdentities 验证复制项目目录后，
// 第二个项目不会复用已有的 project/deployment ID。
func TestAddProject_RewritesCopiedProjectIdentities(t *testing.T) {
	srv, _ := newTestApp(t)
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	writeCopiedIdentityProject(t, firstDir)
	writeCopiedIdentityProject(t, secondDir)

	first := addProjectFromConfigDir(t, srv.URL, firstDir)
	second := addProjectFromConfigDir(t, srv.URL, secondDir)

	require.Len(t, first.Services, 1)
	require.Len(t, first.Services[0].Deployments, 1)
	require.Len(t, second.Services, 1)
	require.Len(t, second.Services[0].Deployments, 1)
	assert.Equal(t, "copied-project", first.ID)
	assert.Equal(t, "api-dev", first.Services[0].Deployments[0].ID)
	assert.NotEqual(t, first.ID, second.ID)
	assert.NotEqual(t, first.Services[0].Deployments[0].ID, second.Services[0].Deployments[0].ID)

	saved, err := os.ReadFile(filepath.Join(secondDir, ".superdev", "config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(saved), "id: "+second.ID)
	assert.Contains(t, string(saved), "id: "+second.Services[0].Deployments[0].ID)
	assert.NotContains(t, string(saved), "\n        id: api-dev\n")
}

func writeProjectWithDeployment(t *testing.T, dir string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	content := `
name: backend-sync
environments:
  - name: dev
    is_dev: true
    order: 1
services:
  - id: web
    name: web
    deployments:
      - id: web-dev
        env: dev
        location: local
        control_mode: managed
        command: echo ready
        working_dir: .
        logs:
          type: process
`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(content), 0o644))
}

func writeCopiedIdentityProject(t *testing.T, dir string) {
	t.Helper()
	cfgDir := filepath.Join(dir, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	content := `
id: copied-project
name: copied
environments:
  - name: dev
    is_dev: true
    order: 1
services:
  - id: svc-api
    name: api
    deployments:
      - id: api-dev
        env: dev
        location: local
        control_mode: managed
        command: echo ready
        working_dir: .
        logs:
          type: process
`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "config.yaml"), []byte(content), 0o644))
}

func addProjectFromConfigDir(t *testing.T, srvURL string, dir string) model.Project {
	t.Helper()
	addBody := fmt.Sprintf(`{"root_path": %q}`, dir)
	resp, err := http.Post(srvURL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	return created
}
