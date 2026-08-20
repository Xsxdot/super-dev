// Package noderegistry_test 验证本地内存节点状态中心。
//
// 职责：
//   - 覆盖状态流合并、订阅初始快照、断流/过期置 unreachable
//   - 确认单节点异常不会阻塞其它节点快照更新
//
// 边界：
//   - 不建立真实 SSH 隧道
//   - 不测试 HTTP handler
package noderegistry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type fakeTransport struct {
	covers []string
	ch     chan []nodetransport.NodeStatus
}

func newFakeTransport(covers ...string) *fakeTransport {
	return &fakeTransport{covers: covers, ch: make(chan []nodetransport.NodeStatus, 16)}
}

func (f *fakeTransport) Do(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	return nodetransport.NodeResponse{}, nodetransport.ErrHostUnreachable
}

func (f *fakeTransport) Stream(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
	return nil, nodetransport.ErrHostUnreachable
}

func (f *fakeTransport) SubscribeNodes(ctx context.Context) (<-chan []nodetransport.NodeStatus, func()) {
	out := make(chan []nodetransport.NodeStatus, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-f.ch:
				if !ok {
					return
				}
				select {
				case out <- batch:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, func() {}
}

func (f *fakeTransport) Covers() []string { return append([]string(nil), f.covers...) }

func nodeStatus(hostID, name string, reachable bool) nodetransport.NodeStatus {
	health := model.AgentHealthHealthy
	if !reachable {
		health = model.AgentHealthUnreachable
	}
	return nodetransport.NodeStatus{
		HostID:    hostID,
		Name:      name,
		Reachable: reachable,
		Agent: model.AgentRuntime{
			Installed: reachable,
			Version:   "0.1.0",
			Health:    health,
			Reachable: reachable,
		},
		UpdatedAt: time.Now().UTC(),
	}
}

// nodeStatusWithDeployments 构造一个带指定数量 deployment 实例的可达节点帧，
// 用于验证「不完整帧不应抹掉已知服务条目」的兜底逻辑。
func nodeStatusWithDeployments(hostID, name string, deploymentIDs ...string) nodetransport.NodeStatus {
	status := nodeStatus(hostID, name, true)
	insts := make([]model.InstanceStatus, 0, len(deploymentIDs))
	for _, depID := range deploymentIDs {
		insts = append(insts, model.InstanceStatus{
			DeploymentID: depID,
			ServiceID:    depID,
			ServiceName:  depID,
			NodeID:       hostID,
			Metrics:      model.InstanceMetrics{Health: model.HealthRunning},
		})
	}
	status.Deployments = insts
	return status
}

func deploymentIDsOf(status nodetransport.NodeStatus) []string {
	out := make([]string, 0, len(status.Deployments))
	for _, inst := range status.Deployments {
		out = append(out, inst.DeploymentID)
	}
	return out
}

// TestForgetRemovesNodeAndBroadcasts 钉死「卸载是权威事件，快照必须消失」。
//
// 为什么不是「把 installed 改成 false」而是整条移除：控制面对这台机器已经
// 一无所知（agent 配置也一并删了），保留一条 installed=false 的空壳快照等于
// 用另一个断言替换旧断言。没有就是没有。
func TestForgetRemovesNodeAndBroadcasts(t *testing.T) {
	r := noderegistry.New(nil, noderegistry.Options{})
	r.ApplyForTest([]nodetransport.NodeStatus{{
		HostID: "h1",
		Agent:  model.AgentRuntime{Installed: true, Version: "0.2.3", Reachable: true},
	}})
	_, ok := r.SnapshotOf("h1")
	require.True(t, ok)

	ch, cancel := r.Subscribe()
	defer cancel()
	// Subscribe 建立订阅时会立刻推一帧当前快照，先排掉它，否则下面的 select
	// 会读到 Forget 之前的那一帧而假通过。
	<-ch

	r.Forget("h1")

	_, ok = r.SnapshotOf("h1")
	require.False(t, ok, "Forget 之后不应再有该 host 的快照")

	select {
	case snap := <-ch:
		for _, n := range snap {
			require.NotEqual(t, "h1", n.HostID)
		}
	case <-time.After(time.Second):
		t.Fatal("Forget 应当广播一次新快照")
	}
}

// TestForgetUnknownHostIsNoop 钉死重复卸载/卸载未知 host 不 panic、不广播。
func TestForgetUnknownHostIsNoop(t *testing.T) {
	r := noderegistry.New(nil, noderegistry.Options{})
	ch, cancel := r.Subscribe()
	defer cancel()
	<-ch // 排掉 Subscribe 的初始帧，见上一条测试的说明

	r.Forget("nobody")

	select {
	case <-ch:
		t.Fatal("未命中任何 host 时不应广播")
	case <-time.After(200 * time.Millisecond):
	}
}

func snapshotByHost(snapshot []nodetransport.NodeStatus) map[string]nodetransport.NodeStatus {
	out := map[string]nodetransport.NodeStatus{}
	for _, status := range snapshot {
		out[status.HostID] = status
	}
	return out
}

func TestRegistrySnapshotAndSubscribe(t *testing.T) {
	tr := newFakeTransport("h1", "h2")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{
		StaleAfter: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	snapCh, unsubscribe := reg.Subscribe()
	defer unsubscribe()
	initial := <-snapCh
	require.Len(t, initial, 2)
	assert.False(t, initial[0].Reachable)
	assert.False(t, initial[1].Reachable)
	assert.Equal(t, model.AgentHealthUnknown, initial[0].Agent.Health)
	assert.Equal(t, model.AgentHealthUnknown, initial[1].Agent.Health)

	tr.ch <- []nodetransport.NodeStatus{nodeStatus("h2", "jp", true)}
	got := <-snapCh
	require.Len(t, got, 2)
	h2, ok := snapshotByHost(got)["h2"]
	require.True(t, ok)
	assert.True(t, h2.Reachable)

	tr.ch <- []nodetransport.NodeStatus{nodeStatus("h1", "ali", true)}
	require.Eventually(t, func() bool {
		return len(reg.Snapshot()) == 2
	}, time.Second, 10*time.Millisecond)
	got = reg.Snapshot()
	assert.Equal(t, []string{"h1", "h2"}, []string{got[0].HostID, got[1].HostID})
}

func TestRegistryMarksTransportClosedNodesUnreachable(t *testing.T) {
	tr := newFakeTransport("h1")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{
		StaleAfter: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	tr.ch <- []nodetransport.NodeStatus{nodeStatus("h1", "ali", true)}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && got.Reachable
	}, time.Second, 10*time.Millisecond)

	close(tr.ch)
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && !got.Reachable && got.Agent.Health == model.AgentHealthUnreachable
	}, time.Second, 10*time.Millisecond)
}

