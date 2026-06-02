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
// AllEndpointsOK 为 true 表示所有必需 endpoint 均返回 200。
type ProbeResult struct {
	AllEndpointsOK bool
}

// Prober 抽象“对某个 host 的远端 agent 探活一次”。生产实现见 api 层（通过隧道 baseURL 请求必需 endpoint）。
type Prober interface {
	Probe(ctx context.Context, hostID string) (ProbeResult, error)
}

// Monitor 维护各 host 的 agent 健康状态。
type Monitor struct {
	prober Prober
	mu     sync.Mutex
	status map[string]Status
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
		prober: prober,
		status: map[string]Status{},
	}
}

// Status 返回指定 host 的 agent 健康状态；从未探过返回 StatusUnknown。
func (m *Monitor) Status(hostID string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.status[hostID]; ok {
		return s
	}
	return StatusUnknown
}

// ProbeOnce 对 host 探活一次并更新其健康状态。
//
// 参数：
//   - ctx: 上下文，用于取消探活
//   - hostID: 目标 host
//
// 注意：
//   - 探活报错映射为 StatusUnreachable
//   - 接口不全映射为 StatusVersionMismatch
//   - 该方法只改 agent 状态，不触碰隧道
func (m *Monitor) ProbeOnce(ctx context.Context, hostID string) {
	next := m.classify(ctx, hostID)
	m.mu.Lock()
	m.status[hostID] = next
	m.mu.Unlock()
}

// classify 执行一次探活并把结果归类为健康状态。
func (m *Monitor) classify(ctx context.Context, hostID string) Status {
	res, err := m.prober.Probe(ctx, hostID)
	if err != nil {
		return StatusUnreachable
	}
	if res.AllEndpointsOK {
		return StatusHealthy
	}
	return StatusVersionMismatch
}
