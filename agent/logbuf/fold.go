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
	seq         uint64
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
	window  time.Duration
	lanes   map[string]*foldLane
	nextSeq func(deploymentID string) uint64
}

var foldKeySeq uint64

// processFoldEpoch 是本 agent 进程的启动唯一标识，进程重启必变、生命周期内不变。
//
// 作用：fold_key 落库时的 UPSERT 冲突键是 (run_id, fold_key)。采集类日志（journalctl/
// process 进程 stdout）的 run_id 恒为空，若 fold_key 只用进程内自增序号 foldKeySeq，
// agent 重启后序号归零会与历史 fold_key 精确撞键，新日志被 UPDATE 进重启前旧行，
// 表现为「实时能看到新日志、刷新就没、且每次卡在 agent 重启那一刻」。
// 用启动纳秒时间戳做 epoch 顶替缺失的 run 维度，保证跨重启永不撞键。
var processFoldEpoch = strconv.FormatInt(time.Now().UnixNano(), 36)

// nextFoldKey 生成本次折叠段的 fold_key。
//
// 参数：
//   - runID: 日志所属的 run 会话 id；采集类服务日志恒为空，pipeline run 日志非空
//
// 返回：
//   - 跨 run/跨 agent 重启唯一的 fold_key
//
// 注意：
//   - runID 非空时用 runID 维度（pipeline run 日志）
//   - runID 为空时用进程启动 epoch 维度（采集类日志的正常路径），
//     避免重启后纯序号归零撞键——这是生产主链路，不是理论兜底
func nextFoldKey(runID string) string {
	seq := strconv.FormatUint(atomic.AddUint64(&foldKeySeq, 1), 36)
	if runID == "" {
		return "f" + processFoldEpoch + ":" + seq
	}
	return "f" + runID + ":" + seq
}

// newFoldTracker 创建折叠跟踪器。
//
// 参数：
//   - window: 折叠时间窗口，超过则同签名也不折叠（保留时间真相）
//   - nextSeq: 开新段时分配 deployment 内单调序号；nil 表示调用方不需要 seq
func newFoldTracker(window time.Duration, nextSeq func(string) uint64) *foldTracker {
	return &foldTracker{window: window, lanes: map[string]*foldLane{}, nextSeq: nextSeq}
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
		lane.rep.Seq = lane.seq
		return nil, &foldIncrement{DeploymentID: e.DeploymentID, FoldKey: lane.foldKey, RepeatCount: lane.repeatCount}
	}

	key := nextFoldKey(e.RunID)
	row := e
	row.RepeatCount = 1
	row.FoldKey = key
	// seq 只在开新段（真正产生存储新行）时分配；折叠命中沿用段首 seq，
	// 保证 (deployment_id, seq) 与存储行一一对应、折叠 UPSERT 不产生序号空洞。
	if t.nextSeq != nil {
		row.Seq = t.nextSeq(e.DeploymentID)
	}
	t.lanes[e.DeploymentID] = &foldLane{
		signature:   sig,
		foldKey:     key,
		seq:         row.Seq,
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
