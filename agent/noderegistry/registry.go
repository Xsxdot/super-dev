// Package noderegistry 持有本地 agent 看到的全局节点状态快照。
//
// 职责：
//   - 订阅一个或多个 NodeTransport 的节点状态流
//   - 将多 transport 上报合并为 hostID -> NodeStatus 的内存快照
//   - 向 HTTP/WebSocket 层提供快照和变更订阅
//
// 边界：
//   - 不持久化节点状态
//   - 不建立 SSH 隧道或选择传输
//   - 不采集 runtime 指标，指标来自远端 agent 上报
package noderegistry

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

const (
	defaultStaleAfter    = 15 * time.Second
	defaultCheckInterval = 5 * time.Second
)

// Options 控制 Registry 的心跳过期策略。
//
// 参数：
//   - StaleAfter: 节点多久没有新状态后被标记为 unreachable，0 表示使用默认值
//   - CheckInterval: 过期检查间隔，0 表示使用默认值
//
// 注意：
//   - 过期只改变内存快照，不会主动断开或重连 transport
type Options struct {
	StaleAfter    time.Duration
	CheckInterval time.Duration
}

// Registry 是本地内存节点状态中心。
//
// 职责：
//   - 合并所有 NodeTransport 的节点状态流
//   - 提供稳定排序的快照查询和全量快照订阅
//
// 注意：
//   - Registry 只保存进程内状态，重启后由 transport 重新上报恢复
type Registry struct {
	transports []nodetransport.NodeTransport
	staleAfter time.Duration
	checkEvery time.Duration

	mu       sync.Mutex
	nodes    map[string]nodetransport.NodeStatus
	lastSeen map[string]time.Time
	covers   map[int]map[string]struct{}
	subs     map[string]chan []nodetransport.NodeStatus
	started  bool
}

// New 创建 Registry。
//
// 参数：
//   - transports: 节点状态来源，每个 transport 通过 SubscribeNodes 上报
//   - opts: 过期检查配置，零值会使用默认策略
//
// 返回：
//   - 尚未启动的 Registry 实例
//
// 注意：
//   - 调用方需要再调用 Start(ctx) 才会开始消费状态流
func New(transports []nodetransport.NodeTransport, opts Options) *Registry {
	staleAfter := opts.StaleAfter
	if staleAfter == 0 {
		staleAfter = defaultStaleAfter
	}
	checkEvery := opts.CheckInterval
	if checkEvery == 0 {
		checkEvery = defaultCheckInterval
	}
	return &Registry{
		transports: append([]nodetransport.NodeTransport(nil), transports...),
		staleAfter: staleAfter,
		checkEvery: checkEvery,
		nodes:      map[string]nodetransport.NodeStatus{},
		lastSeen:   map[string]time.Time{},
		covers:     map[int]map[string]struct{}{},
		subs:       map[string]chan []nodetransport.NodeStatus{},
	}
}

// Start 启动 transport 状态流订阅。
//
// 参数：
//   - ctx: 控制 Registry 后台 goroutine 生命周期
//
// 注意：
//   - 重复调用是幂等的
//   - ctx 取消后，Registry 停止消费新状态，但保留最后一次内存快照
func (r *Registry) Start(ctx context.Context) {
	coverage := make(map[int]map[string]struct{}, len(r.transports))
	for idx, transport := range r.transports {
		coverage[idx] = idsToSet(transport.Covers())
	}
	now := time.Now().UTC()

	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	for idx, ids := range coverage {
		r.covers[idx] = ids
	}
	changed := r.preseedCoveredNodesLocked(now)
	if changed {
		r.broadcastLocked(sortedSnapshot(r.nodes))
	}
	r.mu.Unlock()

	for idx, transport := range r.transports {
		idx := idx
		transport := transport
		go r.consumeTransport(ctx, idx, transport)
	}
	go r.staleLoop(ctx)
}

// Snapshot 返回所有节点的当前快照，按 name/id 稳定排序。
//
// 返回：
//   - 当前所有已知节点的状态副本
//
// 注意：
//   - 调用方修改返回切片不会影响 Registry 内部状态
func (r *Registry) Snapshot() []nodetransport.NodeStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return sortedSnapshot(r.nodes)
}

// SnapshotOf 返回单节点快照。
//
// 参数：
//   - hostID: 节点 ID
//
// 返回：
//   - 节点状态
//   - 是否存在该节点快照
func (r *Registry) SnapshotOf(hostID string) (nodetransport.NodeStatus, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.nodes[hostID]
	return status, ok
}

