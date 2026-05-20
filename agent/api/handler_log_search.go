// Package api 提供项目维度的历史日志搜索 HTTP 接口。
//
// 职责：
//   - 解析日志搜索和上下文查询参数
//   - 在查询 Store 前收敛到项目服务范围
//   - 返回桌面端排障看板需要的原始日志数据
//
// 边界：
//   - 不应用项目日志过滤规则
//   - 不为 UI 时间栅格做格式化或分组
//   - 不暴露 Store 内部实现细节，只返回 HTTP 响应 DTO
package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

const (
	defaultSearchLimit = 1000
	maxSearchLimit     = 5000
	defaultContextMS   = 30000
	maxContextMS       = 300000
)

type logSearchResponse struct {
	Query         string           `json:"query"`
	Total         int              `json:"total"`
	Items         []model.LogEntry `json:"items"`
	ServiceCounts map[string]int   `json:"service_counts"`
}

type logContextResponse struct {
	TargetID       int64                       `json:"target_id"`
	AnchorTime     time.Time                   `json:"anchor_time"`
	ItemsByService map[string][]model.LogEntry `json:"items_by_service"`
}

// searchLogs 处理 GET /api/log-search，按项目服务集合搜索历史日志。
func (a *App) searchLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	queryText := strings.TrimSpace(q.Get("q"))
	if projectID == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	if queryText == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	serviceIDs, ok := a.projectServiceIDs(projectID, q["service"])
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}

	limit := parseBoundedInt(q.Get("limit"), defaultSearchLimit, maxSearchLimit)
	result, err := a.store.Search(store.SearchParams{
		ServiceIDs: serviceIDs,
		Query:      queryText,
		Limit:      limit,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to search logs: "+err.Error())
		return
	}
	jsonOK(w, logSearchResponse{
		Query:         queryText,
		Total:         result.Total,
		Items:         result.Entries,
		ServiceCounts: result.ServiceCounts,
	})
}

// fetchLogContext 处理 GET /api/logs/context，按目标日志时间拉取跨服务上下文。
func (a *App) fetchLogContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	if projectID == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	targetID, err := strconv.ParseInt(q.Get("id"), 10, 64)
	if err != nil || targetID <= 0 {
		jsonError(w, http.StatusBadRequest, "id is required")
		return
	}
	serviceIDs, ok := a.projectServiceIDs(projectID, q["service"])
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	beforeMS := parseBoundedInt(q.Get("before_ms"), defaultContextMS, maxContextMS)
	afterMS := parseBoundedInt(q.Get("after_ms"), defaultContextMS, maxContextMS)

	result, err := a.store.FetchContext(store.ContextParams{
		TargetID:   targetID,
		ServiceIDs: serviceIDs,
		Before:     time.Duration(beforeMS) * time.Millisecond,
		After:      time.Duration(afterMS) * time.Millisecond,
	})
	if errors.Is(err, store.ErrLogEntryNotFound) {
		jsonError(w, http.StatusNotFound, "log entry not found")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch log context: "+err.Error())
		return
	}
	jsonOK(w, logContextResponse{
		TargetID:       result.TargetID,
		AnchorTime:     result.AnchorTime,
		ItemsByService: result.ItemsByService,
	})
}

func (a *App) projectServiceIDs(projectID string, requested []string) ([]string, bool) {
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		return nil, false
	}

	allowed := map[string]bool{}
	for _, service := range project.Services {
		allowed[service.ID] = true
	}
	if len(requested) == 0 {
		ids := make([]string, 0, len(project.Services))
		for _, service := range project.Services {
			ids = append(ids, service.ID)
		}
		return ids, true
	}

	ids := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, id := range requested {
		// 忽略非本项目服务，保证搜索接口不能跨项目窥探日志。
		if !allowed[id] || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, true
}

func parseBoundedInt(raw string, fallback int, max int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
