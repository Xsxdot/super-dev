// approval_aggregator.go 维护对各归属机审批流的只读订阅，供本控制面把
// 外来审批合并进自己的 /ws/operation-approvals 快照。
//
// 职责：
//   - 按「本控制面已知项目的归属机」集合增删上游订阅，集合每轮现取
//   - 持有每个来源的末次已知快照与可达性
//   - 上游帧到达时通知调用方（用于扇出到本机 WS 订阅者）
//
// 边界：
//   - 只读。绝不把外来审批写进本机 approval store——本机 store 是持久化的，
//     写进去就有了本机的过期扫描与裁决状态，而权威副本在源节点，两份同 id
//     各自演进是最难排查的一类不一致
//   - 不做裁决、不签发 token、不做管辖过滤（那是合并侧的职责）
//   - 不认识 HTTP：转发与路由由 handler 负责
package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/nodetransport"
	"github.com/xsxdot/super-dev/agent/operation"
)

// remoteApprovals 是单个来源的末次已知审批快照与可达性。
type remoteApprovals struct {
	Snapshot  approvalsSnapshot
	Reachable bool
	// Err 是不可达时的原因原文；Reachable 为 true 时恒为空。
	Err       string
	UpdatedAt time.Time
}

type approvalAggregatorDeps struct {
	// HomeHosts 返回当前应当订阅的归属机 ID 列表。**每轮现取**，不做装配期快照。
	HomeHosts func() []string
	Stream    func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeStream, error)
	// OnChange 在任一来源的快照或可达性发生变化时调用，用于扇出。
	OnChange func()
	// RetryDelay 是上游断开后的重连间隔；零值用 approvalAggregatorDefaultRetry。
	RetryDelay time.Duration
}

const approvalAggregatorDefaultRetry = 5 * time.Second

type approvalAggregator struct {
	deps approvalAggregatorDeps

	mu            sync.RWMutex
	byHost        map[string]remoteApprovals
	subscriptions map[string]*approvalAggregationSubscription
	closed        bool
	wg            sync.WaitGroup
}

type approvalAggregationSubscription struct {
	cancel context.CancelFunc
}

func newApprovalAggregator(deps approvalAggregatorDeps) *approvalAggregator {
	if deps.HomeHosts == nil {
		deps.HomeHosts = func() []string { return nil }
	}
	if deps.RetryDelay <= 0 {
		deps.RetryDelay = approvalAggregatorDefaultRetry
	}
	return &approvalAggregator{
		deps:          deps,
		byHost:        make(map[string]remoteApprovals),
		subscriptions: make(map[string]*approvalAggregationSubscription),
	}
}

