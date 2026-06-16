// fold.go 提供按 deployment 分车道的重复日志折叠判定逻辑。
//
// 职责：
//   - 为每个 deployment 维护一条"折叠车道"，将时间窗口内、归一化签名相同的连续日志折叠
//   - observe：判定单条日志是"开新段（需落库的新行）"还是"折叠（增量计数）"
//   - sweep：把长时间无新行的段强制收尾，产出待落库行
//
// 边界：
//   - 纯逻辑，无锁、无 channel、无 I/O；并发安全由调用方（Buffer）的锁保证
//   - 不直接写 store，不直接推送订阅者；只返回"该落库什么/该增量推什么"的决策
package logbuf

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/xsxdot/super-dev/agent/logparse"
	"github.com/xsxdot/super-dev/agent/model"
)

// foldLane 记录单个 deployment 当前正在累积的折叠段。
type foldLane struct {
	signature   string
	foldKey     string
	repeatCount int
	rep         model.LogEntry
	lastSeen    time.Time
}

// foldIncrement 表示一次折叠产生的实时增量信号。
type foldIncrement struct {
	DeploymentID string
	FoldKey      string
	RepeatCount  int
}

// foldTracker 持有所有 deployment 的折叠车道。
type foldTracker struct {
	window time.Duration
	lanes  map[string]*foldLane
}

var foldKeySeq uint64

// nextFoldKey 生成本次折叠段的 fold_key。
//
// 必须带 runID 维度：foldKeySeq 是进程内自增计数器，agent 重启会归零，
// 若不带 run 维度，重启后新会话重发的序号会与历史 fold_key 撞键；
// 而 fold_key 是落库 upsert 的冲突键，撞键会让新日志被 UPDATE 进重启前的旧行，
// 表现为「实时日志卡在重启前不刷新」。带上 runID 后跨 run/跨重启永不撞键。
//
// runID 为空时退化为纯序号（仅理论兜底，正常链路 LogEntry.RunID 必非空）。
func nextFoldKey(runID string) string {
	seq := strconv.FormatUint(atomic.AddUint64(&foldKeySeq, 1), 36)
	if runID == "" {
		return "f" + seq
	}
	return "f" + runID + ":" + seq
}

// newFoldTracker 创建折叠跟踪器。
//
// 参数：
//   - window: 折叠时间窗口，超过则同签名也不折叠（保留时间真相）
func newFoldTracker(window time.Duration) *foldTracker {
	return &foldTracker{window: window, lanes: map[string]*foldLane{}}
}

// observe 处理一条新日志，返回两个互斥结果之一：
//   - emit != nil：需要落库的新行（开了新段；emit.RepeatCount==1）
//   - inc  != nil：折叠到现有段（实时增量信号，emit 为 nil）
//
// 注意：
//   - 该方法只维护内存中的当前段；最终持久化由 Buffer 将 rep 交给 store upsert 完成。
func (t *foldTracker) observe(e model.LogEntry) (emit *model.LogEntry, inc *foldIncrement) {
	sig := logparse.Normalize(e.Message)
	lane, ok := t.lanes[e.DeploymentID]
	if ok && lane.signature == sig && e.Timestamp.Sub(lane.lastSeen) < t.window {
		// 同车道、同签名、窗口内：只更新当前段计数，不产生新行。
		lane.repeatCount++
		lane.lastSeen = e.Timestamp
		lane.rep = e
		lane.rep.RepeatCount = lane.repeatCount
		lane.rep.FoldKey = lane.foldKey
		return nil, &foldIncrement{DeploymentID: e.DeploymentID, FoldKey: lane.foldKey, RepeatCount: lane.repeatCount}
	}

	key := nextFoldKey(e.RunID)
	row := e
	row.RepeatCount = 1
	row.FoldKey = key
	t.lanes[e.DeploymentID] = &foldLane{
		signature:   sig,
		foldKey:     key,
		repeatCount: 1,
		rep:         row,
		lastSeen:    e.Timestamp,
	}
	return &row, nil
}

// sweep 收尾所有 now 超过 lastSeen+window 的车道，返回这些段待落库的代表行。
func (t *foldTracker) sweep(now time.Time) []model.LogEntry {
	var closed []model.LogEntry
	for dep, lane := range t.lanes {
		if now.Sub(lane.lastSeen) >= t.window {
			closed = append(closed, lane.rep)
			delete(t.lanes, dep)
		}
	}
	return closed
}

// closeAll 收尾所有车道（进程退出时调用），返回全部待落库代表行。
func (t *foldTracker) closeAll() []model.LogEntry {
	var closed []model.LogEntry
	for dep, lane := range t.lanes {
		closed = append(closed, lane.rep)
		delete(t.lanes, dep)
	}
	return closed
}
