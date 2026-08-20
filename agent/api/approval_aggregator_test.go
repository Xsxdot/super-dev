// approval_aggregator_test.go 覆盖归属机审批订阅集合动态对账与断线保留末次快照。
package api

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
)

// TestAggregatorSubscribesDesiredHostsAndDropsRemoved 钉死「订阅集合每轮现取」。
//
// 为什么这条重要：归属关系会随一次转移即时改变。同类坑本仓库踩过两次——
// nodeRegistry.Start 在进程启动时一次性算 coverage，导致新增主机后必须重启
// 控制面才进得了节点注册表；端口镜像的 KnownDeployments 也因此被要求每轮
// reconcile 现取。装配期快照必然过期。
func TestAggregatorSubscribesDesiredHostsAndDropsRemoved(t *testing.T) {
	var mu sync.Mutex
	opened := map[string]int{}
	closed := map[string]int{}

	hosts := []string{"h1"}
	g := newApprovalAggregator(approvalAggregatorDeps{
		HomeHosts: func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), hosts...)
		},
		Stream: func(ctx context.Context, hostID string, _ nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
			mu.Lock()
			opened[hostID]++
			mu.Unlock()
			return &fakeApprovalStream{
				ctx: ctx,
				onClose: func() {
					mu.Lock()
					closed[hostID]++
					mu.Unlock()
				},
			}, nil
		},
		OnChange: func() {},
	})
	defer g.Close()

	ctx := context.Background()
	g.Reconcile(ctx)
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return opened["h1"] == 1
	}, 2*time.Second, 20*time.Millisecond)

	// 归属集合变化：h1 移出、h2 加入
	mu.Lock()
	hosts = []string{"h2"}
	mu.Unlock()
	g.Reconcile(ctx)

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return opened["h2"] == 1 && closed["h1"] == 1
	}, 2*time.Second, 20*time.Millisecond)
}

// TestAggregatorKeepsLastSnapshotWhenUpstreamDrops 钉死「断线不等于没有待批」。
//
// 为什么：条目消失会让用户认为「没有待批」，而真相是「我们看不见了」。
// 与归属路由既有的「转发失败绝不静默回落本机」同源纪律：看不见 ≠ 不存在。
func TestAggregatorKeepsLastSnapshotWhenUpstreamDrops(t *testing.T) {
	frames := make(chan approvalsSnapshot, 1)
	frames <- approvalsSnapshot{Pending: []operation.Approval{{ID: "opa_1"}}}

	failNext := make(chan struct{})
	g := newApprovalAggregator(approvalAggregatorDeps{
		HomeHosts: func() []string { return []string{"h1"} },
		Stream: func(ctx context.Context, _ string, _ nodetransport.NodeRequest) (nodetransport.NodeStream, error) {
			return &fakeApprovalStream{ctx: ctx, frames: frames, failAfterDrain: failNext}, nil
		},
		OnChange: func() {},
	})
	defer g.Close()

	g.Reconcile(context.Background())
	require.Eventually(t, func() bool {
		st, ok := g.All()["h1"]
		return ok && st.Reachable && len(st.Snapshot.Pending) == 1
	}, 2*time.Second, 20*time.Millisecond)

	close(failNext) // 让上游读失败

	require.Eventually(t, func() bool {
		st, ok := g.All()["h1"]
		// 条目仍在、内容仍是末次已知，但被标记为不可达且带原因
		return ok && !st.Reachable && st.Err != "" && len(st.Snapshot.Pending) == 1
	}, 3*time.Second, 20*time.Millisecond)
}

// fakeApprovalStream 是审批聚合器测试用的只读 JSON 流。
type fakeApprovalStream struct {
	ctx            context.Context
	frames         <-chan approvalsSnapshot
	failAfterDrain <-chan struct{}
	onClose        func()
	closeOnce      sync.Once
}

func (s *fakeApprovalStream) ReadJSON(v any) error {
	select {
	case frame := <-s.frames:
		*snapshotTarget(v) = frame
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-s.failAfterDrain:
		return io.EOF
	}
}

func (s *fakeApprovalStream) WriteJSON(any) error { return nil }

func (s *fakeApprovalStream) Close() error {
	s.closeOnce.Do(func() {
		if s.onClose != nil {
			s.onClose()
		}
	})
	return nil
}

func snapshotTarget(v any) *approvalsSnapshot {
	return v.(*approvalsSnapshot)
}
