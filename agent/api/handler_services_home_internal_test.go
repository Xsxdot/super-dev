// handler_services_home_internal_test.go 钉死 GET /api/services 的 deployment.status
// 在项目归属他机时来自归属机的节点帧，而不是本机进程管理器。
//
// 职责：覆盖侧边栏启停按钮/绿点、底栏、服务级聚合共同依赖的那个字段。
// 边界：不真实拉起进程、不走真实传输层——节点帧由 ApplyForTest 注入。
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// TestListServicesReadsHomeNodeFrameForHomedDeployment 钉死主路径。
//
// 为什么这条与 runtime-status 那条不能互相替代：它们是**两个**回答
// 「这个 deployment 归谁跑」的地方，界面读的也是两个不同字段。真机上
// runtime-status 已经修好、返回 running 之后，侧边栏依旧显示未运行、
// 按钮停在「启动」——因为侧边栏读的是这里的 deployment.status。
func TestListServicesReadsHomeNodeFrameForHomedDeployment(t *testing.T) {
	app, reg := newHomeRuntimeStatusApp(t)
	projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
	createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
	require.NoError(t, app.projectHomeStore.SetHome(projectID, "host-dev", "/root/workspace/homed-runtime"))

	applyHomeFrame(reg, model.HealthRunning)

	svc := onlyServiceFromList(t, app)
	require.Len(t, svc.Deployments, 1)
	assert.Equal(t, model.StatusRunning, svc.Deployments[0].Status, "归属机帧里是 running，本机不该报 stopped")
	assert.Equal(t, model.StatusRunning, svc.Status, "服务级聚合也必须跟着对，侧边栏的绿点读的是它")
	assert.Zero(t, svc.Deployments[0].PID, "进程不在本机，不该填一个会被当成本机 PID 的值")
}

// TestListServicesMapsHomeFrameHealth 钉死映射表，尤其 restarting 不能被说成 running
// ——那会让一次重启在界面上看起来从没发生过。
func TestListServicesMapsHomeFrameHealth(t *testing.T) {
	cases := map[model.Health]model.ServiceStatus{
		model.HealthRunning:    model.StatusRunning,
		model.HealthHealthy:    model.StatusRunning,
		model.HealthRestarting: model.StatusStarting,
		model.HealthFailed:     model.StatusFailed,
		model.HealthStopped:    model.StatusStopped,
		model.HealthUnknown:    model.StatusStopped,
	}
	for health, want := range cases {
		t.Run(string(health), func(t *testing.T) {
			app, reg := newHomeRuntimeStatusApp(t)
			projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
			createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
			require.NoError(t, app.projectHomeStore.SetHome(projectID, "host-dev", ""))
			applyHomeFrame(reg, health)

			svc := onlyServiceFromList(t, app)
			assert.Equal(t, want, svc.Deployments[0].Status)
		})
	}
}

// TestListServicesHomedDeploymentWithoutFrameReportsStopped 钉死归属机没上报时
// 的降级：报 stopped 而不是把上一次的本机采样漏出去。
func TestListServicesHomedDeploymentWithoutFrameReportsStopped(t *testing.T) {
	app, _ := newHomeRuntimeStatusApp(t)
	projectID := addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
	createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
	require.NoError(t, app.projectHomeStore.SetHome(projectID, "host-dev", ""))
	// 刻意不注入任何帧。

	svc := onlyServiceFromList(t, app)
	assert.Equal(t, model.StatusStopped, svc.Deployments[0].Status)
}

// TestListServicesStaysLocalWhenProjectHomedHere 钉死不越界：归属在本机时
// 依旧走本机进程管理器，不去查任何节点帧。
func TestListServicesStaysLocalWhenProjectHomedHere(t *testing.T) {
	app, reg := newHomeRuntimeStatusApp(t)
	addHomeRuntimeStatusProject(t, app, homeRuntimeStatusConfig)
	createHomeRuntimeStatusHost(t, app, "host-dev", "linux-01")
	// 帧里写 running，但项目没有归属——绝不能被它影响。
	applyHomeFrame(reg, model.HealthRunning)

	svc := onlyServiceFromList(t, app)
	assert.Equal(t, model.StatusStopped, svc.Deployments[0].Status, "没有归属就该以本机为准，本机没跑就是 stopped")
}

func applyHomeFrame(reg interface {
	ApplyForTest([]nodetransport.NodeStatus)
}, health model.Health) {
	reg.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    "host-dev",
		Name:      "linux-01",
		Reachable: true,
		Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
		Deployments: []model.InstanceStatus{{
			DeploymentID: "dep-proxy-dev",
			Metrics:      model.InstanceMetrics{Health: health, Base: "language"},
		}},
		UpdatedAt: time.Now().UTC(),
	}})
}

func onlyServiceFromList(t *testing.T, app *App) model.Service {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var services []model.Service
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&services))
	require.Len(t, services, 1)
	return services[0]
}
