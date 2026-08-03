// host_lifecycle_test.go 验证主机删除守卫的 project_home 分支与开发机模式
// 关闭提示。
//
// 职责：
//   - 证明设归属后删除该 Host 被拒绝（409 project_home），detail 含项目名
//   - 证明迁回本机后同一 Host 可以正常删除
//   - 证明归属记录指向已消失项目时优雅降级（退化为展示 ID，不 panic）
//   - 证明 PUT 更新 dev_machine_mode true→false 时响应体附带 homed_projects，
//     false→true 方向不携带
//
// 白盒测试（package api）：直接操作 app.projectHomeStore/appendProjectLocked
// 构造归属状态，不依赖尚未提供的转移执行 HTTP 入口。
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// createGuardTestHost 创建一个不带 SSH 凭据、未装配 Agent 的最小 Host，
// 供本文件测试专注验证 project_home 守卫本身，不与 agent_configured 分支纠缠。
func createGuardTestHost(t *testing.T, app *App, body string) string {
	t.Helper()
	resp := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(body))
	require.Equal(t, http.StatusOK, resp.Code)
	return decodeHostID(t, resp.Body.Bytes())
}

// TestRemoveHostSafely_ProjectHomeGuard 验证设归属后删除该 Host 被拒绝，
// 迁回本机后同一 Host 可以正常删除——守卫必须与 agent_configured 分支并列，
// 防止留下指向已消失主机的悬空归属记录。
func TestRemoveHostSafely_ProjectHomeGuard(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"h1"}`)

	const projectID = "proj-guard-1"
	app.mu.Lock()
	app.appendProjectLocked(model.Project{ID: projectID, Name: "demo-project", RootPath: t.TempDir()})
	app.mu.Unlock()
	require.NoError(t, app.projectHomeStore.SetHome(projectID, hostID))

	blocked := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
	require.Equal(t, http.StatusConflict, blocked.Code)
	assert.Contains(t, blocked.Body.String(), `"code":"project_home"`)
	assert.Contains(t, blocked.Body.String(), "demo-project", "detail 必须含项目名而不仅仅是 ID")
	_, hostFound, err := app.remoteHostByID(hostID)
	require.NoError(t, err)
	assert.True(t, hostFound, "守卫拒绝时 Host 不能被删除")

	// 迁回本机（SetHome ""）后守卫解除，Host 可以正常删除。
	require.NoError(t, app.projectHomeStore.SetHome(projectID, ""))
	deleted := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
	require.Equal(t, http.StatusOK, deleted.Code)
	_, hostFound, err = app.remoteHostByID(hostID)
	require.NoError(t, err)
	assert.False(t, hostFound, "迁回后 Host 必须可以被删除")
}

// TestRemoveHostSafely_ProjectHomeGuard_MultipleProjects 验证 detail 里的项目
// 名清单覆盖同一 Host 上的全部归属项目，不只是其中一个。
func TestRemoveHostSafely_ProjectHomeGuard_MultipleProjects(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"h1"}`)

	app.mu.Lock()
	app.appendProjectLocked(model.Project{ID: "proj-a", Name: "alpha-svc", RootPath: t.TempDir()})
	app.appendProjectLocked(model.Project{ID: "proj-b", Name: "beta-svc", RootPath: t.TempDir()})
	app.mu.Unlock()
	require.NoError(t, app.projectHomeStore.SetHome("proj-a", hostID))
	require.NoError(t, app.projectHomeStore.SetHome("proj-b", hostID))

	blocked := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
	require.Equal(t, http.StatusConflict, blocked.Code)
	assert.Contains(t, blocked.Body.String(), "alpha-svc")
	assert.Contains(t, blocked.Body.String(), "beta-svc")
}

