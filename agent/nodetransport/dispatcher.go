// dispatcher.go 按 Host.Agent.Transport.Chain 分派节点通信请求。
//
// 职责：
//   - 根据 host 的 Agent transport chain 选择具体 NodeTransport provider
//   - 在链头不可达时串行探测并降级到后续 transport
//   - 聚合多个 provider 的节点状态订阅
//   - 在缺 Agent 或缺 provider 时返回结构化 NodeError
//
// 边界：
//   - 不建立具体连接，连接生命周期由 provider 负责
//   - 不修改 Host 或 Agent 配置
//   - 不持久化节点状态
package nodetransport

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	defaultRecoveryInterval = 7 * time.Minute
)

type hostRoute struct {
	selectedIndex int
	probedAt      time.Time
	lastResults   []ProbeResult
}

// Dispatcher 根据 Host.Agent.Transport.Chain 路由请求。
type Dispatcher struct {
	hosts            HostSource
	providers        map[model.TransportType]NodeTransport
	mu               sync.Mutex
	routes           map[string]hostRoute
	routeChanged     chan struct{}
	probeTimeout     time.Duration
	recoveryInterval time.Duration
}

// NewDispatcher 创建 dispatcher，并拷贝 providers 避免调用方后续修改映射。
func NewDispatcher(hosts HostSource, providers map[model.TransportType]NodeTransport) *Dispatcher {
	copied := map[model.TransportType]NodeTransport{}
	for typ, provider := range providers {
		if provider == nil {
			continue
		}
		copied[typ] = provider
	}
	return &Dispatcher{
		hosts:            hosts,
		providers:        copied,
		routes:           map[string]hostRoute{},
		routeChanged:     make(chan struct{}, 1),
		probeTimeout:     defaultProbeTimeout,
		recoveryInterval: defaultRecoveryInterval,
	}
}

// SetProbeTimeoutForTest 设置短探测超时，仅供测试压缩等待时间。
func (d *Dispatcher) SetProbeTimeoutForTest(timeout time.Duration) {
	d.probeTimeout = timeout
}

// SetRecoveryIntervalForTest 设置链头恢复探测周期，仅供测试使用。
func (d *Dispatcher) SetRecoveryIntervalForTest(interval time.Duration) {
	d.recoveryInterval = interval
}

// RouteSnapshotForTest 返回指定 host 的当前选路快照，仅供测试断言。
func (d *Dispatcher) RouteSnapshotForTest(hostID string) (RouteStatus, bool) {
	return d.routeSnapshot(hostID)
}

// RecoverChainHeadsForTest 立即执行一次链头恢复探测，仅供测试使用。
func (d *Dispatcher) RecoverChainHeadsForTest(ctx context.Context) {
	d.recoverChainHeads(ctx)
}

// Do 按 host transport chain 分派 HTTP 请求。
func (d *Dispatcher) Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error) {
	host, err := d.requireHost(hostID, "http")
	if err != nil {
		return NodeResponse{}, err
	}
	idx, provider, err := d.selectedProvider(host, "http")
	if err != nil {
		return NodeResponse{}, err
	}
	resp, err := provider.Do(ctx, hostID, req)
	if err == nil {
		return resp, nil
	}
	if !isTransportFailure(err) {
		return NodeResponse{}, err
	}
	if probeErr := d.reselect(ctx, host); probeErr != nil {
		return NodeResponse{}, err
	}
	newIdx, newProvider, selectErr := d.selectedProvider(host, "http")
	if selectErr != nil || newIdx == idx {
		return NodeResponse{}, err
	}
	return newProvider.Do(ctx, hostID, req)
}

