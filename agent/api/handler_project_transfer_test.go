// handler_project_transfer_test.go 验证转移预检端点 POST
// /api/projects/{id}/transfer/preflight。
//
// 白盒测试（package api）：直接注入 transferRemoteRunner 假件，绕开真实
// SSH/agent 网络往返；本机侧用 t.TempDir() + 真实 git 命令构造仓库场景，
// 与 gitinfo/local_test.go 的做法保持一致（不 mock git 命令本身）。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// initTransferTestRepo 在临时目录初始化一个真实 git 仓库并提交一次初始文件，
// 返回仓库根目录绝对路径。做法与 gitinfo/local_test.go 的 initTestRepo 一致，
// 复制而非跨包导出是因为该 helper 是 _test.go，两个包无法共享。
func initTransferTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runTransferGit(t, dir, "init")
	runTransferGit(t, dir, "config", "user.email", "test@example.com")
	runTransferGit(t, dir, "config", "user.name", "Test User")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644))
	runTransferGit(t, dir, "add", "README.md")
	runTransferGit(t, dir, "commit", "-m", "init")
	return dir
}

func runTransferGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v 执行失败: %v\n输出: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// setTransferRemoteRunner 注入测试假件并注册 t.Cleanup 复位，避免跨测试污染
// 这个包级变量（transferRemoteRunner 是唯一的远端探测测试 seam）。
func setTransferRemoteRunner(t *testing.T, fn func(cmd string) (string, int, error)) {
	t.Helper()
	transferRemoteRunner = func(_ context.Context, cmd, _ string) (string, int, error) {
		return fn(cmd)
	}
	t.Cleanup(func() { transferRemoteRunner = nil })
}

// dirAbsentRunner 是最常用的假件：任何 "test -d" 探测都回答目录不存在，
// 其余命令不应被调用到（InspectRemote 在目录不存在时短路返回）。
func dirAbsentRunner(cmd string) (string, int, error) {
	if strings.Contains(cmd, "test -d") {
		return "no", 0, nil
	}
	return "", 1, nil
}

// addTransferTestProject 通过 POST /api/projects 注册 rootPath 对应的项目，
// 返回创建后的 model.Project（含分配的 ID）。
func addTransferTestProject(t *testing.T, srv *httptest.Server, rootPath string) model.Project {
	t.Helper()
	body := `{"root_path": "` + rootPath + `"}`
	resp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var p model.Project
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&p))
	require.NotEmpty(t, p.ID)
	return p
}

// TestTransferPreflight_NonDevMachineHost_Returns400 验证目标 host 未开启
// DevMachineMode 时预检直接 400，不进入任何探测逻辑。
func TestTransferPreflight_NonDevMachineHost_Returns400(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	dir := initTransferTestRepo(t)
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-not-dev", Name: "Not Dev", DevMachineMode: false})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-not-dev"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "非开发机 host 应返回 400")
}

// TestTransferPreflight_DirtyRepo_BlockersContainUncommitted 验证本机存在
// 未提交变更时，预检响应的 blockers 包含 uncommitted。
func TestTransferPreflight_DirtyRepo_BlockersContainUncommitted(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("wip\n"), 0o644))
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev", Name: "Dev Machine", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var codes []string
	for _, b := range result.Blockers {
		codes = append(codes, b.Code)
	}
	assert.Contains(t, codes, "uncommitted", "脏仓库应触发 uncommitted blocker，实际 blockers=%v", codes)
}

// TestTransferPreflight_CleanRepo_ReadyContainsCheckoutClone 验证干净仓库 +
// 假 Runner 返回目标目录不存在时，ready 包含 checkout_clone 且 TargetDir
// 使用正确的默认值 "~/workspace/<项目目录名>"。
func TestTransferPreflight_CleanRepo_ReadyContainsCheckoutClone(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)
	setTransferRemoteRunner(t, dirAbsentRunner)

	dir := initTransferTestRepo(t)
	project := addTransferTestProject(t, srv, dir)

	_, err := app.remoteStore.AddHost(model.Host{ID: "host-dev-2", Name: "Dev Machine 2", DevMachineMode: true})
	require.NoError(t, err)

	reqBody := `{"host_id": "host-dev-2"}`
	resp, err := http.Post(srv.URL+"/api/projects/"+project.ID+"/transfer/preflight", "application/json", strings.NewReader(reqBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result transferPreflightResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))

	var codes []string
	for _, r := range result.Ready {
		codes = append(codes, r.Code)
	}
	assert.Contains(t, codes, "checkout_clone", "目标目录不存在应 ready=checkout_clone，实际 ready=%v", codes)

	wantTargetDir := "~/workspace/" + filepath.Base(dir)
	assert.Equal(t, wantTargetDir, result.TargetDir, "target_dir 留空时应取默认值")
}
