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
	require.Empty(t, <-snapCh)

	tr.ch <- []nodetransport.NodeStatus{nodeStatus("h2", "jp", true)}
	got := <-snapCh
	require.Len(t, got, 1)
	assert.Equal(t, "h2", got[0].HostID)
	assert.True(t, got[0].Reachable)

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
