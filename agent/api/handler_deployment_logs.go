// handler_deployment_logs.go 实现 Deployment 统一日志接口。
//
// 职责：
//   - GET /api/deployments/{id}/logs：按游标拉取历史日志
//   - GET /api/deployments/{id}/search：按关键字搜索历史日志
//   - GET /ws/deployments/{id}/logs：WebSocket 实时日志推送
//
// 边界：
//   - handler 只调 LogBackend 接口，不判断 location（local/remote）
//   - 找不到 deployment 或未构造 backend 时返回 404
package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

type deploymentLogsResponse struct {
	Items []model.LogEntry `json:"items"`
	Next  struct {
		Time string `json:"time,omitempty"`
		ID   int64  `json:"id,omitempty"`
	} `json:"next"`
}

type deploymentSearchResponse struct {
	Query   string           `json:"query"`
	Items   []model.LogEntry `json:"items"`
	HasMore bool             `json:"has_more"`
	Next    struct {
		Time string `json:"time,omitempty"`
		ID   int64  `json:"id,omitempty"`
	} `json:"next"`
}

// fetchDeploymentLogs 处理 GET /api/deployments/{id}/logs。
//
// 参数（query string）：
//   - run: 按 RunID 过滤（可选）
//   - limit: 最大返回条数（默认 1000，上限由 maxLimit 控制）
//   - before: 返回 id < before 的记录（游标分页）
func (a *App) fetchDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	backend, ok := a.lookupBackend(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}

	q := r.URL.Query()
	filter := logbackend.QueryFilter{
		DeploymentID: depID,
		RunID:        q.Get("run"),
		Limit:        parseBoundedInt(q.Get("limit"), 1000, maxLimit),
	}
	if beforeStr := q.Get("before"); beforeStr != "" {
		before, err := strconv.ParseInt(beforeStr, 10, 64)
		if err != nil || before <= 0 {
			jsonError(w, http.StatusBadRequest, "before is invalid")
			return
		}
		filter.BeforeID = before
	}

	entries, next, err := backend.Query(r.Context(), filter)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch logs: "+err.Error())
		return
	}
	if entries == nil {
		entries = []model.LogEntry{}
	}

	resp := deploymentLogsResponse{Items: entries}
	if !next.Time.IsZero() {
		resp.Next.Time = next.Time.Format(time.RFC3339Nano)
		resp.Next.ID = next.ID
	}
	jsonOK(w, resp)
}

// searchDeploymentLogs 处理 GET /api/deployments/{id}/search。
//
// 参数（query string）：
//   - q: 搜索关键词（必填）
//   - limit: 最大返回条数
//   - cursor_time / cursor_id: 翻页游标（可选）
func (a *App) searchDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	backend, ok := a.lookupBackend(depID)
	if !ok {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}

	q := r.URL.Query()
	text := searchQueryText(q)
	if text == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	sq := logbackend.SearchQuery{
		Text:  text,
		Limit: parseBoundedInt(q.Get("limit"), defaultSearchLimit, maxSearchLimit),
	}
	if rawCursorTime := q.Get("cursor_time"); rawCursorTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawCursorTime)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "cursor_time is invalid")
			return
		}
		cursorID, err := strconv.ParseInt(q.Get("cursor_id"), 10, 64)
		if err != nil || cursorID <= 0 {
			jsonError(w, http.StatusBadRequest, "cursor_id is required")
			return
		}
		sq.Cursor = logbackend.Cursor{Time: parsed, ID: cursorID}
	}

	entries, next, hasMore, err := backend.Search(r.Context(), sq)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to search logs: "+err.Error())
		return
	}
	if entries == nil {
		entries = []model.LogEntry{}
	}

	resp := deploymentSearchResponse{Query: text, Items: entries, HasMore: hasMore}
	if !next.Time.IsZero() {
		resp.Next.Time = next.Time.Format(time.RFC3339Nano)
		resp.Next.ID = next.ID
	}
	jsonOK(w, resp)
}

// wsDeploymentLogs 处理 GET /ws/deployments/{id}/logs，建立 WebSocket 推送实时日志。
//
// 连接建立后，实时推送 Subscribe 返回的日志条目；客户端断开或 ctx 取消时自动退出。
func (a *App) wsDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	depID := r.PathValue("id")
	backend, ok := a.lookupBackend(depID)
	if !ok {
		http.Error(w, "deployment not found", http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	stream := backend.Subscribe(r.Context(), depID)
	defer stream.Cancel()

	ctx := r.Context()
	for {
		select {
		case entry, ok := <-stream.Ch:
			if !ok {
				return
			}
			if err := conn.WriteJSON(entry); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// lookupBackend 在 backends 映射中查找指定 deployment 的 LogBackend。
func (a *App) lookupBackend(depID string) (logbackend.LogBackend, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	b, ok := a.backends[depID]
	return b, ok
}
