// handler_node_status_test.go 验证节点状态上报和桌面端节点快照接口。
//
// 职责：
//   - 覆盖远端 agent /ws/node-status 的首帧内容
//   - 覆盖桌面端 /api/nodes 与 /ws/nodes 读取 NodeRegistry 快照
//
// 边界：
//   - 不建立真实 SSH 隧道
//   - 不测试前端 nodeStore
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type nodeStatusSampler struct{}

func (nodeStatusSampler) Sample(ctx context.Context, target metrics.SampleTarget) (model.InstanceMetrics, error) {
	return model.InstanceMetrics{Health: model.HealthRunning, Base: target.Base}, nil
}

func TestWsNodeStatusReportsManagedRuntimeAndCollectors(t *testing.T) {
	app, err := NewApp(AppConfig{
		DataDir:               t.TempDir(),
		RuntimeMetricsSampler: nodeStatusSampler{},
	})
	require.NoError(t, err)
	defer app.Close()

	desired := []model.ManagedDeployment{{
		DeploymentID: "dep-1",
		ServiceID:    "svc-1",
		ServiceName:  "api",
		ProjectID:    "proj-1",
		EnvName:      "prod",
		Runtime:      &model.RuntimeConfig{Type: model.RuntimeTypeSystemd, ServiceName: "api.service"},
		Logs:         &model.LogConfig{Type: model.LogKindCommand, Command: "printf ok"},
		Location:     model.LocationLocal,
	}}
	body, err := json.Marshal(desired)
	require.NoError(t, err)
	putReq := httptest.NewRequest(http.MethodPut, "/api/managed-deployments", bytes.NewReader(body))
	putRR := httptest.NewRecorder()
	app.putManagedDeployments(putRR, putReq)
	require.Equal(t, http.StatusOK, putRR.Code)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/node-status?host_id=h1&host_name=ali-01"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	var batch []nodetransport.NodeStatus
	require.NoError(t, conn.ReadJSON(&batch))
	require.Len(t, batch, 1)
	status := batch[0]
	assert.Equal(t, "h1", status.HostID)
	assert.Equal(t, "ali-01", status.Name)
	assert.True(t, status.Reachable)
	assert.Equal(t, model.AgentHealthHealthy, status.Agent.Health)
	assert.Equal(t, agentAPIVersion, status.Agent.Version)
	require.NotNil(t, status.Managed)
	assert.Equal(t, 1, status.Managed.DeploymentCount)
	require.Len(t, status.Deployments, 1)
	assert.Equal(t, "dep-1", status.Deployments[0].DeploymentID)
	assert.Equal(t, "h1", status.Deployments[0].NodeID)
	assert.False(t, status.Deployments[0].IsLocal)
}

func TestWsNodeStatusPushesManagedDeploymentChanges(t *testing.T) {
	app, err := NewApp(AppConfig{
		DataDir:               t.TempDir(),
		RuntimeMetricsSampler: nodeStatusSampler{},
	})
	require.NoError(t, err)
	defer app.Close()

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/node-status?host_id=h1&host_name=ali-01"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	var initial []nodetransport.NodeStatus
	require.NoError(t, conn.ReadJSON(&initial))
	require.Len(t, initial, 1)
	require.Empty(t, initial[0].Deployments)

	desired := []model.ManagedDeployment{{
		DeploymentID: "dep-2",
		ServiceID:    "svc-2",
		ServiceName:  "worker",
		ProjectID:    "proj-2",
		EnvName:      "prod",
		Runtime:      &model.RuntimeConfig{Type: model.RuntimeTypeSystemd, ServiceName: "worker.service"},
		Location:     model.LocationLocal,
	}}
	body, err := json.Marshal(desired)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/managed-deployments", bytes.NewReader(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	var pushed []nodetransport.NodeStatus
	require.NoError(t, conn.ReadJSON(&pushed))
	require.Len(t, pushed, 1)
	require.Len(t, pushed[0].Deployments, 1)
	assert.Equal(t, "dep-2", pushed[0].Deployments[0].DeploymentID)
}

func TestNodeEndpointsExposeRegistrySnapshot(t *testing.T) {
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	app, err := NewApp(AppConfig{
		DataDir:              t.TempDir(),
		NodeRegistryOverride: reg,
	})
	require.NoError(t, err)
	defer app.Close()
	reg.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    "h1",
		Name:      "ali-01",
		Reachable: true,
		Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
		UpdatedAt: time.Now().UTC(),
	}})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"host_id":"h1"`)

	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/nodes"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()
	var batch []nodetransport.NodeStatus
	require.NoError(t, conn.ReadJSON(&batch))
	require.Len(t, batch, 1)
	assert.Equal(t, "h1", batch[0].HostID)
}
