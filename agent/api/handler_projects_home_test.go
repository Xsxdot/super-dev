// handler_projects_home_test.go 验证 GET /api/projects 透出项目归属信息。
//
// 白盒测试（package api）：Task 2 尚未提供 HTTP 层的归属设置入口（归属切换的
// 路由与转移是后续任务），这里直接操作 App 内部的 projectHomeStore/remoteStore
// 模拟"归属已设置"的状态，只验证 listProjects DTO 的组装是否正确。
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

// TestListProjectsCarriesHomeHost 验证设置项目归属后，GET /api/projects 响应
// 携带 home_host_id 和 home_host_name；归属主机若已被删除（remoteStore 查不到），
// name 必须为空但不能导致接口 500——优雅降级，主机删除守卫是另一层职责。
func TestListProjectsCarriesHomeHost(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	dir := t.TempDir()
	addBody := `{"root_path": "` + dir + `"}`
	addResp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer addResp.Body.Close()
	require.Equal(t, http.StatusOK, addResp.StatusCode)

	var created model.Project
	require.NoError(t, json.NewDecoder(addResp.Body).Decode(&created))
	require.NotEmpty(t, created.ID)

	// 归属指向一台从未在 remoteStore 注册过的主机 ID，模拟"主机已被删除"。
	require.NoError(t, app.projectHomeStore.SetHome(created.ID, "host-ghost"))

	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()
	require.Equal(t, http.StatusOK, listResp.StatusCode, "归属主机缺失时不应 500")

	var projects []model.Project
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&projects))
	require.Len(t, projects, 1)
	assert.Equal(t, "host-ghost", projects[0].HomeHostID, "ID 必须保留，供后续 UI 提示/清理判断")
	assert.Empty(t, projects[0].HomeHostName, "主机已删除时 Name 应为空，不能 panic 或报错")

	// 补上真实存在的主机后，Name 应正确回填。
	_, err = app.remoteStore.AddHost(model.Host{ID: "host-real", Name: "Real Host"})
	require.NoError(t, err)
	require.NoError(t, app.projectHomeStore.SetHome(created.ID, "host-real"))

	listResp2, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp2.Body.Close()
	require.Equal(t, http.StatusOK, listResp2.StatusCode)

	var projects2 []model.Project
	require.NoError(t, json.NewDecoder(listResp2.Body).Decode(&projects2))
	require.Len(t, projects2, 1)
	assert.Equal(t, "host-real", projects2[0].HomeHostID)
	assert.Equal(t, "Real Host", projects2[0].HomeHostName)
}

// TestListProjectsOmitsHomeFieldsWhenLocal 验证从未设置过归属的项目（归属本机）
// 不携带 home_host_id/home_host_name（omitempty，前端据此判断"本机"这一默认态）。
func TestListProjectsOmitsHomeFieldsWhenLocal(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	dir := t.TempDir()
	addBody := `{"root_path": "` + dir + `"}`
	addResp, err := http.Post(srv.URL+"/api/projects", "application/json", strings.NewReader(addBody))
	require.NoError(t, err)
	defer addResp.Body.Close()
	require.Equal(t, http.StatusOK, addResp.StatusCode)

	listResp, err := http.Get(srv.URL + "/api/projects")
	require.NoError(t, err)
	defer listResp.Body.Close()

	var raw []map[string]any
	require.NoError(t, json.NewDecoder(listResp.Body).Decode(&raw))
	require.Len(t, raw, 1)
	_, hasID := raw[0]["home_host_id"]
	_, hasName := raw[0]["home_host_name"]
	assert.False(t, hasID, "本机归属不应携带 home_host_id 字段")
	assert.False(t, hasName, "本机归属不应携带 home_host_name 字段")
}
