// node_status_publisher.go 封装远端 agent 节点状态的事件驱动推送。
//
// 职责：
//   - 为 /ws/node-status 生成订阅式节点快照流
//   - 合并 managed 状态变化信号，避免高频状态变更阻塞写端
//   - 保留 heartbeat，保证长连接在无事件时仍周期刷新
//
// 边界：
//   - 不持久化节点状态
//   - 不直接写 WebSocket，网络写入由 handler 完成
//   - 不参与桌面端 NodeRegistry 聚合逻辑
package api

import (
	"context"
	"time"

	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type nodeStatusPublisher struct {
	app       *App
	hostID    string
	hostName  string
	heartbeat time.Duration
	signal    chan struct{}
}

func newNodeStatusPublisher(app *App, hostID, hostName string, heartbeat time.Duration) *nodeStatusPublisher {
	return &nodeStatusPublisher{
		app:       app,
		hostID:    hostID,
		hostName:  hostName,
		heartbeat: heartbeat,
		signal:    make(chan struct{}, 1),
	}
}

func (a *App) registerNodeStatusPublisher(pub *nodeStatusPublisher) func() {
	a.nodeStatusPublisherMu.Lock()
	if a.nodeStatusPublishers == nil {
		a.nodeStatusPublishers = map[*nodeStatusPublisher]struct{}{}
	}
	a.nodeStatusPublishers[pub] = struct{}{}
	a.nodeStatusPublisherMu.Unlock()

	return func() {
		a.nodeStatusPublisherMu.Lock()
		delete(a.nodeStatusPublishers, pub)
		a.nodeStatusPublisherMu.Unlock()
	}
}

func (a *App) signalNodeStatusPublishers() {
	a.nodeStatusPublisherMu.Lock()
	publishers := make([]*nodeStatusPublisher, 0, len(a.nodeStatusPublishers))
	for pub := range a.nodeStatusPublishers {
		publishers = append(publishers, pub)
	}
	a.nodeStatusPublisherMu.Unlock()

	for _, pub := range publishers {
		pub.Signal()
	}
}

func (p *nodeStatusPublisher) Signal() {
	select {
	case p.signal <- struct{}{}:
	default:
	}
}

func (p *nodeStatusPublisher) Subscribe(ctx context.Context) <-chan []nodetransport.NodeStatus {
	ch := make(chan []nodetransport.NodeStatus, 16)
	go func() {
		defer close(ch)

		var ticker *time.Ticker
		var ticks <-chan time.Time
		if p.heartbeat > 0 {
			ticker = time.NewTicker(p.heartbeat)
			ticks = ticker.C
			defer ticker.Stop()
		}

		if !p.send(ctx, ch) {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-p.signal:
				if !p.send(ctx, ch) {
					return
				}
			case <-ticks:
				if !p.send(ctx, ch) {
					return
				}
			}
		}
	}()
	return ch
}

func (p *nodeStatusPublisher) send(ctx context.Context, ch chan<- []nodetransport.NodeStatus) bool {
	batch := []nodetransport.NodeStatus{p.app.nodeStatusSnapshot(ctx, p.hostID, p.hostName)}
	select {
	case ch <- batch:
		return true
	case <-ctx.Done():
		return false
	}
}
