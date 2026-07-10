// pump.go 实现 DAP 事件泵，维护单个 runtime 的 debugger 快照。
//
// 职责：
//   - 订阅 DAP stopped/continued/terminated 事件
//   - stopped 时补一次 StackTrace，记录暂停源码位置和线程
//   - 把调试器运行态写入线程安全 store，供 runtime status 轮询读取
//
// 边界：
//   - 不向前端主动推送事件
//   - 不持有 manager 锁，不管理 runtime 生命周期——terminated/exited 只通过
//     onTerminated 回调通知外部（由 manager 决定如何反向失效 runtime）
//   - 不解释变量和 scopes，只取顶层 stack frame 位置
package codedebug

import (
	"context"
	"sync"
)

type debuggerSnapshotStore struct {
	mu   sync.Mutex
	snap DebuggerSnapshot
}

func (s *debuggerSnapshotStore) get() DebuggerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

func (s *debuggerSnapshotStore) set(snap DebuggerSnapshot) {
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
}

type pumpDAP interface {
	Subscribe() (<-chan map[string]any, func())
	StackTrace(context.Context, int) (map[string]any, error)
}

type eventPump struct {
	dap    pumpDAP
	store  *debuggerSnapshotStore
	stopFn func()
	done   chan struct{}
	// onTerminated 在收到 terminated/exited 事件时触发（在 pump loop goroutine 内）。
	// 回调方若要停 pump 或关闭资源必须异步执行——pump.stop 会等待 loop 退出，
	// 同步调用会死锁。必须在 start 之前设置。
	onTerminated func()
}

func newEventPump(dap pumpDAP, store *debuggerSnapshotStore) *eventPump {
	return &eventPump{dap: dap, store: store, done: make(chan struct{})}
}

func (p *eventPump) start(ctx context.Context) {
	p.store.set(DebuggerSnapshot{State: "attached"})
	sub, cancel := p.dap.Subscribe()
	p.stopFn = cancel
	go p.loop(ctx, sub)
}

func (p *eventPump) loop(ctx context.Context, sub <-chan map[string]any) {
	defer close(p.done)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			if !p.handle(ctx, event) {
				return
			}
		}
	}
}

func (p *eventPump) handle(ctx context.Context, event map[string]any) bool {
	switch event["event"] {
	case "stopped":
		threadID := 0
		if body, ok := event["body"].(map[string]any); ok {
			threadID = intFromAny(body["threadId"])
		}
		source, line := p.resolveLocation(ctx, threadID)
		p.store.set(DebuggerSnapshot{State: "paused", ThreadID: threadID, Source: source, Line: line})
	case "continued":
		p.store.set(DebuggerSnapshot{State: "attached"})
	case "terminated", "exited":
		p.store.set(DebuggerSnapshot{State: "attached"})
		// debuggee 已终止：通知外部反向失效 runtime，否则死 runtime 会被
		// 后续调试请求永久复用（Alive 只在显式 Stop/Close 时翻转是不够的）。
		if p.onTerminated != nil {
			p.onTerminated()
		}
		if p.stopFn != nil {
			p.stopFn()
		}
		return false
	}
	return true
}

func (p *eventPump) resolveLocation(ctx context.Context, threadID int) (string, int) {
	stack, err := p.dap.StackTrace(ctx, threadID)
	if err != nil || stack == nil {
		return "", 0
	}
	for _, frame := range asMapSlice(stack["stackFrames"]) {
		line := intFromAny(frame["line"])
		source, _ := frame["source"].(map[string]any)
		path := ""
		if source != nil {
			path, _ = source["path"].(string)
		}
		if path != "" || line != 0 {
			return path, line
		}
	}
	return "", 0
}

func (p *eventPump) stop() {
	if p.stopFn != nil {
		p.stopFn()
	}
	<-p.done
}