// Reconcile 重新读取归属机集合，补上新增订阅并停止已移除的订阅。
func (g *approvalAggregator) Reconcile(ctx context.Context) {
	desired := make(map[string]struct{})
	for _, hostID := range g.deps.HomeHosts() {
		if hostID != "" {
			desired[hostID] = struct{}{}
		}
	}

	var removed []string
	var added []struct {
		hostID string
		sub    *approvalAggregationSubscription
		ctx    context.Context
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	for hostID, sub := range g.subscriptions {
		if _, ok := desired[hostID]; ok {
			continue
		}
		sub.cancel()
		delete(g.subscriptions, hostID)
		delete(g.byHost, hostID)
		removed = append(removed, hostID)
	}
	for hostID := range desired {
		if _, ok := g.subscriptions[hostID]; ok {
			continue
		}
		subCtx, cancel := context.WithCancel(ctx)
		sub := &approvalAggregationSubscription{cancel: cancel}
		g.subscriptions[hostID] = sub
		added = append(added, struct {
			hostID string
			sub    *approvalAggregationSubscription
			ctx    context.Context
		}{hostID: hostID, sub: sub, ctx: subCtx})
	}
	g.mu.Unlock()

	for _, hostID := range removed {
		log.Printf("[SuperDev] approval aggregator: 移除归属机订阅 host=%s", hostID)
		g.notifyChange()
	}
	for _, item := range added {
		g.wg.Add(1)
		go g.subscribe(item.ctx, item.hostID, item.sub)
	}
}

// All 返回各来源的并发安全快照副本，调用方不能通过返回值修改聚合器状态。
func (g *approvalAggregator) All() map[string]remoteApprovals {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make(map[string]remoteApprovals, len(g.byHost))
	for hostID, remote := range g.byHost {
		out[hostID] = cloneRemoteApprovals(remote)
	}
	return out
}

// ApplyForTest 直接注入各来源的快照，供上层测试绕过真实订阅。
// 与 noderegistry.Registry.ApplyForTest 同一惯例：生产代码不调用，且仍走与
// 真实路径相同的存储结构，不引入测试专用状态分支。
func (g *approvalAggregator) ApplyForTest(byHost map[string]remoteApprovals) {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.byHost = make(map[string]remoteApprovals, len(byHost))
	for hostID, remote := range byHost {
		g.byHost[hostID] = cloneRemoteApprovals(remote)
	}
	g.mu.Unlock()
	g.notifyChange()
}

// Close 停止所有上游订阅，并等待订阅 goroutine 退出，避免关闭后仍写入状态。
func (g *approvalAggregator) Close() {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	for _, sub := range g.subscriptions {
		sub.cancel()
	}
	g.mu.Unlock()
	g.wg.Wait()
}

func (g *approvalAggregator) subscribe(ctx context.Context, hostID string, sub *approvalAggregationSubscription) {
	defer g.wg.Done()
	for {
		if ctx.Err() != nil {
			return
		}

		stream, err := g.deps.Stream(ctx, hostID, nodetransport.NodeRequest{
			Method: http.MethodGet,
			Path:   "/ws/operation-approvals",
		})
		if err == nil && stream == nil {
			err = errors.New("approval stream is nil")
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			g.markUnreachable(hostID, sub, err)
			if !g.waitRetry(ctx) {
				return
			}
			continue
		}

		log.Printf("[SuperDev] approval aggregator: 建立归属机审批订阅 host=%s", hostID)
		readErr := g.readFrames(ctx, hostID, sub, stream)
		_ = stream.Close()
		if ctx.Err() != nil {
			return
		}
		if readErr != nil {
			g.markUnreachable(hostID, sub, readErr)
		}
		if !g.waitRetry(ctx) {
			return
		}
	}
}

func (g *approvalAggregator) readFrames(ctx context.Context, hostID string, sub *approvalAggregationSubscription, stream nodetransport.NodeStream) error {
	for {
		var snapshot approvalsSnapshot
		if err := stream.ReadJSON(&snapshot); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		g.markReachable(hostID, sub, snapshot)
	}
}

func (g *approvalAggregator) markReachable(hostID string, sub *approvalAggregationSubscription, snapshot approvalsSnapshot) {
	g.mu.Lock()
	active := !g.closed && g.subscriptions[hostID] == sub
	if active {
		g.byHost[hostID] = remoteApprovals{
			Snapshot:  cloneSnapshot(snapshot),
			Reachable: true,
			UpdatedAt: time.Now(),
		}
	}
	g.mu.Unlock()
	if active {
		g.notifyChange()
	}
}

func (g *approvalAggregator) markUnreachable(hostID string, sub *approvalAggregationSubscription, err error) {
	reason := err.Error()
	g.mu.Lock()
	active := !g.closed && g.subscriptions[hostID] == sub
	if active {
		remote := g.byHost[hostID]
		remote.Reachable = false
		remote.Err = reason
		remote.UpdatedAt = time.Now()
		g.byHost[hostID] = remote
	}
	g.mu.Unlock()
	if active {
		log.Printf("[SuperDev] approval aggregator: 上游断开 host=%s err=%s；保留末次已知快照，标记为不可达", hostID, reason)
		g.notifyChange()
	}
}

func (g *approvalAggregator) waitRetry(ctx context.Context) bool {
	timer := time.NewTimer(g.deps.RetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (g *approvalAggregator) notifyChange() {
	if g.deps.OnChange != nil {
		g.deps.OnChange()
	}
}

func cloneRemoteApprovals(remote remoteApprovals) remoteApprovals {
	remote.Snapshot = cloneSnapshot(remote.Snapshot)
	return remote
}

func cloneSnapshot(snapshot approvalsSnapshot) approvalsSnapshot {
	snapshot.Pending = cloneApprovals(snapshot.Pending)
	snapshot.Decided = cloneApprovals(snapshot.Decided)
	return snapshot
}

func cloneApprovals(approvals []operation.Approval) []operation.Approval {
	if approvals == nil {
		return nil
	}
	out := make([]operation.Approval, len(approvals))
	for i, approval := range approvals {
		out[i] = approval
		out[i].Plan.Reasons = append([]string(nil), approval.Plan.Reasons...)
		out[i].Plan.ExpectedEffects = append([]string(nil), approval.Plan.ExpectedEffects...)
		out[i].Plan.Checks = append([]operation.Check(nil), approval.Plan.Checks...)
	}
	return out
}
