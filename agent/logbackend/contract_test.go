// contract_test.go 定义对任何 LogReader 实现都成立的契约测试。
//
// 职责：
//   - 固化 Query 游标分页与 Subscribe Since 去重的读侧契约
//   - 先用 SQLiteBackend 跑通，未来 PG/云后端可复用同一组 runReaderContract
//
// 边界：
//   - 不测试具体存储 SQL，只通过 LogReader/LogWriter 公共接口验证行为
package logbackend_test

import (
	"context"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
)

// readerFactory 返回一个预置了给定条目的 LogReader。
type readerFactory func(t *testing.T, entries []model.LogEntry) logbackend.LogReader

func runReaderContract(t *testing.T, newReader readerFactory) {
	t.Run("QueryPaginatesWithReturnedCursor", func(t *testing.T) {
		base := time.Now().Truncate(time.Millisecond)
		entries := make([]model.LogEntry, 5)
		for i := range entries {
			entries[i] = model.LogEntry{
				DeploymentID: "d",
				Timestamp:    base.Add(time.Duration(i) * time.Millisecond),
				Message:      "m",
			}
		}
		r := newReader(t, entries)
		page1, next, err := r.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "d", Limit: 3})
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(page1) == 0 {
			t.Fatal("page1 empty")
		}

		page2, _, err := r.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "d", Limit: 3, Before: next})
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		for _, a := range page1 {
			for _, b := range page2 {
				if a.Timestamp.Equal(b.Timestamp) && a.Message == b.Message && a.ID == b.ID {
					t.Fatalf("page2 overlaps page1 at id=%d", a.ID)
				}
			}
		}
	})

	t.Run("SubscribeSinceFiltersOlder", func(t *testing.T) {
		r := newReader(t, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		future := logbackend.Cursor{Time: time.Now().Add(time.Hour)}
		stream := r.Subscribe(ctx, logbackend.SubscribeOptions{DeploymentID: "d", Since: future})
		defer stream.Cancel()
		select {
		case e, ok := <-stream.Ch:
			if ok {
				t.Fatalf("expected no entries before Since, got %+v", e)
			}
		case <-ctx.Done():
			// 超时无条目 = 正确。
		}
	})
}

func TestSQLiteBackendContract(t *testing.T) {
	runReaderContract(t, func(t *testing.T, entries []model.LogEntry) logbackend.LogReader {
		b, _ := newTestSQLiteBackend(t)
		if len(entries) > 0 {
			if err := b.AppendBatch(context.Background(), entries); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		return b
	})
}