func TestRegistryMarksStaleNodesUnreachableWithoutBlockingFreshNodes(t *testing.T) {
	tr := newFakeTransport("h1", "h2")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{
		StaleAfter:    40 * time.Millisecond,
		CheckInterval: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	tr.ch <- []nodetransport.NodeStatus{
		nodeStatus("h1", "slow", true),
		nodeStatus("h2", "fresh", true),
	}
	require.Eventually(t, func() bool {
		return len(reg.Snapshot()) == 2
	}, time.Second, 10*time.Millisecond)

	freshCtx, stopFresh := context.WithCancel(ctx)
	defer stopFresh()
	go func() {
		ticker := time.NewTicker(15 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-freshCtx.Done():
				return
			case <-ticker.C:
				tr.ch <- []nodetransport.NodeStatus{nodeStatus("h2", "fresh", true)}
			}
		}
	}()

	require.Eventually(t, func() bool {
		h1, ok1 := reg.SnapshotOf("h1")
		h2, ok2 := reg.SnapshotOf("h2")
		return ok1 && ok2 && !h1.Reachable && h2.Reachable
	}, time.Second, 10*time.Millisecond)
}

func TestRegistryPreseedsCoveredNodesAsUnknown(t *testing.T) {
	tr := newFakeTransport("h1", "h2")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{StaleAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	require.Eventually(t, func() bool {
		snap := reg.Snapshot()
		return len(snap) == 2 && !snap[0].Reachable && !snap[1].Reachable &&
			snap[0].Agent.Health == model.AgentHealthUnknown &&
			snap[1].Agent.Health == model.AgentHealthUnknown
	}, time.Second, 10*time.Millisecond)
}

func TestRegistryIgnoresFramesOutsideTransportCovers(t *testing.T) {
	tr := newFakeTransport("h1")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{StaleAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	tr.ch <- []nodetransport.NodeStatus{nodeStatus("h2", "foreign", true)}
	require.Never(t, func() bool {
		_, ok := reg.SnapshotOf("h2")
		return ok
	}, 100*time.Millisecond, 10*time.Millisecond)
}

// TestRegistryKeepsDeploymentsOnIncompleteReachableFrame 复现「节点中心服务数
// 6→1→6 跳变」：先收到带 3 个 deployment 的完整帧，再收到一个 reachable=true
// 但 deployment 数骤减的不完整帧（实时采集瞬时短缺）。Registry 应保留上一帧
// 已知的 deployment 列表，避免把瞬时短缺渲染成「服务消失」。
func TestRegistryKeepsDeploymentsOnIncompleteReachableFrame(t *testing.T) {
	tr := newFakeTransport("h1")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{StaleAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	// 完整帧：3 个服务。
	tr.ch <- []nodetransport.NodeStatus{nodeStatusWithDeployments("h1", "local-01", "d1", "d2", "d3")}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && len(got.Deployments) == 3
	}, time.Second, 10*time.Millisecond)

	// 不完整帧：reachable 仍为 true，但只剩 1 个 deployment。
	tr.ch <- []nodetransport.NodeStatus{nodeStatusWithDeployments("h1", "local-01", "d1")}

	// 保留上一帧的 3 个服务，不应回退到 1。
	require.Never(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && len(got.Deployments) != 3
	}, 200*time.Millisecond, 10*time.Millisecond)

	got, ok := reg.SnapshotOf("h1")
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"d1", "d2", "d3"}, deploymentIDsOf(got))
}

