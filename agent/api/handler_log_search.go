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
	"time"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/store"
)

const (
	defaultSearchLimit = 1000
	maxSearchLimit     = 5000
	defaultContextMS   = 30000
	maxContextMS       = 300000
	defaultPageLimit   = 200
	maxPageLimit       = 1000
)

type logSearchResponse struct {
	Query            string           `json:"query"`
	Total            int              `json:"total"`
	Items            []model.LogEntry `json:"items"`
	DeploymentCounts map[string]int   `json:"deployment_counts"`
	HasMore          bool             `json:"has_more"`
}

type logContextResponse struct {
	TargetID          int64                       `json:"target_id"`
	AnchorTime        time.Time                   `json:"anchor_time"`
	ItemsByDeployment map[string][]model.LogEntry `json:"items_by_deployment"`
}

type logContextPageResponse struct {
	DeploymentID string                     `json:"deployment_id"`
	Direction    store.ContextPageDirection `json:"direction"`
	Items        []model.LogEntry           `json:"items"`
	HasMore      bool                       `json:"has_more"`
}

// searchLogs 处理 GET /api/log-search，按项目服务集合搜索历史日志。
func (a *App) searchLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	queryText := searchQueryText(q)
	if queryText == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	var deploymentIDs []string
	if projectID != "" {
		var ok bool
		deploymentIDs, ok = a.projectDeploymentIDs(projectID, q["deployment"])
		if !ok {
			jsonError(w, http.StatusNotFound, "project not found")
			return
		}
	} else {
		// 无 project 时直接使用 deployment 列表,用于远端 collector 虚拟部署查询。
		deploymentIDs = q["deployment"]
		if len(deploymentIDs) == 0 {
			jsonError(w, http.StatusBadRequest, "project or deployment is required")
			return
		}
	}

	limit := parseBoundedInt(q.Get("limit"), defaultSearchLimit, maxSearchLimit)
	var cursorTime *time.Time
	var cursorID int64
	if rawCursorTime := q.Get("cursor_time"); rawCursorTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawCursorTime)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "cursor_time is invalid")
			return
		}
		cursorID, err = strconv.ParseInt(q.Get("cursor_id"), 10, 64)
		if err != nil || cursorID <= 0 {
			jsonError(w, http.StatusBadRequest, "cursor_id is required")
			return
		}
		cursorTime = &parsed
	} else if q.Get("cursor_id") != "" {
		jsonError(w, http.StatusBadRequest, "cursor_time is required")
		return
	}
	result, err := a.store.Search(store.SearchParams{
		DeploymentIDs: deploymentIDs,
		Query:         queryText,
		Limit:         limit,
		CursorTime:    cursorTime,
		CursorID:      cursorID,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to search logs: "+err.Error())
		return
	}
	jsonOK(w, logSearchResponse{
		Query:            queryText,
		Total:            result.Total,
		Items:            result.Entries,
		DeploymentCounts: result.DeploymentCounts,
		HasMore:          result.HasMore,
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
	deploymentIDs, ok := a.projectDeploymentIDs(projectID, q["deployment"])
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	beforeMS := parseBoundedInt(q.Get("before_ms"), defaultContextMS, maxContextMS)
	afterMS := parseBoundedInt(q.Get("after_ms"), defaultContextMS, maxContextMS)

	result, err := a.store.FetchContext(store.ContextParams{
		TargetID:      targetID,
		DeploymentIDs: deploymentIDs,
		Before:        time.Duration(beforeMS) * time.Millisecond,
		After:         time.Duration(afterMS) * time.Millisecond,
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
		TargetID:          result.TargetID,
		AnchorTime:        result.AnchorTime,
		ItemsByDeployment: result.ItemsByDeployment,
	})
}

// fetchLogContextPage 处理 GET /api/logs/context/page，按单服务时间游标继续读取上下文。
func (a *App) fetchLogContextPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	if projectID == "" {
		jsonError(w, http.StatusBadRequest, "project is required")
		return
	}
	deploymentID := q.Get("deployment")
	if deploymentID == "" {
		jsonError(w, http.StatusBadRequest, "deployment is required")
		return
	}
	direction := store.ContextPageDirection(q.Get("direction"))
	if direction != store.ContextPageBefore && direction != store.ContextPageAfter {
		jsonError(w, http.StatusBadRequest, "direction must be before or after")
		return
	}
	cursorTime, err := time.Parse(time.RFC3339Nano, q.Get("cursor_time"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "cursor_time is required")
		return
	}
	cursorID, err := strconv.ParseInt(q.Get("cursor_id"), 10, 64)
	if err != nil || cursorID < 0 {
		jsonError(w, http.StatusBadRequest, "cursor_id is required")
		return
	}
	deploymentIDs, ok := a.projectDeploymentIDs(projectID, []string{deploymentID})
	if !ok {
		jsonError(w, http.StatusNotFound, "project not found")
		return
	}
	if len(deploymentIDs) != 1 {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}

	limit := parseBoundedInt(q.Get("limit"), defaultPageLimit, maxPageLimit)
	result, err := a.store.FetchContextPage(store.ContextPageParams{
		DeploymentID: deploymentIDs[0],
		CursorTime:   cursorTime,
		CursorID:     cursorID,
		Direction:    direction,
		Limit:        limit,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "failed to fetch log context page: "+err.Error())
		return
	}
	if result.Entries == nil {
		result.Entries = []model.LogEntry{}
	}
	jsonOK(w, logContextPageResponse{
		DeploymentID: deploymentIDs[0],
		Direction:    direction,
		Items:        result.Entries,
		HasMore:      result.HasMore,
	})
}

// projectDeploymentIDs 把请求的 deployment 范围收敛到指定项目内，防止跨项目窥探日志。
//
// 参数：
//   - projectID: 目标项目 ID
//   - requested: 请求方指定的 deployment ID 列表，为空表示该项目全部
//
// 返回：
//   - 收敛后的 deployment ID 列表
//   - 项目是否存在
func (a *App) projectDeploymentIDs(projectID string, requested []string) ([]string, bool) {
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
		// 忽略不属于本项目的 deployment，保证搜索接口不能跨项目窥探日志。
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
