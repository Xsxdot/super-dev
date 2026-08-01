// uistate_restart_test.go 验证 split 格式项目的 env-selected UI 状态能在
// agent 重启后从 uistate store 正确恢复到内存态 Project。
//
// 职责：
//   - 驱动 putEnvSelected 的 split 分支、模拟重启后的 loadRegisteredProjects
//     hydrate overlay，串起 Task 5 交付的完整持久化闭环
//
// 边界：
//   - 只覆盖 split 格式；legacy 分支已由 api_test.go 的
//     TestEnvSelectedPutAndStart 覆盖，本文件不重复
//   - 本文件属于 package api（而非 api_test），因为验证"重启后内存态是否
//     恢复"必须直接调用未导出的 loadRegisteredProjects——包外没有触发它的
//     HTTP seam（真实触发点是 Start(addr) 里的 ListenAndServe 前置调用）
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSplitEnvSelectedSurvivesRestart 钉住 Task 5 的核心交付：split 格式项目
// 通过 PUT /api/projects/{id}/env-selected 写入的勾选状态，在模拟 agent 重启
// （新建一个指向同一 DataDir 的 App 并直接驱动 loadRegisteredProjects）之后，
// 仍然出现在内存态 Project.EnvSelectedServiceIDs 上。
//
// 这条链路（putEnvSelected split 分支 → UIStateStore.SetEnvSelected →
// loadRegisteredProjects 的 hydrate overlay → 内存 Project）此前只在任务执行
// 过程中用一次性脚本验证过、验证完即删除，没有留下回归守护；这个测试补上。
func TestSplitEnvSelectedSurvivesRestart(t *testing.T) {
	dataDir := t.TempDir()
	projDir := t.TempDir()

	cfgDir := filepath.Join(projDir, ".superdev")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	// 直接写 project.yaml（split 格式共享层文件），确保 addProject 走
	// loader.Load() 成功分支，Project.ConfigFormat 从磁盘正确探测为 split——
	// 这是最常见的真实路径（项目已有共享层配置，例如克隆一个已接入 SuperDev
	// 的仓库）。addProject 对全新空目录的格式回填由
	// TestAddProject_EmptyDirConfigFormatIsSplit 单独钉住，本测试不重复。
	cfg := `
name: myproject
environments:
  - name: dev
    is_dev: true
    order: 0
services:
  - name: api
    required: true
    order: 0
    deployments:
      - env: dev
        location: local
        command: "sleep 60"
        working_dir: "."
  - name: worker
    required: false
    order: 1
    deployments:
      - env: dev
        location: local
        command: "sleep 60"
        working_dir: "."
`
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "project.yaml"), []byte(cfg), 0o644))

	app1, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	srv1 := httptest.NewServer(app1.Handler())

	body, _ := json.Marshal(map[string]string{"root_path": projDir})
	resp, err := http.Post(srv1.URL+"/api/projects", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	var project struct {
		ID           string `json:"id"`
		ConfigFormat string `json:"config_format"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&project))
	_ = resp.Body.Close()
	require.Equal(t, "split", project.ConfigFormat, "前置条件：addProject 应正确探测出 split 格式")

	putBody, _ := json.Marshal(map[string]interface{}{
		"env_name": "dev",
		"names":    []string{"worker"},
	})
	putReq, _ := http.NewRequest(http.MethodPut,
		srv1.URL+"/api/projects/"+project.ID+"/env-selected",
		bytes.NewReader(putBody))
	putReq.Header.Set("Content-Type", "application/json")
	putResp, err := http.DefaultClient.Do(putReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, putResp.StatusCode)
	_ = putResp.Body.Close()

	srv1.Close()
	app1.Close()

	// split 格式下 env_selected_service_ids 不应写回 project.yaml——唯一归宿是
	// uistate.json；直接断言磁盘文件内容，而不是只信内存态或 HTTP 响应，避免
	// "内存看着对但其实没真落盘到正确位置"的假阳性。
	projectYAML, err := os.ReadFile(filepath.Join(cfgDir, "project.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(projectYAML), "env_selected_service_ids")
	uistateRaw, err := os.ReadFile(filepath.Join(dataDir, "uistate.json"))
	require.NoError(t, err)
	require.Contains(t, string(uistateRaw), "worker")

	// 模拟 agent 重启：新建一个指向同一 DataDir 的 App，直接驱动
	// loadRegisteredProjects（生产环境中由 App.Start 在 ListenAndServe 之前
	// 调用；这里跳过网络监听，直接触发同一个未导出方法）。
	app2, err := NewApp(AppConfig{DataDir: dataDir})
	require.NoError(t, err)
	defer app2.Close()
	app2.loadRegisteredProjects()

	app2.mu.RLock()
	restored, ok := app2.findProject(project.ID)
	app2.mu.RUnlock()
	require.True(t, ok, "重启后项目应仍在内存态")
	require.Equal(t, "split", restored.ConfigFormat, "重启后重新 Load 仍应正确探测出 split 格式")
	require.Equal(t, []string{"worker"}, restored.EnvSelectedServiceIDs["dev"],
		"重启后 env-selected 应通过 hydrate overlay 从 uistate store 恢复到内存态")
}
