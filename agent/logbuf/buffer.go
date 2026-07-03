// Package logbuf 提供内存环形日志缓冲功能。
//
// 职责：
//   - 以环形缓冲方式保存最近 maxSize 条日志条目
//   - 每 100ms 或积累 50 条时批量 flush 到持久化存储（store）
//   - 支持 WebSocket 订阅者实时接收新日志推送
//   - 在写入链路对重复日志做折叠，降低落库与实时推送成本
//
// 边界：
//   - 不解析日志格式，仅存储已构造好的 model.LogEntry
//   - 不直接依赖 store 包，通过 LogWriter 接口解耦，便于测试注入 nil
//   - 订阅者 channel 满时跳过写入，不阻塞生产者
package logbuf

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// flushInterval 是批量 flush 到 store 的最大等待间隔。
	flushInterval = 100 * time.Millisecond
	// flushBatch 是触发立即 flush 的积累条数阈值。
	flushBatch = 50
	// defaultFoldWindow 是折叠时间窗口默认值；超窗的同签名日志不折叠。
	defaultFoldWindow = 5 * time.Second
)

// LogWriter 是 buffer 的持久化出口（与 logbackend.LogWriter 结构等价）。
//
// 本地定义避免 logbuf → logbackend → logbuf 的 import 环。
type LogWriter interface {
	// AppendBatch 批量写入日志条目。ctx 为未来中心化写入保留超时/取消能力。
	AppendBatch(ctx context.Context, entries []model.LogEntry) error
}

// Buffer 是线程安全的环形日志缓冲，支持订阅推送和批量持久化。
type Buffer struct {
	mu      sync.RWMutex
	ring    []model.LogEntry
	head    int // 下一个写入位置
	count   int // 历史总写入数（用于判断 ring 是否写满）
	maxSize int
	pending []model.LogEntry
	subs    map[string]chan model.LogEntry
	store   LogWriter
	done    chan struct{}
	nodeID  string // 本机 node_id，Append 时填入 LogEntry.SourceID（空字符串时不填充）
	fold    *foldTracker
	foldPos map[string]int // fold_key -> ring index，用于折叠增量同步 Recent 回放计数
}

// New 创建并启动一个新的日志缓冲实例。
//
// 参数：
//   - store: 持久化接口，传 nil 时跳过持久化（测试场景）
//   - maxSize: 环形缓冲容量，保留最近 maxSize 条日志
//   - nodeID: 本机 node_id，Append 时用于填充 LogEntry.SourceID；传空字符串时不填充
//   - seqWatermarks: 每个 deployment 已落库最大 seq；nil 表示从 1 开始（测试/空库）
//
// 返回：
//   - 已启动 flush goroutine 的 *Buffer
func New(store LogWriter, maxSize int, nodeID string, seqWatermarks map[string]uint64) *Buffer {
	watermarks := map[string]uint64{}
	for dep, max := range seqWatermarks {
		watermarks[dep] = max
	}
	b := &Buffer{
		ring:    make([]model.LogEntry, maxSize),
		maxSize: maxSize,
		subs:    map[string]chan model.LogEntry{},
		store:   store,
		done:    make(chan struct{}),
		nodeID:  nodeID,
		foldPos: map[string]int{},
	}
	// nextSeq 在 Buffer.mu 保护下由 observe 调用（Append 持锁），无需自己加锁。
	b.fold = newFoldTracker(defaultFoldWindow, func(dep string) uint64 {
		watermarks[dep]++
		return watermarks[dep]
	})
	go b.flushLoop()
	return b
}

// SetFoldWindow 设置折叠时间窗口（启动时按配置注入）。
func (b *Buffer) SetFoldWindow(d time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fold.window = d
}

// incrementEntry 把折叠增量编码为一条 model.LogEntry，供实时订阅 channel 透传。
//
// 约定：Message 为空 + FoldKey 非空 + RepeatCount 为最新值 + ID 为 0。
// 下游据此识别为"对现有折叠行的计数更新"，而非新行。
func incrementEntry(inc *foldIncrement) model.LogEntry {
	return model.LogEntry{
		DeploymentID: inc.DeploymentID,
		FoldKey:      inc.FoldKey,
		RepeatCount:  inc.RepeatCount,
	}
}

