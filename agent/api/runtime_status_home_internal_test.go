// runtime_status_home_internal_test.go 钉死「归属转移之后，dev 部署的运行态
// 必须来自归属机的节点帧，而不是本机的进程管理器」。
//
// 职责：覆盖 Snapshot 对 location:local + 项目已归属他机 这一组合的分派。
// 边界：不真实拉起进程、不走真实传输层——节点帧由 ApplyForTest 直接注入。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// stoppedSampler 恒定回答「没跑」：本机采样器对一个不在本机的进程只可能这么答，
// 用它把「读错了机器」的失败形态固化下来——如果实现回落到本机采样，断言必红。
type stoppedSampler struct{}

func (stoppedSampler) Sample(context.Context, metrics.SampleTarget) (model.InstanceMetrics, error) {
	return model.InstanceMetrics{Health: model.HealthStopped, Base: "language"}, nil
}

const homeRuntimeStatusConfig = `
id: homed-runtime
name: homed-runtime
environments:
  - name: dev
    is_dev: true
services:
  - id: svc-proxy
    name: proxy
    deployments:
      - id: dep-proxy-dev
        env: dev
        location: local
`

func TestRuntimeStatusReadsHomeNodeFrameForHomedDevDeployment(t *testing.T) {
	app, reg := newHomeRuntimeStatusApp(t)
	projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
	createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
	require.NoError(t, app.projectHomeStore.SetHome(projectID, "host-dev", "/root/workspace/homed-runtime"))

	reg.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    "host-dev",
		Name:      "linux-01",
		Reachable: true,
		Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
		Deployments: []model.InstanceStatus{{
			DeploymentID: "dep-proxy-dev",
			Metrics:      model.InstanceMetrics{Health: model.HealthRunning, Base: "language"},
			Ports:        []int{8080},
		}},
		UpdatedAt: time.Now().UTC(),
	}})

	inst := onlyHomeRuntimeStatusInstance(t, app, projectID)

	assert.Equal(t, model.HealthRunning, inst.Metrics.Health, "归属机帧里是 running，本机不该report stopped")
	assert.Equal(t, "linux-01", inst.NodeName)
	assert.False(t, inst.IsLocal, "进程不在本机，IsLocal 必须为 false")
	assert.Equal(t, []int{8080}, inst.Ports)
	assert.Empty(t, inst.Error)
}

// TestRuntimeStatusStaysLocalWhenProjectHomedHere 钉死不越界：归属在本机
// （SetHome 从未调用）时仍走本机采样，不去查任何节点帧。
func TestRuntimeStatusStaysLocalWhenProjectHomedHere(t *testing.T) {
	app, _ := newHomeRuntimeStatusApp(t)
	projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)

	inst := onlyHomeRuntimeStatusInstance(t, app, projectID)

	assert.True(t, inst.IsLocal)
	assert.Equal(t, model.HealthStopped, inst.Metrics.Health)
}

// TestRuntimeStatusHomedDevDeploymentSurfacesUnreachableHome 钉死归属机失联时
// 不伪装成 stopped：用户必须能区分「服务停了」和「我看不到那台机器」。
func TestRuntimeStatusHomedDevDeploymentSurfacesUnreachableHome(t *testing.T) {
	app, _ := newHomeRuntimeStatusApp(t)
	projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
	createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
	require.NoError(t, app.projectHomeStore.SetHome(projectID, "host-dev", ""))
	// 刻意不注入任何节点帧：等价于归属机从未上报过。

	inst := onlyHomeRuntimeStatusInstance(t, app, projectID)

	assert.Equal(t, model.HealthUnknown, inst.Metrics.Health)
	assert.NotEmpty(t, inst.Error)
}

func newHomeRuntimeStatusApp(t *testing.T) (*App, *noderegistry.Registry) {
	t.Helper()
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	app, err := NewApp(AppConfig{
		DataDir:                     t.TempDir(),
		RuntimeMetricsSampler:       stoppedSampler{},
		RuntimeStatusRequestTimeout: 200 * time.Millisecond,
		NodeRegistryOverride:        reg,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app, reg
}

func addHomeRuntimeStatusProject(t *testing.T, app *App, cfg string) string {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(cfg), 0o644))

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var project model.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&project))
	return project.ID
}

func createHomeRuntimeStatusHost(t *testing.T, app *App, id, name string) {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/hosts", strings.NewReader(`{"id":"`+id+`","name":"`+name+`","private_ip":"127.0.0.1"}`))
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func onlyHomeRuntimeStatusInstance(t *testing.T, app *App, projectID string) model.InstanceStatus {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	return got.Environments[0].Instances[0]
}
