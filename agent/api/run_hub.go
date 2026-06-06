// Package api 中的 run_hub.go 提供 pipeline run 实时事件总线。
//
// 职责：
//   - 按 runID 维护 WebSocket 订阅者
//   - 将 pipeline engine 事件广播给实时控制台
//   - 在慢消费者阻塞时关闭该订阅，避免拖慢流水线执行
//
// 边界：
//   - 不持久化事件，日志持久化仍由 store 负责
//   - 不解析 pipeline 业务语义，仅转发已转换好的 RunEvent
package api

import (
	"sync"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	RunEventKindLog    = "log"
	RunEventKindStatus = "status"
	RunEventKindDone   = "done"
)

// RunStatusPatch 描述 run 控制台需要实时更新的一段状态。
type RunStatusPatch struct {
	StepName string          `json:"step_name,omitempty"`
	HostID   string          `json:"host_id,omitempty"`
	Status   model.RunStatus `json:"status"`
	ExitCode int             `json:"exit_code,omitempty"`
	At       int64           `json:"at,omitempty"`
}

// RunEvent 是 /ws/runs/{runId}/logs 的统一事件信封。
type RunEvent struct {
	Kind   string            `json:"kind"`
	Log    *model.RunLogLine `json:"log,omitempty"`
	Status *RunStatusPatch   `json:"status,omitempty"`
	Run    *model.Run        `json:"run,omitempty"`
}

type runSubscriber struct {
	ch     chan RunEvent
	mu     sync.Mutex
	closed bool
}

// Ch 返回订阅事件通道。
func (s *runSubscriber) Ch() <-chan RunEvent {
	return s.ch
}

func (s *runSubscriber) send(ev RunEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	select {
	case s.ch <- ev:
		return true
	default:
		close(s.ch)
		s.closed = true
		return false
	}
}

func (s *runSubscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	close(s.ch)
	s.closed = true
}

// RunHub 按 runID 管理内存订阅者。
type RunHub struct {
	mu     sync.RWMutex
	buffer int
	subs   map[string]map[*runSubscriber]struct{}
}

// NewRunHub 创建默认缓冲大小的 RunHub。
func NewRunHub() *RunHub {
	return newRunHubWithBuffer(256)
}

func newRunHubWithBuffer(buffer int) *RunHub {
	if buffer <= 0 {
		buffer = 1
	}
	return &RunHub{
		buffer: buffer,
		subs:   map[string]map[*runSubscriber]struct{}{},
	}
}

// Subscribe 订阅指定 runID 的事件，并返回取消函数。
func (h *RunHub) Subscribe(runID string) (*runSubscriber, func()) {
	sub := &runSubscriber{ch: make(chan RunEvent, h.buffer)}
	h.mu.Lock()
	if h.subs[runID] == nil {
		h.subs[runID] = map[*runSubscriber]struct{}{}
	}
	h.subs[runID][sub] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.remove(runID, sub)
		sub.close()
	}
	return sub, cancel
}

// Broadcast 向指定 runID 的所有订阅者发送事件。
func (h *RunHub) Broadcast(runID string, ev RunEvent) {
	h.mu.RLock()
	subs := make([]*runSubscriber, 0, len(h.subs[runID]))
	for sub := range h.subs[runID] {
		subs = append(subs, sub)
	}
	h.mu.RUnlock()

	for _, sub := range subs {
		if sub.send(ev) {
			continue
		}
		h.remove(runID, sub)
	}
}

// Close 关闭指定 runID 的所有订阅者。
func (h *RunHub) Close(runID string) {
	h.mu.Lock()
	subs := h.subs[runID]
	delete(h.subs, runID)
	h.mu.Unlock()

	for sub := range subs {
		sub.close()
	}
}

func (h *RunHub) remove(runID string, sub *runSubscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[runID] == nil {
		return
	}
	delete(h.subs[runID], sub)
	if len(h.subs[runID]) == 0 {
		delete(h.subs, runID)
	}
}
