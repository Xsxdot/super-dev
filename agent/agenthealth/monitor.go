// Package agenthealth 监控远端 agent 的健康状态。
//
// 职责：
//   - 维护 hostID → agent 健康状态（四态）
//   - 通过注入的 Prober 探活，把探活结果映射为健康状态
//   - （Task 2 起）订阅隧道事件，按 host 启停轮询循环
//   - 对外提供状态查询与变化事件订阅
//
// 边界：
//   - 不建立隧道、不管理传输层；只消费隧道的“已连接/已断开”信号
//   - 探活失败只反映在 agent 状态上，绝不反向影响隧道
//   - 不直接发 HTTP 请求，探活细节由注入的 Prober 实现（生产实现在 api 层）
package agenthealth

import (
	"context"
	"sync"
	"time"
)

// Status 是远端 agent 的健康状态。
type Status string

const (
	// StatusUnknown 表示隧道刚连上、尚未探过，初始态。
	StatusUnknown Status = "unknown"
	// StatusHealthy 表示探活通且所需接口齐全。
	StatusHealthy Status = "healthy"
	// StatusUnreachable 表示探不到（agent 进程挂了/未安装）。
	StatusUnreachable Status = "unreachable"
	// StatusVersionMismatch 表示探得到但接口不全/版本旧。
	StatusVersionMismatch Status = "version-mismatch"
)

// ProbeResult 是一次探活的结果。
// AllEndpointsOK 为 true 表示所有必需 endpoint 均返回可接受状态。
type ProbeResult struct {
	AllEndpointsOK bool
	Version        string
}

// Prober 抽象“对某个 host 的远端 agent 探活一次”。生产实现见 api 层（通过隧道 baseURL 请求必需 endpoint）。
type Prober interface {
	Probe(ctx context.Context, hostID string) (ProbeResult, error)
}

// TunnelSignal 是 Monitor 消费的隧道连接信号。
// Connected=true 表示隧道已连上（开始轮询）；false 表示断开（停止轮询）。
type TunnelSignal struct {
	HostID    string
	Connected bool
}

// Event 表示一次 agent 健康状态变化。
type Event struct {
	HostID    string `json:"host_id"`
	Status    Status `json:"agent"`
	Version   string `json:"agent_version,omitempty"`
	CheckedAt string `json:"agent_checked_at,omitempty"`
}

// Info 是指定 host 最近一次 agent 探活的可展示元信息。
type Info struct {
	Status    Status
	Version   string
	CheckedAt time.Time
}

const defaultPollInterval = 10 * time.Second

// Monitor 维护各 host 的 agent 健康状态，并按隧道信号启停轮询。
type Monitor struct {
	prober   Prober
	interval time.Duration

	mu       sync.Mutex
	status   map[string]Info
	cancels  map[string]context.CancelFunc // hostID → 停止该 host 轮询
	pollIDs  map[string]uint64
	nextPoll uint64
	subs     map[string]chan Event
}

// NewMonitor 创建 Monitor。prober 不可为 nil。
//
// 参数：
//   - prober: 探活能力实现
//
// 返回：
//   - 已初始化的 *Monitor（尚未订阅任何隧道事件）
func NewMonitor(prober Prober) *Monitor {
	return &Monitor{
		prober:   prober,
		interval: defaultPollInterval,
		status:   map[string]Info{},
		cancels:  map[string]context.CancelFunc{},
		pollIDs:  map[string]uint64{},
		subs:     map[string]chan Event{},
	}
}

// SetPollInterval 设置轮询间隔（主要供测试调快）。须在 Run 启动相应 host 轮询前调用。
func (m *Monitor) SetPollInterval(d time.Duration) {
	m.mu.Lock()
	m.interval = d
	m.mu.Unlock()
}

// Status 返回指定 host 的 agent 健康状态；从未探过返回 StatusUnknown。
func (m *Monitor) Status(hostID string) Status {
	return m.Info(hostID).Status
}

// Info 返回指定 host 最近一次 agent 探活元信息；从未探过返回 unknown 空信息。
func (m *Monitor) Info(hostID string) Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.status[hostID]; ok {
		return s
	}
	return Info{Status: StatusUnknown}
}

// ProbeOnce 对 host 探活一次，更新其健康状态，并在状态元信息变化时广播事件。
//
// 参数：
//   - ctx: 上下文，用于取消探活
//   - hostID: 目标 host
//
// 返回：
//   - 本次探活后的可展示元信息
//
// 注意：
//   - 探活报错映射为 StatusUnreachable
//   - 接口不全映射为 StatusVersionMismatch
//   - 该方法只改 agent 状态，不触碰隧道
func (m *Monitor) ProbeOnce(ctx context.Context, hostID string) Info {
	next := m.classify(ctx, hostID)
	m.mu.Lock()
	changed := m.status[hostID] != next
	m.status[hostID] = next
	m.mu.Unlock()
	if changed {
		m.emit(eventFromInfo(hostID, next))
	}
	return next
}