// Append 追加一条日志：先做折叠判定，再决定落库与实时推送。
//
// SourceID 填充：若 e.SourceID 为空且 b.nodeID 非空，则自动填充 e.SourceID = b.nodeID；
// 若 e.SourceID 已有值（远端日志转发场景），不覆盖。
//
// 注意：
//   - 折叠命中时不新增 ring 行，只推送增量条目并把最终计数交给 store upsert
//   - 订阅者 channel 满时跳过写入，不阻塞调用方
//   - 当 pending 积累达到 flushBatch 时触发立即 flush
func (b *Buffer) Append(e model.LogEntry) {
	if e.SourceID == "" && b.nodeID != "" {
		e.SourceID = b.nodeID
	}

	b.mu.Lock()
	emit, inc := b.fold.observe(e)
	var pushed model.LogEntry
	if emit != nil {
		if old := b.ring[b.head]; old.FoldKey != "" {
			delete(b.foldPos, old.FoldKey)
		}
		b.ring[b.head] = *emit
		b.foldPos[emit.FoldKey] = b.head
		b.head = (b.head + 1) % b.maxSize
		b.count++
		b.pending = append(b.pending, *emit)
		pushed = *emit
	} else {
		rep := b.fold.lanes[e.DeploymentID].rep
		b.pending = append(b.pending, rep)
		if idx, ok := b.foldPos[inc.FoldKey]; ok && b.ring[idx].FoldKey == inc.FoldKey {
			// Recent() 回放读 ring；折叠命中时同步计数，避免重连拿到旧 repeat_count。
			b.ring[idx].RepeatCount = inc.RepeatCount
			b.ring[idx].Timestamp = rep.Timestamp
		}
		pushed = incrementEntry(inc)
	}
	shouldFlush := len(b.pending) >= flushBatch
	// 在持有锁时收集订阅者列表，避免遍历时并发修改
	subs := make([]chan model.LogEntry, 0, len(b.subs))
	for _, ch := range b.subs {
		subs = append(subs, ch)
	}
	b.mu.Unlock()

	// 锁外推送，避免阻塞 Append
	for _, ch := range subs {
		select {
		case ch <- pushed:
		default:
			// 订阅者消费过慢时丢弃，不阻塞生产者
		}
	}

	if shouldFlush {
		b.flush()
	}
}

// Recent 返回最近 n 条日志（按时间正序，从旧到新）。
//
// 参数：
//   - n: 期望返回的条数，实际返回数不超过缓冲中已有条数
//
// 返回：
//   - 按时间正序排列的日志切片
func (b *Buffer) Recent(n int) []model.LogEntry {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// 实际有效条数：ring 写满后上限为 maxSize
	total := b.count
	if total > b.maxSize {
		total = b.maxSize
	}
	if n > total {
		n = total
	}
	out := make([]model.LogEntry, n)
	// 从 head 往回数 n 格，即最旧的一条的位置
	// 使用 maxSize 而非 total 保证模运算在环上正确
	start := (b.head - n + b.maxSize) % b.maxSize
	for i := 0; i < n; i++ {
		out[i] = b.ring[(start+i)%b.maxSize]
	}
	return out
}

// Subscribe 注册一个订阅者，返回接收日志的只读 channel。
//
// 参数：
//   - id: 订阅者唯一标识，用于后续 Unsubscribe
//
// 返回：
//   - 缓冲大小为 64 的只读 channel，Append 时写入
func (b *Buffer) Subscribe(id string) <-chan model.LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan model.LogEntry, 64)
	b.subs[id] = ch
	return ch
}

// Unsubscribe 注销订阅者并关闭对应 channel。
//
// 参数：
//   - id: 注册时使用的订阅者标识
func (b *Buffer) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ch, ok := b.subs[id]; ok {
		close(ch)
		delete(b.subs, id)
	}
}

// Close 停止 flush goroutine 并将剩余 pending 日志 flush 到 store。
func (b *Buffer) Close() {
	close(b.done)
	b.mu.Lock()
	for _, row := range b.fold.closeAll() {
		if row.RepeatCount > 1 {
			b.pending = append(b.pending, row)
		}
	}
	b.mu.Unlock()
	b.flush()
}

// flushLoop 按固定间隔触发批量 flush，直到 Buffer 关闭。
func (b *Buffer) flushLoop() {
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			b.sweepFolds()
			b.flush()
		case <-b.done:
			return
		}
	}
}

// sweepFolds 把超过折叠窗口仍未收尾的段强制落库（计数最终化）。
func (b *Buffer) sweepFolds() {
	b.mu.Lock()
	closed := b.fold.sweep(time.Now())
	for _, row := range closed {
		if row.RepeatCount > 1 {
			b.pending = append(b.pending, row)
		}
	}
	b.mu.Unlock()
}

// flush 将当前 pending 批次写入 store，写完后清空 pending。
func (b *Buffer) flush() {
	b.mu.Lock()
	if len(b.pending) == 0 {
		b.mu.Unlock()
		return
	}
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()

	if b.store != nil {
		// 落库失败不能再静默吞：曾因 fold_key 撞键等问题导致持久化长期失败而无人知晓。
		// 这里打印批次大小与首条 deployment/fold_key 上下文，便于 tail_logs 直接定位。
		if err := b.store.AppendBatch(context.Background(), batch); err != nil {
			log.Printf("[logbuf] flush 落库失败 batch=%d deployment=%s fold_key=%s: %v",
				len(batch), batch[0].DeploymentID, batch[0].FoldKey, err)
		}
	}
}