// Subscribe 订阅全量快照变更，并立即收到一次当前快照。
//
// 返回：
//   - 快照 channel，每次变更发送完整节点列表
//   - unsubscribe 函数，用于关闭订阅
//
// 注意：
//   - 订阅者消费过慢时会跳过中间快照，只保留 Registry 不被慢消费者阻塞
func (r *Registry) Subscribe() (<-chan []nodetransport.NodeStatus, func()) {
	r.mu.Lock()
	id := uuid.NewString()
	ch := make(chan []nodetransport.NodeStatus, 16)
	r.subs[id] = ch
	initial := sortedSnapshot(r.nodes)
	r.mu.Unlock()

	ch <- initial
	return ch, func() {
		r.mu.Lock()
		if existing, ok := r.subs[id]; ok {
			delete(r.subs, id)
			close(existing)
		}
		r.mu.Unlock()
	}
}

// ApplyForTest 注入一批节点状态，供 API 测试绕过 transport 状态流。
//
// 参数：
//   - batch: 要写入 Registry 的完整或部分节点快照
//
// 注意：
//   - 生产代码不调用此方法；它仍走同一 applyBatch 路径，避免测试专用状态分支
func (r *Registry) ApplyForTest(batch []nodetransport.NodeStatus) {
	r.applyBatch(-1, batch, time.Now().UTC())
}

func (r *Registry) consumeTransport(ctx context.Context, idx int, transport nodetransport.NodeTransport) {
	ch, cancel := transport.SubscribeNodes(ctx)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-ch:
			if !ok {
				r.markSourceUnreachable(idx)
				return
			}
			r.applyBatch(idx, batch, time.Now().UTC())
		}
	}
}

func (r *Registry) applyBatch(source int, batch []nodetransport.NodeStatus, now time.Time) {
	if len(batch) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	changed := false
	for _, status := range batch {
		if status.HostID == "" {
			continue
		}
		if !r.sourceCoversLocked(source, status.HostID) {
			continue
		}
		if status.UpdatedAt.IsZero() {
			status.UpdatedAt = now
		}
		r.nodes[status.HostID] = status
		r.lastSeen[status.HostID] = now
		changed = true
	}
	if changed {
		r.broadcastLocked(sortedSnapshot(r.nodes))
	}
}

func (r *Registry) markSourceUnreachable(source int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	ids := r.covers[source]
	if len(ids) == 0 {
		return
	}
	now := time.Now().UTC()
	for hostID := range ids {
		r.nodes[hostID] = unreachableStatus(r.nodes[hostID], now, "node status stream closed")
	}
	r.broadcastLocked(sortedSnapshot(r.nodes))
}

func (r *Registry) preseedCoveredNodesLocked(now time.Time) bool {
	changed := false
	for _, ids := range r.covers {
		for hostID := range ids {
			if hostID == "" {
				continue
			}
			if _, exists := r.nodes[hostID]; exists {
				continue
			}
			r.nodes[hostID] = nodetransport.NodeStatus{
				HostID:    hostID,
				Reachable: false,
				Agent: model.AgentRuntime{
					Health:    model.AgentHealthUnknown,
					Reachable: false,
				},
				UpdatedAt: now,
			}
			changed = true
		}
	}
	return changed
}

func (r *Registry) sourceCoversLocked(source int, hostID string) bool {
	if source < 0 {
		return true
	}
	ids := r.covers[source]
	if len(ids) == 0 {
		return false
	}
	_, ok := ids[hostID]
	return ok
}

func idsToSet(ids []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func (r *Registry) staleLoop(ctx context.Context) {
	ticker := time.NewTicker(r.checkEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.markStale(time.Now().UTC())
		}
	}
}

func (r *Registry) markStale(now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	changed := false
	for hostID, seenAt := range r.lastSeen {
		if now.Sub(seenAt) <= r.staleAfter {
			continue
		}
		current := r.nodes[hostID]
		if !current.Reachable {
			continue
		}
		r.nodes[hostID] = unreachableStatus(current, now, "node status heartbeat timeout")
		changed = true
	}
	if changed {
		r.broadcastLocked(sortedSnapshot(r.nodes))
	}
}

func unreachableStatus(current nodetransport.NodeStatus, now time.Time, errText string) nodetransport.NodeStatus {
	current.Reachable = false
	current.Agent.Reachable = false
	current.Agent.Health = model.AgentHealthUnreachable
	current.UpdatedAt = now
	current.Error = errText
	return current
}

func sortedSnapshot(nodes map[string]nodetransport.NodeStatus) []nodetransport.NodeStatus {
	out := make([]nodetransport.NodeStatus, 0, len(nodes))
	for _, status := range nodes {
		out = append(out, status)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := out[i].Name
		if left == "" {
			left = out[i].HostID
		}
		right := out[j].Name
		if right == "" {
			right = out[j].HostID
		}
		if left != right {
			return left < right
		}
		return out[i].HostID < out[j].HostID
	})
	return out
}

func (r *Registry) broadcastLocked(snapshot []nodetransport.NodeStatus) {
	for _, ch := range r.subs {
		select {
		case ch <- snapshot:
		default:
		}
	}
}
