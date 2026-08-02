// node_status_publisher_test.go 验证远端节点状态 publisher 的推送时机。
//
// 职责：
//   - 覆盖订阅后立即发送节点快照
//   - 覆盖 managed 状态变化信号触发即时推送
//   - 覆盖 deployment 状态变更（process.Manager.onStatusChange 回调）触发的即时推送，
//     证明其为事件帧而非 5s 周期心跳
//
// 边界：
//   - 不测试桌面端 NodeRegistry 消费逻辑
package api

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

func TestNodeStatusPublisherPushesImmediatelyOnSignal(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	pub := newNodeStatusPublisher(app, "h1", "ali-01", time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := pub.Subscribe(ctx)

	initial := <-ch
	require.Len(t, initial, 1)
	require.Equal(t, "h1", initial[0].HostID)

	pub.Signal()

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].HostID == "h1"
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

// TestDeploymentStatusChangeSignalsNodeStatusPublishers 验证本机 deployment 状态变更
// （process.Manager.onStatusChange 回调 → a.signalNodeStatusPublishers）能立刻催发一帧
// /ws/node-status 推送，不必等 5s 周期心跳（handler_node_status.go:25 的
// nodeStatusReportInterval）。这是端口镜像 <2s 时延目标的机制来源。
//
// 时延证据：ReadJSON 的 500ms 截止时间远小于 5s 心跳周期——若能在此窗口内收到新帧，
// 该帧只可能来自事件驱动的 signal 路径，不可能是周期心跳（心跳要再等 4.5s 以上才会触发）。
func TestDeploymentStatusChangeSignalsNodeStatusPublishers(t *testing.T) {
	app := newTestAppForPackage(t)
	srv := newHTTPServerForPackage(t, app)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/node-status?host_id=h1&host_name=ali-01"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer conn.Close()

	var initial []nodetransport.NodeStatus
	require.NoError(t, conn.ReadJSON(&initial))
	require.Len(t, initial, 1)

	const projectID = "proj-status-signal"
	const depID = "dep-status-signal"
	dep := model.Deployment{
		ID:       depID,
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 30",
		WorkDir:  t.TempDir(),
	}
	project := model.Project{
		ID:   projectID,
		Name: projectID,
		Services: []model.Service{{
			ID:          "svc-status-signal",
			ProjectID:   projectID,
			Name:        "api",
			Deployments: []model.Deployment{dep},
		}},
	}
	app.mu.Lock()
	app.projects = append(app.projects, project)
	app.mu.Unlock()

	require.NoError(t, app.startDeploymentRuntime(context.Background(), projectID, dep, intentStartNormal))
	mgr := app.getOrCreateManager(projectID)
	t.Cleanup(func() { mgr.StopDeployment(depID) })

	// 500ms 远小于 5s 心跳周期：若能在此窗口内读到新帧，证明推送来自状态变更信号，
	// 而不是周期心跳（心跳要再等 4.5s 以上才会触发）。
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
	var pushed []nodetransport.NodeStatus
	err = conn.ReadJSON(&pushed)
	require.NoError(t, err, "expected an event-driven node-status frame within 500ms of a deployment status change (well inside the 5s heartbeat period)")
	require.Len(t, pushed, 1)
}
