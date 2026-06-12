// pump_test.go 验证 DAP 事件泵对 debugger 快照的维护。
//
// 职责：
//   - 用可控 fake DAP 驱动 stopped/continued 事件
//   - 验证事件泵把 DAP 事件转换为 runtime debugger 快照
//
// 边界：
//   - 不启动真实 DAP adapter
//   - 不覆盖 manager 生命周期接入
package codedebug

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakePumpDAP struct {
	mu    sync.Mutex
	subs  []chan map[string]any
	stack map[string]any
}

func (f *fakePumpDAP) Subscribe() (<-chan map[string]any, func()) {
	f.mu.Lock()
	ch := make(chan map[string]any, 16)
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			for i, sub := range f.subs {
				if sub == ch {
					f.subs = append(f.subs[:i], f.subs[i+1:]...)
					close(ch)
					break
				}
			}
			f.mu.Unlock()
		})
	}
	return ch, cancel
}

func (f *fakePumpDAP) emit(event map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, ch := range f.subs {
		ch <- event
	}
}

func (f *fakePumpDAP) StackTrace(context.Context, int) (map[string]any, error) {
	return f.stack, nil
}

func TestPumpStoppedThenContinued(t *testing.T) {
	dap := &fakePumpDAP{stack: map[string]any{
		"stackFrames": []any{
			map[string]any{
				"id":   float64(1),
				"line": float64(42),
				"source": map[string]any{
					"path": "/proj/main.go",
				},
			},
		},
	}}
	snap := &debuggerSnapshotStore{}
	pump := newEventPump(dap, snap)
	pump.start(context.Background())
	defer pump.stop()

	dap.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": float64(1)}})
	waitForPump(t, func() bool {
		got := snap.get()
		return got.State == "paused" && got.ThreadID == 1 && got.Line == 42 && got.Source == "/proj/main.go"
	})

	dap.emit(map[string]any{"event": "continued", "body": map[string]any{}})
	waitForPump(t, func() bool {
		return snap.get().State == "attached"
	})
}

func waitForPump(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
