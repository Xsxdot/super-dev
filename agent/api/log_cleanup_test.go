// log_cleanup_test.go 验证 agent 本机日志后台淘汰任务。
//
// 职责：
//   - 覆盖清理器单次执行时按保留期删除过期日志
//   - 固化后台任务与 store 淘汰接口之间的边界
//
// 边界：
//   - 不启动真实 HTTP 服务
//   - 不依赖定时器等待，单测只调用 runOnce
package api

import (
	"context"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

func TestLogCleanupRunOnceEvictsByTimeAndSize(t *testing.T) {
	s, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	old := model.LogEntry{
		DeploymentID: "A",
		RunID:        "r",
		Timestamp:    time.Now().UTC().AddDate(0, 0, -100),
		Level:        "INFO",
		Message:      "old",
		Stream:       "stdout",
		RepeatCount:  1,
	}
	if err := s.AppendBatch([]model.LogEntry{old}); err != nil {
		t.Fatal(err)
	}

	cleaner := newLogCleaner(s, cleanupConfig{RetentionDays: 7, MaxBytes: 256 * 1024 * 1024})
	if err := cleaner.runOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s.Fetch(store.FetchParams{DeploymentID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected old log evicted by retention, got %d", len(got))
	}
}
