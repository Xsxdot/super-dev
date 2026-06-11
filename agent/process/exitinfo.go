// Package process 提供进程退出证据的数据结构。
//
// 职责：
//   - stderrRing：固定容量环形缓冲，保留最后 N 行 stderr
//   - ExitInfo：承载启动失败或退出时的结构化证据
//
// 边界：
//   - 不解析 stderr 语义，只保留原始文本
//   - 不感知 deployment 或 API 层状态
package process

import "sync"

// stderrRing 是固定容量环形缓冲，线程安全，保留最后 cap 行。
type stderrRing struct {
	mu    sync.Mutex
	buf   []string
	cap   int
	start int
	size  int
}

func newStderrRing(capacity int) *stderrRing {
	if capacity <= 0 {
		capacity = 1
	}
	return &stderrRing{buf: make([]string, capacity), cap: capacity}
}

// push 追加一行；超出容量时覆盖最旧的一行。
func (r *stderrRing) push(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := (r.start + r.size) % r.cap
	r.buf[idx] = line
	if r.size < r.cap {
		r.size++
		return
	}
	r.start = (r.start + 1) % r.cap
}

// tail 返回当前保留的所有行，按时间从旧到新排列。
func (r *stderrRing) tail() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	out := make([]string, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.start+i)%r.cap]
	}
	return out
}

// ExitReason 描述进程生命周期失败或退出原因。
type ExitReason string

const (
	// ExitReasonExited 表示进程正常退出，可通过 ExitCode 判断成功或失败。
	ExitReasonExited ExitReason = "exited"
	// ExitReasonSignaled 表示进程被信号终止。
	ExitReasonSignaled ExitReason = "signaled"
	// ExitReasonStartFailed 表示进程尚未启动成功，exec.Start 返回错误。
	ExitReasonStartFailed ExitReason = "start_failed"
)

// ExitInfo 描述一次进程启动失败或退出的结构化证据。
type ExitInfo struct {
	Reason     ExitReason
	ExitCode   int
	Signaled   bool
	Signal     string
	Error      string
	StderrTail []string
}
