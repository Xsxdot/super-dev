// handler_projects_test.go 验证项目探测与创建分离的行为。
package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
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