// TestRegistryAcceptsCompleteDeploymentFrame 确认兜底不会卡住正常更新：
// 当新帧 deployment 数不少于上一帧时，应正常覆盖（含服务上线带来的增长）。
func TestRegistryAcceptsCompleteDeploymentFrame(t *testing.T) {
	tr := newFakeTransport("h1")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{StaleAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	tr.ch <- []nodetransport.NodeStatus{nodeStatusWithDeployments("h1", "local-01", "d1", "d2")}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && len(got.Deployments) == 2
	}, time.Second, 10*time.Millisecond)

	// 新增一个服务，数量增长，应正常覆盖为 3。
	tr.ch <- []nodetransport.NodeStatus{nodeStatusWithDeployments("h1", "local-01", "d1", "d2", "d3")}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && len(got.Deployments) == 3
	}, time.Second, 10*time.Millisecond)
}

// TestRegistryDropsDeploymentsWhenNodeUnreachable 确认兜底只针对 reachable 帧：
// 节点真正变为 unreachable 时，deployments 应正常清空/更新，不被旧值粘住。
func TestRegistryDropsDeploymentsWhenNodeUnreachable(t *testing.T) {
	tr := newFakeTransport("h1")
	reg := noderegistry.New([]nodetransport.NodeTransport{tr}, noderegistry.Options{StaleAfter: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg.Start(ctx)

	tr.ch <- []nodetransport.NodeStatus{nodeStatusWithDeployments("h1", "local-01", "d1", "d2", "d3")}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && len(got.Deployments) == 3
	}, time.Second, 10*time.Millisecond)

	// 节点不可达：deployments 短缺是真实的，应如实反映。
	unreachable := nodeStatus("h1", "local-01", false)
	tr.ch <- []nodetransport.NodeStatus{unreachable}
	require.Eventually(t, func() bool {
		got, ok := reg.SnapshotOf("h1")
		return ok && !got.Reachable && len(got.Deployments) == 0
	}, time.Second, 10*time.Millisecond)
}