// Stream 按 host transport chain 分派 WebSocket 请求。
func (d *Dispatcher) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	host, err := d.requireHost(hostID, "stream")
	if err != nil {
		return nil, err
	}
	idx, provider, err := d.selectedProvider(host, "stream")
	if err != nil {
		return nil, err
	}
	stream, err := provider.Stream(ctx, hostID, req)
	if err == nil {
		return stream, nil
	}
	if !isTransportFailure(err) {
		return nil, err
	}
	if probeErr := d.reselect(ctx, host); probeErr != nil {
		return nil, err
	}
	newIdx, newProvider, selectErr := d.selectedProvider(host, "stream")
	if selectErr != nil || newIdx == idx {
		return nil, err
	}
	return newProvider.Stream(ctx, hostID, req)
}

// SubscribeNodes 聚合所有 provider 的节点状态流。
func (d *Dispatcher) SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 32)
	go d.runSubscriptions(runCtx, out)
	return out, cancel
}

// Covers 返回配置了 transport chain 的 hostID 列表。
func (d *Dispatcher) Covers() []string {
	hosts, err := d.hosts()
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, host := range hosts {
		if host.ID == "" || host.Agent == nil || len(host.Agent.Transport.Chain) == 0 {
			continue
		}
		out = append(out, host.ID)
	}
	sort.Strings(out)
	return out
}

func (d *Dispatcher) runSubscriptions(ctx context.Context, out chan<- []NodeStatus) {
	recoveryCtx, recoveryCancel := context.WithCancel(ctx)
	defer recoveryCancel()
	go d.recoveryLoop(recoveryCtx)

	interval := 5 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	type watcher struct {
		index  int
		cancel context.CancelFunc
	}
	type doneEvent struct {
		hostID string
		index  int
	}
	var wg sync.WaitGroup
	done := make(chan doneEvent, 128)
	watchers := map[string]watcher{}

	defer func() {
		for _, w := range watchers {
			w.cancel()
		}
		wg.Wait()
		close(out)
	}()

	startWatcher := func(host model.Host, idx int, sub HostNodeSubscriber) {
		ch, stop := sub.SubscribeHostNodes(ctx, host)
		watchers[host.ID] = watcher{index: idx, cancel: stop}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				stop()
				select {
				case done <- doneEvent{hostID: host.ID, index: idx}:
				default:
				}
			}()
			recoveredOnThisWatcher := false
			for {
				select {
				case <-ctx.Done():
					return
				case batch, ok := <-ch:
					if !ok {
						return
					}
					if !recoveredOnThisWatcher && d.recoverChainHeadOnStatus(ctx, host, batch) {
						recoveredOnThisWatcher = true
					}
					if routeIdx, ok := d.routeIndex(host.ID); ok && routeIdx != idx {
						continue
					}
					select {
					case out <- d.attachRoute(batch):
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}

	reconcile := func() {
		if d.hosts == nil {
			return
		}
		hosts, err := d.hosts()
		if err != nil {
			return
		}
		seen := map[string]struct{}{}
		for _, host := range hosts {
			if host.ID == "" || host.Agent == nil || len(host.Agent.Transport.Chain) == 0 {
				continue
			}
			seen[host.ID] = struct{}{}
			idx := d.selectedIndex(host)
			entry := host.Agent.Transport.Chain[idx]
			provider := d.providers[entry.Type]
			sub, ok := provider.(HostNodeSubscriber)
			if !ok {
				if w, running := watchers[host.ID]; running {
					w.cancel()
					delete(watchers, host.ID)
				}
				continue
			}
			if w, running := watchers[host.ID]; running {
				if w.index == idx {
					continue
				}
				w.cancel()
				delete(watchers, host.ID)
			}
			startWatcher(host, idx, sub)
		}
		for hostID, w := range watchers {
			if _, ok := seen[hostID]; ok {
				continue
			}
			w.cancel()
			delete(watchers, hostID)
		}
	}

	reconcile()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-done:
			if w, ok := watchers[ev.hostID]; ok && w.index == ev.index {
				delete(watchers, ev.hostID)
			}
		case <-ticker.C:
			reconcile()
		case <-d.routeChanged:
			reconcile()
		}
	}
}