// TestRemoveHostSafely_ProjectHomeGuard_MissingProjectDegradesToID 验证归属
// 记录指向一个已经从项目列表消失的项目 ID（异常态：项目被移除但归属记录未
// 清理）时，守卫依旧拦截删除，detail 退化为直接展示该 ID，而不是 panic
// 或吞掉整条记录悄悄放行删除。
func TestRemoveHostSafely_ProjectHomeGuard_MissingProjectDegradesToID(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"h1"}`)

	const ghostProjectID = "proj-ghost"
	require.NoError(t, app.projectHomeStore.SetHome(ghostProjectID, hostID))

	blocked := httptestDo(t, app, http.MethodDelete, "/api/hosts/"+hostID, nil)
	require.Equal(t, http.StatusConflict, blocked.Code)
	assert.Contains(t, blocked.Body.String(), `"code":"project_home"`)
	assert.Contains(t, blocked.Body.String(), ghostProjectID, "项目已不存在时应退化为展示 ID")
}

// TestUpdateHost_DevMachineModeOff_ReportsHomedProjects 验证关闭
// dev_machine_mode（true→false）时，响应体附带 homed_projects——非阻断提示，
// 归属记录本身不受影响（spec 裁定：关闭开关不动归属）。
func TestUpdateHost_DevMachineModeOff_ReportsHomedProjects(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"dev01","dev_machine_mode":true}`)

	const projectID = "proj-notice-1"
	app.mu.Lock()
	app.appendProjectLocked(model.Project{ID: projectID, Name: "notice-project", RootPath: t.TempDir()})
	app.mu.Unlock()
	require.NoError(t, app.projectHomeStore.SetHome(projectID, hostID))

	offResp := httptestDo(t, app, http.MethodPut, "/api/hosts/"+hostID, bytes.NewBufferString(`{"name":"dev01","dev_machine_mode":false}`))
	require.Equal(t, http.StatusOK, offResp.Code)

	var view struct {
		HomedProjects []string `json:"homed_projects"`
	}
	require.NoError(t, json.Unmarshal(offResp.Body.Bytes(), &view))
	assert.Equal(t, []string{"notice-project"}, view.HomedProjects)

	// 更新成功且归属记录原样保留——关闭开关不触发任何归属变更。
	assert.Equal(t, hostID, app.projectHomeStore.HomeOf(projectID), "关闭开发机模式不得改变归属")
}

// TestUpdateHost_DevMachineModeOn_OmitsHomedProjects 验证开启方向
// （false→true）不携带 homed_projects——该字段只用于关闭动作的非阻断提示，
// 开启没有"停止镜像"的后果需要警示。
func TestUpdateHost_DevMachineModeOn_OmitsHomedProjects(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"dev01"}`)

	const projectID = "proj-notice-2"
	app.mu.Lock()
	app.appendProjectLocked(model.Project{ID: projectID, Name: "notice-project-2", RootPath: t.TempDir()})
	app.mu.Unlock()
	require.NoError(t, app.projectHomeStore.SetHome(projectID, hostID))

	onResp := httptestDo(t, app, http.MethodPut, "/api/hosts/"+hostID, bytes.NewBufferString(`{"name":"dev01","dev_machine_mode":true}`))
	require.Equal(t, http.StatusOK, onResp.Code)
	assert.NotContains(t, onResp.Body.String(), "homed_projects", "开启方向不应携带 homed_projects")
}

// TestUpdateHost_DevMachineModeOff_NoHomedProjectsOmitsField 验证关闭时若该
// Host 当前没有任何项目归属，响应体不应携带空的 homed_projects 字段
// （omitempty 语义，前端据此判断"无需提示"）。
func TestUpdateHost_DevMachineModeOff_NoHomedProjectsOmitsField(t *testing.T) {
	app := newTestAppForPackage(t)
	hostID := createGuardTestHost(t, app, `{"name":"dev02","dev_machine_mode":true}`)

	offResp := httptestDo(t, app, http.MethodPut, "/api/hosts/"+hostID, bytes.NewBufferString(`{"name":"dev02","dev_machine_mode":false}`))
	require.Equal(t, http.StatusOK, offResp.Code)
	assert.NotContains(t, offResp.Body.String(), "homed_projects")
}
