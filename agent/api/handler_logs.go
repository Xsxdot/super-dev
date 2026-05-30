// handler_logs.go 实现日志 REST 查询接口。
//
// 职责：
//   - 解析查询参数并调用 store.Fetch 返回历史日志
//   - 保证空结果返回 [] 而非 null（避免前端 JSON 解析问题）
//
// 边界：
//   - 不处理实时日志推送（由 handler_ws.go 负责）
//   - 不解析日志格式，直接透传 store 返回的 LogEntry
package api

import (
	"net/http"
	"strconv"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

// maxLimit 是单次日志查询允许的最大条数，防止超大查询打满 SQLite。
const maxLimit = 5000

// fetchLogs 处理 GET /api/logs，支持以下查询参数：
//   - deployment: 按 DeploymentID 过滤
//   - run: 按 RunID 过滤
//   - limit: 返回条数上限（默认 1000，最大 5000）
//   - before: 返回 id < before 的记录（游标分页）
func (a *App) fetchLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	params := store.FetchParams{
		DeploymentID: q.Get("deployment"),
		RunID:        q.Get("run"),
	}

	if limitStr := q.Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 {
			if limit > maxLimit {
				limit = maxLimit
			}
			params.Limit = limit
		}
	}

	if beforeStr := q.Get("before"); beforeStr != "" {
		if before, err := strconv.ParseInt(beforeStr, 10, 64); err == nil && before > 0 {
			params.Before = before
		}
	}

	entries, err := a.store.Fetch(params)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch logs: "+err.Error())
		return
	}

	// 保证返回 [] 而非 null，防止前端 JSON.parse 得到 null
	if entries == nil {
		entries = []model.LogEntry{}
	}

	jsonOK(w, entries)
}