func (d *Dispatcher) recoverChainHeadOnStatus(ctx context.Context, host model.Host, batch []NodeStatus) bool {
	if !batchHasReachableHost(host.ID, batch) {
		return false
	}
	d.mu.Lock()
	route := d.routes[host.ID]
	d.mu.Unlock()
	if route.selectedIndex <= 0 {
		return false
	}
	return d.recoverChainHead(ctx, host)
}

func batchHasReachableHost(hostID string, batch []NodeStatus) bool {
	for _, status := range batch {
		if status.HostID == hostID && status.Reachable {
			return true
		}
	}
	return false
}

func (d *Dispatcher) sortedProviders() []NodeTransport {
	types := make([]string, 0, len(d.providers))
	byType := map[string]NodeTransport{}
	for typ, provider := range d.providers {
		key := string(typ)
		types = append(types, key)
		byType[key] = provider
	}
	sort.Strings(types)
	out := make([]NodeTransport, 0, len(types))
	for _, typ := range types {
		out = append(out, byType[typ])
	}
	return out
}

func (d *Dispatcher) requireHost(hostID, operation string) (model.Host, error) {
	host, found, err := d.hostByID(hostID)
	if err != nil {
		return model.Host{}, err
	}
	if !found {
		return model.Host{}, agentNotConfigured(hostID, operation)
	}
	return host, nil
}

func (d *Dispatcher) selectedProvider(host model.Host, operation string) (int, NodeTransport, error) {
	if host.Agent == nil || len(host.Agent.Transport.Chain) == 0 {
		return -1, nil, agentNotConfigured(host.ID, operation)
	}
	idx := d.selectedIndex(host)
	entry := host.Agent.Transport.Chain[idx]
	if entry.Type == "" {
		return -1, nil, agentNotConfigured(host.ID, operation)
	}
	provider := d.providers[entry.Type]
	if provider == nil {
		return -1, nil, unsupportedTransport(host.ID, entry.Type, operation)
	}
	return idx, provider, nil
}

func (d *Dispatcher) selectedIndex(host model.Host) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	route := d.routes[host.ID]
	if route.selectedIndex < 0 || route.selectedIndex >= len(host.Agent.Transport.Chain) {
		route.selectedIndex = 0
	}
	d.routes[host.ID] = route
	return route.selectedIndex
}

func (d *Dispatcher) routeSnapshot(hostID string) (RouteStatus, bool) {
	d.mu.Lock()
	route, ok := d.routes[hostID]
	d.mu.Unlock()
	if !ok {
		return RouteStatus{}, false
	}
	host, found, _ := d.hostByID(hostID)
	if !found || host.Agent == nil {
		return RouteStatus{}, false
	}
	status := RouteStatus{
		SelectedIndex: route.selectedIndex,
		Degraded:      route.selectedIndex > 0,
		LastResults:   append([]ProbeResult(nil), route.lastResults...),
	}
	if route.selectedIndex < 0 || route.selectedIndex >= len(host.Agent.Transport.Chain) {
		return status, true
	}
	entry := host.Agent.Transport.Chain[route.selectedIndex]
	status.SelectedType = entry.Type
	return status, true
}

func (d *Dispatcher) routeIndex(hostID string) (int, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	route, ok := d.routes[hostID]
	if !ok {
		return 0, false
	}
	return route.selectedIndex, true
}

func (d *Dispatcher) attachRoute(batch []NodeStatus) []NodeStatus {
	if len(batch) == 0 {
		return batch
	}
	out := make([]NodeStatus, len(batch))
	copy(out, batch)
	for i := range out {
		if route, ok := d.routeSnapshot(out[i].HostID); ok {
			route := route
			out[i].Route = &route
		}
	}
	return out
}

func (d *Dispatcher) setRoute(hostID string, next hostRoute) {
	d.mu.Lock()
	prev := d.routes[hostID]
	d.routes[hostID] = next
	changed := prev.selectedIndex != next.selectedIndex
	d.mu.Unlock()
	if changed {
		d.notifyRouteChanged()
	}
}

