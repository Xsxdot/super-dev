// handler_run_logs_ws.go 实现 pipeline run 级实时日志 WebSocket。
//
// 职责：
//   - 校验 run 是否存在
//   - 先订阅 RunHub，再回放数据库日志，避免回放和订阅之间出现事件空窗
//   - 将后续 RunHub 事件转发给 WebSocket 客户端
//
// 边界：
//   - 不执行 pipeline
//   - 不做前端去重，重复日志由客户端按 log.id 幂等合并
package api

import (
	"net/http"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

// wsRunLogs 处理 GET /ws/runs/{runId}/logs，建立 run 实时日志连接。
func (a *App) wsRunLogs(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runId")
	run, ok, err := a.store.GetRun(runID)
	if err != nil {
		http.Error(w, "failed to get pipeline run", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "pipeline run not found", http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	sub, unsubscribe := a.runHub.Subscribe(runID)
	defer unsubscribe()

	if err := a.writeRunLogReplay(conn, runID); err != nil {
		return
	}
	if isTerminalRunStatus(run.Status) {
		_ = conn.WriteJSON(RunEvent{Kind: RunEventKindDone, Run: &run})
		return
	}

	ctx := r.Context()
	for {
		select {
		case ev, ok := <-sub.Ch():
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
			if ev.Kind == RunEventKindDone {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) writeRunLogReplay(conn interface{ WriteJSON(v any) error }, runID string) error {
	var afterID int64
	for {
		lines, err := a.store.ReadRunLogs(store.RunLogQuery{
			RunID:     runID,
			Limit:     maxLimit,
			AfterID:   afterID,
			Ascending: true,
		})
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			return nil
		}
		for i := range lines {
			line := lines[i]
			if err := conn.WriteJSON(RunEvent{Kind: RunEventKindLog, Log: &line}); err != nil {
				return err
			}
		}
		afterID = lines[len(lines)-1].ID
		if len(lines) < maxLimit {
			return nil
		}
	}
}

func isTerminalRunStatus(status model.RunStatus) bool {
	return status == model.StatusSuccess || status == model.RunStatusFailed || status == model.StatusCanceled
}
