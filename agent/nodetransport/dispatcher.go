// dispatcher.go 按 Host.Agent.Transport.Type 分派节点通信请求。
//
// 职责：
//   - 根据 host 的 Agent transport 类型选择具体 NodeTransport provider
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

	"github.com/xsxdot/super-dev/agent/model"
)

// Dispatcher 根据 Host.Agent.Transport.Type 路由请求。
type Dispatcher struct {
	hosts     HostSource
	providers map[model.TransportType]NodeTransport
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
	return &Dispatcher{hosts: hosts, providers: copied}
}

// Do 按 host transport 类型分派 HTTP 请求。
func (d *Dispatcher) Do(ctx context.Context, hostID string, req NodeRequest) (NodeResponse, error) {
	provider, err := d.providerFor(hostID, "http")
	if err != nil {
		return NodeResponse{}, err
	}
	return provider.Do(ctx, hostID, req)
}

// Stream 按 host transport 类型分派 WebSocket 请求。
func (d *Dispatcher) Stream(ctx context.Context, hostID string, req NodeRequest) (NodeStream, error) {
	provider, err := d.providerFor(hostID, "stream")
	if err != nil {
		return nil, err
	}
	return provider.Stream(ctx, hostID, req)
}

// SubscribeNodes 聚合所有 provider 的节点状态流。
func (d *Dispatcher) SubscribeNodes(ctx context.Context) (<-chan []NodeStatus, func()) {
	runCtx, cancel := context.WithCancel(ctx)
	out := make(chan []NodeStatus, 32)
	go d.runSubscriptions(runCtx, out)
	return out, cancel
}

// Covers 返回所有 provider 覆盖 hostID 的去重并集。
func (d *Dispatcher) Covers() []string {
	seen := map[string]struct{}{}
	for _, provider := range d.sortedProviders() {
		for _, hostID := range provider.Covers() {
			if hostID == "" {
				continue
			}
			seen[hostID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for hostID := range seen {
		out = append(out, hostID)
	}
	sort.Strings(out)
	return out
}

func (d *Dispatcher) runSubscriptions(ctx context.Context, out chan<- []NodeStatus) {
	var wg sync.WaitGroup
	stops := []func(){}
	for _, provider := range d.sortedProviders() {
		ch, stop := provider.SubscribeNodes(ctx)
		stops = append(stops, stop)
		wg.Add(1)
		go func(ch <-chan []NodeStatus) {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case batch, ok := <-ch:
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
		}(ch)
	}
	<-ctx.Done()
	for _, stop := range stops {
		stop()
	}
	wg.Wait()
	close(out)
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

func (d *Dispatcher) providerFor(hostID, operation string) (NodeTransport, error) {
	host, found, err := d.hostByID(hostID)
	if err != nil {
		return nil, err
	}
	if !found || host.Agent == nil || len(host.Agent.Transport.Chain) == 0 || host.Agent.Transport.Chain[0].Type == "" {
		return nil, &NodeError{
			Code:      CodeAgentNotConfigured,
			HostID:    hostID,
			Operation: operation,
			Message:   fmt.Sprintf("agent not configured for host %s", hostID),
			Cause:     ErrHostUnreachable,
		}
	}
	typ := host.Agent.Transport.Chain[0].Type
	provider := d.providers[typ]
	if provider == nil {
		return nil, &NodeError{
			Code:          CodeUnsupportedTransport,
			HostID:        hostID,
			TransportType: typ,
			Operation:     operation,
			Message:       fmt.Sprintf("transport %s is not supported for host %s", typ, hostID),
			Cause:         ErrHostUnreachable,
		}
	}
	return provider, nil
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