// Run 消费隧道信号循环，按 host 启停轮询，直到 ctx 取消。
//
// 参数：
//   - ctx: 取消时停止所有 host 轮询并退出
//   - signals: 隧道连接信号源（connected/disconnected）
//
// 注意：
//   - 每个 connected 的 host 启动独立轮询 goroutine
//   - disconnected 时停止该 host 轮询并把状态清回 unknown
func (m *Monitor) Run(ctx context.Context, signals <-chan TunnelSignal) {
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case sig, ok := <-signals:
			if !ok {
				m.stopAll()
				return
			}
			if sig.Connected {
				m.startPolling(ctx, sig.HostID)
			} else {
				m.stopPolling(sig.HostID)
			}
		}
	}
}

// Subscribe 注册状态订阅，返回事件 channel（缓冲 64）。
func (m *Monitor) Subscribe(id string) <-chan Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan Event, 64)
	m.subs[id] = ch
	return ch
}

// Unsubscribe 注销订阅。
func (m *Monitor) Unsubscribe(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.subs[id]; ok {
		delete(m.subs, id)
		close(ch)
	}
}

// classify 执行一次探活并把结果归类为健康状态与展示元信息。
func (m *Monitor) classify(ctx context.Context, hostID string) Info {
	now := time.Now().UTC()
	res, err := m.prober.Probe(ctx, hostID)
	if err != nil {
		return Info{Status: StatusUnreachable, CheckedAt: now}
	}
	if res.AllEndpointsOK {
		return Info{Status: StatusHealthy, Version: res.Version, CheckedAt: now}
	}
	return Info{Status: StatusVersionMismatch, Version: res.Version, CheckedAt: now}
}

// startPolling 为 host 启动轮询；已在轮询则忽略（幂等）。
func (m *Monitor) startPolling(parent context.Context, hostID string) {
	m.mu.Lock()
	if _, ok := m.cancels[hostID]; ok {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.nextPoll++
	pollID := m.nextPoll
	m.cancels[hostID] = cancel
	m.pollIDs[hostID] = pollID
	interval := m.interval
	m.mu.Unlock()

	go m.pollLoop(ctx, hostID, pollID, interval)
}

// pollLoop 立即探一次，之后按 interval 周期探活，直到 ctx 取消。
func (m *Monitor) pollLoop(ctx context.Context, hostID string, pollID uint64, interval time.Duration) {
	m.probeAndEmit(ctx, hostID, pollID)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.probeAndEmit(ctx, hostID, pollID)
		}
	}
}

// stopPolling 停止 host 轮询并把状态清回 unknown，广播变化。
func (m *Monitor) stopPolling(hostID string) {
	m.mu.Lock()
	cancel, ok := m.cancels[hostID]
	if ok {
		delete(m.cancels, hostID)
		delete(m.pollIDs, hostID)
	}
	current, seen := m.status[hostID]
	changed := seen && current.Status != StatusUnknown
	m.status[hostID] = Info{Status: StatusUnknown}
	m.mu.Unlock()
	if ok {
		cancel()
	}
	if changed {
		m.emit(eventFromInfo(hostID, Info{Status: StatusUnknown}))
	}
}

// stopAll 停止全部 host 轮询（Run 退出时调用）。
func (m *Monitor) stopAll() {
	m.mu.Lock()
	cancels := m.cancels
	m.cancels = map[string]context.CancelFunc{}
	m.pollIDs = map[string]uint64{}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

// probeAndEmit 探活一次，状态变化时广播事件。
func (m *Monitor) probeAndEmit(ctx context.Context, hostID string, pollID uint64) {
	next := m.classify(ctx, hostID)
	m.mu.Lock()
	if ctx.Err() != nil || m.pollIDs[hostID] != pollID {
		m.mu.Unlock()
		return
	}
	changed := m.status[hostID] != next
	m.status[hostID] = next
	m.mu.Unlock()
	if changed {
		m.emit(eventFromInfo(hostID, next))
	}
}

func eventFromInfo(hostID string, info Info) Event {
	checkedAt := ""
	if !info.CheckedAt.IsZero() {
		checkedAt = info.CheckedAt.Format(time.RFC3339)
	}
	return Event{
		HostID:    hostID,
		Status:    info.Status,
		Version:   info.Version,
		CheckedAt: checkedAt,
	}
}

// emit 向所有订阅者非阻塞广播（channel 满则丢弃）。
func (m *Monitor) emit(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.subs {
		// 持锁发送是为了和 Unsubscribe 的 close 互斥，避免向已关闭 channel 发送导致 panic。
		// 发送为非阻塞，channel 满时丢弃事件，不会拖慢轮询。
		select {
		case ch <- ev:
		default:
		}
	}
}
