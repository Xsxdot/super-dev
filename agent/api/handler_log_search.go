// Package api 提供项目维度的历史日志搜索 HTTP 接口。
//
// 职责：
//   - 解析日志搜索和上下文查询参数
//   - 在查询日志后端前收敛到项目服务范围
//   - 返回桌面端排障看板需要的原始日志数据
//
// 边界：
//   - 不应用项目日志过滤规则
//   - 不为 UI 时间栅格做格式化或分组
//   - 不暴露 Store 或远端后端内部实现细节，只返回 HTTP 响应 DTO
package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
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

type projectLogSearchScope struct {
	all    []string
	local  []string
	remote []string
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
	var scope projectLogSearchScope
	if projectID != "" {
		var ok bool
		scope, ok = a.projectLogSearchScope(projectID, q["deployment"])
		if !ok {
			jsonError(w, http.StatusNotFound, "project not found")
			return
		}
		deploymentIDs = scope.all
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
	cursorIDText := strings.TrimSpace(q.Get("cursor_id"))
	if rawCursorTime := q.Get("cursor_time"); rawCursorTime != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawCursorTime)
		if err != nil {
			jsonError(w, http.StatusBadRequest, "cursor_time is invalid")
			return
		}
		if cursorIDText == "" {
			jsonError(w, http.StatusBadRequest, "cursor_id is required")
			return
		}
		cursorTime = &parsed
	} else if q.Get("cursor_id") != "" {
		jsonError(w, http.StatusBadRequest, "cursor_time is required")
		return
	}

	if projectID != "" && len(scope.remote) > 0 {
		resp, err := a.searchProjectLogs(r.Context(), queryText, limit, cursorTime, cursorIDText, scope)
		if errors.Is(err, errInvalidCursorID) {
			jsonError(w, http.StatusBadRequest, "cursor_id is required")
			return
		}
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "failed to search logs: "+err.Error())
			return
		}
		jsonOK(w, resp)
		return
	}

	cursorID, err := parseStoreCursorID(cursorTime, cursorIDText)
	if errors.Is(err, errInvalidCursorID) {
		jsonError(w, http.StatusBadRequest, "cursor_id is required")
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

var errInvalidCursorID = errors.New("invalid cursor id")

func parseStoreCursorID(cursorTime *time.Time, cursorIDText string) (int64, error) {
	if cursorTime == nil {
		return 0, nil
	}
	cursorID, err := strconv.ParseInt(cursorIDText, 10, 64)
	if err != nil || cursorID <= 0 {
		return 0, errInvalidCursorID
	}
	return cursorID, nil
}

func (a *App) searchProjectLogs(ctx context.Context, queryText string, limit int, cursorTime *time.Time, cursorIDText string, scope projectLogSearchScope) (logSearchResponse, error) {
	items := []model.LogEntry{}
	counts := map[string]int{}
	total := 0
	hasMore := false

	if len(scope.local) > 0 {
		cursorID, err := parseStoreCursorID(cursorTime, cursorIDText)
		if err != nil {
			return logSearchResponse{}, err
		}
		result, err := a.store.Search(store.SearchParams{
			DeploymentIDs: scope.local,
			Query:         queryText,
			Limit:         limit,
			CursorTime:    cursorTime,
			CursorID:      cursorID,
		})
		if err != nil {
			return logSearchResponse{}, err
		}
		items = append(items, result.Entries...)
		for deploymentID, count := range result.DeploymentCounts {
			counts[deploymentID] += count
			total += count
		}
		hasMore = hasMore || result.HasMore
	}

	remote, err := a.searchDeploymentBackends(ctx, scope.remote, queryText, limit, cursorTime, cursorIDText)
	if err != nil {
		return logSearchResponse{}, err
	}
	items = append(items, remote.Items...)
	for deploymentID, count := range remote.DeploymentCounts {
		counts[deploymentID] += count
		total += count
	}
	hasMore = hasMore || remote.HasMore

	sort.Slice(items, func(i, j int) bool {
		if items[i].Timestamp.Equal(items[j].Timestamp) {
			return items[i].ID < items[j].ID
		}
		return items[i].Timestamp.Before(items[j].Timestamp)
	})
	if len(items) > limit {
		items = items[:limit]
		hasMore = true
	}

	return logSearchResponse{
		Query:            queryText,
		Total:            total,
		Items:            items,
		DeploymentCounts: counts,
		HasMore:          hasMore,
	}, nil
}

func (a *App) searchDeploymentBackends(ctx context.Context, deploymentIDs []string, queryText string, limit int, cursorTime *time.Time, cursorIDText string) (logSearchResponse, error) {
	items := []model.LogEntry{}
	counts := map[string]int{}
	hasMore := false

	cursor := logbackend.Cursor{ID: cursorIDText}
	if cursorTime != nil {
		cursor.Time = *cursorTime
	}
	for _, deploymentID := range deploymentIDs {
		backend, ok := a.lookupBackend(deploymentID)
		if !ok {
			return logSearchResponse{}, errors.New("log backend not found for deployment " + deploymentID)
		}
		entries, _, childHasMore, err := backend.Search(ctx, logbackend.SearchQuery{
			Text:          queryText,
			DeploymentIDs: []string{deploymentID},
			Limit:         limit,
			Cursor:        cursor,
		})
		if err != nil {
			return logSearchResponse{}, err
		}
		for i := range entries {
			// 远端 agent 可能返回 collector 内部归属 ID；项目搜索的展示维度必须稳定为中心侧 deployment ID。
			entries[i].DeploymentID = deploymentID
		}
		items = append(items, entries...)
		counts[deploymentID] += len(entries)
		hasMore = hasMore || childHasMore
	}
	return logSearchResponse{
		Query:            queryText,
		Total:            len(items),
		Items:            items,
		DeploymentCounts: counts,
		HasMore:          hasMore,
	}, nil
}

// fetchLogContext 处理 GET /api/logs/context，按目标日志时间拉取跨服务上下文。
func (a *App) fetchLogContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectID := q.Get("project")
	targetID, err := strconv.ParseInt(q.Get("id"), 10, 64)
	if err != nil || targetID <= 0 {
		jsonError(w, http.StatusBadRequest, "id is required")
		return
	}
	deploymentIDs, status, ok := a.logContextDeploymentIDs(projectID, q["deployment"])
	if !ok {
		if status == http.StatusBadRequest {
			jsonError(w, status, "project or deployment is required")
			return
		}
		jsonError(w, status, "project not found")
		return
	}
	targetDeploymentID := strings.TrimSpace(q.Get("target_deployment"))
	if targetDeploymentID != "" && !containsString(deploymentIDs, targetDeploymentID) {
		jsonError(w, http.StatusNotFound, "deployment not found")
		return
	}
	beforeMS := parseBoundedInt(q.Get("before_ms"), defaultContextMS, maxContextMS)
	afterMS := parseBoundedInt(q.Get("after_ms"), defaultContextMS, maxContextMS)
	before := time.Duration(beforeMS) * time.Millisecond
	after := time.Duration(afterMS) * time.Millisecond

	result, err := a.store.FetchContext(store.ContextParams{
		TargetID:           targetID,
		TargetDeploymentID: targetDeploymentID,
		DeploymentIDs:      deploymentIDs,
		Before:             before,
		After:              after,
	})
	if errors.Is(err, store.ErrLogEntryNotFound) {
		backendContext, found, backendErr := a.fetchBackendLogContextForScope(r.Context(), deploymentIDs, targetDeploymentID, targetID, before, after)
		if backendErr != nil {
			jsonError(w, http.StatusInternalServerError, "failed to fetch backend log context: "+backendErr.Error())
			return
		}
		if found {
			jsonOK(w, backendContext)
			return
		}
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

func (a *App) fetchBackendLogContextForScope(ctx context.Context, deploymentIDs []string, targetDeploymentID string, targetID int64, before time.Duration, after time.Duration) (logContextResponse, bool, error) {
	targetDeploymentIDs := deploymentIDs
	if targetDeploymentID != "" {
		// The anchor deployment is explicit; the broader deployment list is only the peer context scope.
		targetDeploymentIDs = []string{targetDeploymentID}
	}
	for _, deploymentID := range targetDeploymentIDs {
		backendResult, err := a.fetchBackendLogContext(ctx, deploymentID, targetID, before, after)
		if errors.Is(err, logbackend.ErrLogContextNotFound) {
			continue
		}
		if err != nil {
			return logContextResponse{}, false, fmt.Errorf("%s: %w", deploymentID, err)
		}
		itemsByDeployment, err := a.contextItemsAroundBackendAnchor(backendResult.AnchorTime, deploymentIDs, before, after)
		if err != nil {
			return logContextResponse{}, false, err
		}
		itemsByDeployment[deploymentID] = backendResult.Items
		return logContextResponse{
			TargetID:          backendResult.TargetID,
			AnchorTime:        backendResult.AnchorTime,
			ItemsByDeployment: itemsByDeployment,
		}, true, nil
	}
	return logContextResponse{}, false, nil
}

func (a *App) contextItemsAroundBackendAnchor(anchorTime time.Time, deploymentIDs []string, before time.Duration, after time.Duration) (map[string][]model.LogEntry, error) {
	itemsByDeployment := make(map[string][]model.LogEntry, len(deploymentIDs))
	for _, deploymentID := range deploymentIDs {
		itemsByDeployment[deploymentID] = []model.LogEntry{}
	}
	storeContext, err := a.store.FetchContextAt(store.ContextAtParams{
		AnchorTime:    anchorTime,
		DeploymentIDs: deploymentIDs,
		Before:        before,
		After:         after,
	})
	if errors.Is(err, store.ErrLogEntryNotFound) {
		return itemsByDeployment, nil
	}
	if err != nil {
		return nil, err
	}
	for deploymentID, entries := range storeContext.ItemsByDeployment {
		itemsByDeployment[deploymentID] = entries
	}
	return itemsByDeployment, nil
}

func (a *App) logContextDeploymentIDs(projectID string, requested []string) ([]string, int, bool) {
	if projectID != "" {
		deploymentIDs, ok := a.projectDeploymentIDs(projectID, requested)
		if !ok {
			return nil, http.StatusNotFound, false
		}
		return deploymentIDs, http.StatusOK, true
	}
	if len(requested) == 0 {
		return nil, http.StatusBadRequest, false
	}
	return requested, http.StatusOK, true
}

func (a *App) fetchBackendLogContext(ctx context.Context, deploymentID string, targetID int64, before time.Duration, after time.Duration) (logbackend.ContextResult, error) {
	backend, ok := a.lookupBackend(deploymentID)
	if !ok {
		return logbackend.ContextResult{}, logbackend.ErrLogContextNotFound
	}
	contextBackend, ok := backend.(logbackend.ContextReader)
	if !ok {
		return logbackend.ContextResult{}, logbackend.ErrLogContextNotFound
	}
	result, err := contextBackend.Context(ctx, logbackend.ContextQuery{
		TargetID:     targetID,
		DeploymentID: deploymentID,
		Before:       before,
		After:        after,
	})
	if err != nil {
		return logbackend.ContextResult{}, err
	}
	for i := range result.Items {
		// 远端 collector 返回的是远端内部 collector ID；中心侧上下文必须稳定显示为项目 deployment ID。
		result.Items[i].DeploymentID = deploymentID
	}
	return result, nil
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
	result, err := a.fetchLogContextPageForDeployment(r.Context(), deploymentIDs[0], cursorTime, cursorID, direction, limit)
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

func (a *App) fetchLogContextPageForDeployment(ctx context.Context, deploymentID string, cursorTime time.Time, cursorID int64, direction store.ContextPageDirection, limit int) (store.ContextPageResult, error) {
	if backend, ok := a.lookupBackend(deploymentID); ok {
		if pageBackend, ok := backend.(logbackend.ContextPageReader); ok {
			result, err := pageBackend.ContextPage(ctx, logbackend.ContextPageQuery{
				DeploymentID: deploymentID,
				Cursor:       logbackend.Cursor{Time: cursorTime, ID: strconv.FormatInt(cursorID, 10)},
				Direction:    logbackend.ContextPageDirection(direction),
				Limit:        limit,
			})
			if err != nil {
				return store.ContextPageResult{}, err
			}
			for i := range result.Entries {
				// Backends may use collector-internal IDs; the desktop UI groups by the center-side deployment ID.
				result.Entries[i].DeploymentID = deploymentID
			}
			return store.ContextPageResult{Entries: result.Entries, HasMore: result.HasMore}, nil
		}
	}
	return a.store.FetchContextPage(store.ContextPageParams{
		DeploymentID: deploymentID,
		CursorTime:   cursorTime,
		CursorID:     cursorID,
		Direction:    direction,
		Limit:        limit,
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
	scope, ok := a.projectLogSearchScope(projectID, requested)
	return scope.all, ok
}

func (a *App) projectLogSearchScope(projectID string, requested []string) (projectLogSearchScope, bool) {
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		return projectLogSearchScope{}, false
	}

	aliases := map[string][]string{}
	remoteByID := map[string]bool{}
	allIDs := make([]string, 0, len(project.Services))
	for _, service := range project.Services {
		if len(service.Deployments) == 0 {
			// 兼容旧配置：deployment 模型落地前，日志直接以 service.ID 归属。
			aliases[service.ID] = []string{service.ID}
			allIDs = append(allIDs, service.ID)
			continue
		}

		serviceDeploymentIDs := make([]string, 0, len(service.Deployments))
		for _, deployment := range service.Deployments {
			aliases[deployment.ID] = []string{deployment.ID}
			remoteByID[deployment.ID] = deployment.Location == model.LocationRemote
			serviceDeploymentIDs = append(serviceDeploymentIDs, deployment.ID)
			allIDs = append(allIDs, deployment.ID)
		}
		// 允许旧调用方用 service.ID 请求，内部展开为该服务下全部 deployment。
		aliases[service.ID] = serviceDeploymentIDs
	}
	if len(requested) == 0 {
		return splitLogSearchScope(allIDs, remoteByID), true
	}

	ids := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, id := range requested {
		// 忽略不属于本项目的 deployment，保证搜索接口不能跨项目窥探日志。
		for _, deploymentID := range aliases[id] {
			if seen[deploymentID] {
				continue
			}
			seen[deploymentID] = true
			ids = append(ids, deploymentID)
		}
	}
	return splitLogSearchScope(ids, remoteByID), true
}

func splitLogSearchScope(ids []string, remoteByID map[string]bool) projectLogSearchScope {
	scope := projectLogSearchScope{all: ids}
	for _, id := range ids {
		if remoteByID[id] {
			scope.remote = append(scope.remote, id)
		} else {
			scope.local = append(scope.local, id)
		}
	}
	return scope
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
