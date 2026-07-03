// fold_test.go 验证日志折叠车道的纯逻辑。
//
// 职责：
//   - 覆盖同 deployment 同签名窗口内折叠、deployment 分车道、超窗新段和过期收尾
//
// 边界：
//   - 只测试无锁无 I/O 的 foldTracker，不涉及 Buffer、store 或订阅 channel
package logbuf

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

func entryAt(dep, msg string, ts time.Time) model.LogEntry {
	return model.LogEntry{DeploymentID: dep, Message: msg, Timestamp: ts, Stream: "stdout", Level: "INFO"}
}

func TestFoldTrackerSameLaneWithinWindow(t *testing.T) {
	tr := newFoldTracker(5*time.Second, nil)
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	// 第一条：开新段，emit=新行，inc=nil。
	emit, inc := tr.observe(entryAt("A", "boom count=1", t0))
	if emit == nil || inc != nil {
		t.Fatalf("first entry: want emit new row, got emit=%v inc=%v", emit, inc)
	}
	if emit.RepeatCount != 1 || emit.FoldKey == "" {
		t.Fatalf("first emit RepeatCount=%d FoldKey=%q", emit.RepeatCount, emit.FoldKey)
	}

	// 第二条：同签名（count=1 与 count=2 归一化后相同）窗口内 → 折叠。
	emit2, inc2 := tr.observe(entryAt("A", "boom count=2", t0.Add(time.Second)))
	if emit2 != nil || inc2 == nil {
		t.Fatalf("second entry: want fold increment, got emit=%v inc=%v", emit2, inc2)
	}
	if inc2.RepeatCount != 2 || inc2.FoldKey != emit.FoldKey {
		t.Fatalf("inc RepeatCount=%d FoldKey=%q want 2/%q", inc2.RepeatCount, inc2.FoldKey, emit.FoldKey)
	}
}

func TestFoldTrackerLanesIndependent(t *testing.T) {
	tr := newFoldTracker(5*time.Second, nil)
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	tr.observe(entryAt("A", "boom", t0))
	emitB, incB := tr.observe(entryAt("B", "hi", t0))
	if emitB == nil || incB != nil {
		t.Fatalf("B first: want new row")
	}
	emitA2, incA2 := tr.observe(entryAt("A", "boom", t0.Add(time.Second)))
	if emitA2 != nil || incA2 == nil || incA2.RepeatCount != 2 {
		t.Fatalf("A second: want fold to 2, got emit=%v inc=%v", emitA2, incA2)
	}
}

func TestFoldTrackerWindowExpiry(t *testing.T) {
	tr := newFoldTracker(5*time.Second, nil)
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)

	tr.observe(entryAt("A", "boom", t0))
	emit, inc := tr.observe(entryAt("A", "boom", t0.Add(6*time.Second)))
	if emit == nil || inc != nil {
		t.Fatalf("expired: want new row (not fold), got emit=%v inc=%v", emit, inc)
	}
	if emit.RepeatCount != 1 {
		t.Fatalf("new segment RepeatCount=%d want 1", emit.RepeatCount)
	}
}

func TestFoldTrackerSweepClosesStale(t *testing.T) {
	tr := newFoldTracker(5*time.Second, nil)
	t0 := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	tr.observe(entryAt("A", "boom count=1", t0))
	tr.observe(entryAt("A", "boom count=2", t0.Add(time.Second)))

	closed := tr.sweep(t0.Add(10 * time.Second))
	if len(closed) != 1 || closed[0].RepeatCount != 2 {
		t.Fatalf("sweep closed=%v, want 1 row RepeatCount=2", closed)
	}
	if again := tr.sweep(t0.Add(20 * time.Second)); len(again) != 0 {
		t.Fatalf("second sweep produced %d rows, want 0", len(again))
	}
}

func TestNextFoldKeyEmptyRunIDCarriesProcessEpoch(t *testing.T) {
	// 空 run_id（采集类日志的正常情况）时，fold_key 必须带进程启动 epoch，
	// 否则 agent 重启后纯自增序号归零会与历史 fold_key 撞键，
	// 落库 UPSERT 把新日志 UPDATE 进重启前旧行（store 卡在重启点）。
	if processFoldEpoch == "" {
		t.Fatal("processFoldEpoch 应在包初始化时被赋值为非空唯一值")
	}
	key := nextFoldKey("")
	if !strings.Contains(key, processFoldEpoch) {
		t.Fatalf("空 run_id 的 fold_key=%q 应包含进程 epoch=%q", key, processFoldEpoch)
	}

	// 模拟「重启」：epoch 改变后，即使自增序号回到同一值，fold_key 也必须不同。
	oldEpoch := processFoldEpoch
	atomic.StoreUint64(&foldKeySeq, 0)
	keyA := nextFoldKey("")
	processFoldEpoch = oldEpoch + "-restarted"
	atomic.StoreUint64(&foldKeySeq, 0)
	keyB := nextFoldKey("")
	if keyA == keyB {
		t.Fatalf("epoch 变化后同序号的 fold_key 不应相同: %q == %q", keyA, keyB)
	}
}