func (d *Dispatcher) notifyRouteChanged() {
	select {
	case d.routeChanged <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) reselect(ctx context.Context, host model.Host) error {
	results := []ProbeResult{}
	for idx, entry := range host.Agent.Transport.Chain {
		result := d.probeEntry(ctx, host, idx, entry)
		results = append(results, result)
		if result.Status == ProbeStatusReachable {
			d.setRoute(host.ID, hostRoute{selectedIndex: idx, probedAt: time.Now().UTC(), lastResults: results})
			return nil
		}
	}
	d.setRoute(host.ID, hostRoute{selectedIndex: -1, probedAt: time.Now().UTC(), lastResults: results})
	return ErrHostUnreachable
}

func (d *Dispatcher) probeEntry(ctx context.Context, host model.Host, idx int, entry model.TransportEntry) ProbeResult {
	provider := d.providers[entry.Type]
	return ProbeEntry(ctx, provider, host, idx, entry, d.probeTimeout)
}

func (d *Dispatcher) recoveryLoop(ctx context.Context) {
	interval := d.recoveryInterval
	if interval == 0 {
		interval = defaultRecoveryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.recoverChainHeads(ctx)
		}
	}
}

func (d *Dispatcher) recoverChainHeads(ctx context.Context) {
	if d.hosts == nil {
		return
	}
	hosts, err := d.hosts()
	if err != nil {
		return
	}
	for _, host := range hosts {
		if host.Agent == nil || len(host.Agent.Transport.Chain) < 2 {
			continue
		}
		d.recoverChainHead(ctx, host)
	}
}

func (d *Dispatcher) recoverChainHead(ctx context.Context, host model.Host) bool {
	if host.Agent == nil || len(host.Agent.Transport.Chain) < 2 {
		return false
	}
	d.mu.Lock()
	route := d.routes[host.ID]
	d.mu.Unlock()
	if route.selectedIndex <= 0 {
		return false
	}
	result := d.probeEntry(ctx, host, 0, host.Agent.Transport.Chain[0])
	if result.Status != ProbeStatusReachable {
		return false
	}
	route.selectedIndex = 0
	route.probedAt = time.Now().UTC()
	route.lastResults = []ProbeResult{result}
	d.setRoute(host.ID, route)
	return true
}

func isTransportFailure(err error) bool {
	if err == nil {
		return false
	}
	code := ErrorCode(err)
	return code == "" ||
		code == CodeTransportUnreachable ||
		code == CodeRequestTimeout ||
		code == CodeAgentUnreachable
}

func agentNotConfigured(hostID, operation string) error {
	return &NodeError{
		Code:      CodeAgentNotConfigured,
		HostID:    hostID,
		Operation: operation,
		Message:   fmt.Sprintf("agent not configured for host %s", hostID),
		Cause:     ErrHostUnreachable,
	}
}

func unsupportedTransport(hostID string, typ model.TransportType, operation string) error {
	return &NodeError{
		Code:          CodeUnsupportedTransport,
		HostID:        hostID,
		TransportType: typ,
		Operation:     operation,
		Message:       fmt.Sprintf("transport %s is not supported for host %s", typ, hostID),
		Cause:         ErrHostUnreachable,
	}
}

func (d *Dispatcher) hostByID(hostID string) (model.Host, bool, error) {
	if d.hosts == nil {
		return model.Host{}, false, nil
	}
	hosts, err := d.hosts()
	if err != nil {
		return model.Host{}, false, &NodeError{
			Code:      CodeTransportUnreachable,
			HostID:    hostID,
			Operation: "resolve",
			Message:   err.Error(),
			Cause:     err,
		}
	}
	for _, host := range hosts {
		if host.ID == hostID {
			return host, true, nil
		}
	}
	return model.Host{}, false, nil
}
