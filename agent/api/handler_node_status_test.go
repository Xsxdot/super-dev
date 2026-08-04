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
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

type nodeStatusSampler struct{}

func (nodeStatusSampler) Sample(ctx context.Context, target metrics.SampleTarget) (model.InstanceMetrics, error) {
	return model.InstanceMetrics{Health: model.HealthRunning, Base: target.Base}, nil
}

func TestListHostsIncludesRemoteAgentNodeIdentity(t *testing.T) {
	registry := noderegistry.New(nil, noderegistry.Options{})
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeRegistryOverride: registry})
	require.NoError(t, err)
	defer app.Close()

	created := httptestDo(t, app, http.MethodPost, "/api/hosts", bytes.NewBufferString(`{
	  "id":"remote-linux",
	  "name":"remote-linux",
	  "tags":["superdev-validation-dedicated-resettable"]
	}`))
	require.Equal(t, http.StatusOK, created.Code)
	registry.ApplyForTest([]nodetransport.NodeStatus{{
		HostID: "remote-linux", Reachable: true,
		System: &remoteobservation.SystemFacts{AgentNodeID: "agent-node-01"},
	}})

	response := httptestDo(t, app, http.MethodGet, "/api/hosts", nil)
	require.Equal(t, http.StatusOK, response.Code)
	var hosts []hostViewDTO
	require.NoError(t, json.NewDecoder(response.Body).Decode(&hosts))
	require.Len(t, hosts, 2)
	assert.False(t, hosts[1].IsSelf)
	assert.Equal(t, "agent-node-01", hosts[1].NodeID)
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

	srv := httptest.NewServer(testServerHandler(app))
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

	srv := httptest.NewServer(testServerHandler(app))
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

// TestNodeStatusSnapshotCarriesPortsAndStoppedInstances 是端口镜像功能的诊断性测试。
//
// 诊断目的：Snapshot 对「已配置但未启动」的本机 managed deployment 是否仍产出实例条目，
// 决定 Ports 字段能否直接透传，还是需要先把运行态服务改为「managed deployment 全量产出」。
// 断言顺序刻意分层：先判定条目是否存在（若不存在，在此处失败，即为「不产出」分支的证据），
// 再判定 Ports/Health 取值（若条目存在但字段不对，在此处失败，即为「已产出」分支的证据）。
func TestNodeStatusSnapshotCarriesPortsAndStoppedInstances(t *testing.T) {
	app := newTestAppForPackage(t)

	const projectID = "proj-portmirror"
	const depID = "dep-portmirror"
	dep := model.Deployment{
		ID:          depID,
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Command:     "sleep 30",
		WorkDir:     t.TempDir(),
		Ports:       []int{9100},
	}
	project := model.Project{
		ID:   projectID,
		Name: projectID,
		Services: []model.Service{{
			ID:          "svc-portmirror",
			ProjectID:   projectID,
			Name:        "api",
			Deployments: []model.Deployment{dep},
		}},
	}
	// 直接注入 app.projects/managedProjectIDs：走 /api/managed-deployments 的
	// PUT 流程会经 managedProjectsFromDeployments 转换，而该转换目前不透传 Ports
	// （model.ManagedDeployment 本身也没有 Ports 字段），不是本任务改动范围。
	// 这里用常规 Project 配置 + 直接标记 managed，绕开该无关流程。
	app.mu.Lock()
	app.projects = append(app.projects, project)
	app.managedProjectIDs[projectID] = struct{}{}
	app.mu.Unlock()

	ctx := context.Background()
	findInstance := func(snap nodetransport.NodeStatus) *model.InstanceStatus {
		for i := range snap.Deployments {
			if snap.Deployments[i].DeploymentID == depID {
				return &snap.Deployments[i]
			}
		}
		return nil
	}

	beforeSnap := app.nodeStatusSnapshot(ctx, "h1", "ali-01")
	beforeInst := findInstance(beforeSnap)
	require.NotNil(t, beforeInst, "diagnostic: stopped managed deployment produced no instance entry — full-emission rework is required")
	assert.Equal(t, model.HealthStopped, beforeInst.Metrics.Health)
	assert.Equal(t, []int{9100}, beforeInst.Ports)

	require.NoError(t, app.startDeploymentRuntime(ctx, projectID, dep, intentStartNormal))
	mgr := app.getOrCreateManager(projectID)
	t.Cleanup(func() { mgr.StopDeployment(depID) })

	require.Eventually(t, func() bool {
		afterInst := findInstance(app.nodeStatusSnapshot(ctx, "h1", "ali-01"))
		return afterInst != nil && afterInst.Metrics.Health == model.HealthRunning
	}, 2*time.Second, 20*time.Millisecond, "expected health to become running after a real process start")

	afterInst := findInstance(app.nodeStatusSnapshot(ctx, "h1", "ali-01"))
	require.NotNil(t, afterInst)
	assert.Equal(t, model.HealthRunning, afterInst.Metrics.Health)
	assert.Equal(t, []int{9100}, afterInst.Ports)
}

// TestNodeStatusSnapshotDesktopOnlineTracksWsNodesConnections 验证「桌面端在线」信号链：
// /ws/nodes 连接建立后 DesktopOnline 变 true，断开后变回 false。信号来源见
// nodetransport.NodeStatus.DesktopOnline 字段注释——本机是否有活跃 /ws/nodes 订阅。
func TestNodeStatusSnapshotDesktopOnlineTracksWsNodesConnections(t *testing.T) {
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), NodeRegistryOverride: reg})
	require.NoError(t, err)
	defer app.Close()

	ctx := context.Background()
	assert.False(t, app.nodeStatusSnapshot(ctx, "h1", "ali-01").DesktopOnline,
		"没有任何 /ws/nodes 连接时 DesktopOnline 应为 false")

	srv := httptest.NewServer(testServerHandler(app))
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/nodes"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return app.nodeStatusSnapshot(ctx, "h1", "ali-01").DesktopOnline
	}, time.Second, 10*time.Millisecond, "/ws/nodes 连接建立后 DesktopOnline 应变为 true")

	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		return !app.nodeStatusSnapshot(ctx, "h1", "ali-01").DesktopOnline
	}, time.Second, 10*time.Millisecond, "/ws/nodes 断开后 DesktopOnline 应恢复为 false")
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
	req.Header.Set("Authorization", "Bearer "+app.LocalAccessToken())
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"host_id":"h1"`)

	srv := httptest.NewServer(testServerHandler(app))
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
