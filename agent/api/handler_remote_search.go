// handler_remote_search.go 实现跨节点日志搜索:
// fan-out 到多个远端 /api/log-search,归并排序后返回。
//
// 职责：
//   - 解析参数:log_source_id, group, q/query, limit, cursor, from, to
//   - 根据 LogSource + group 选出 Host 子集
//   - 并发为每个 Host 通过隧道 BaseURL 调 /api/log-search
//   - 单 host 3 秒超时或错误时加入 hosts_failed,不中断其他 Host
//   - 用 MergeStreams 归并,返回 entries + next_cursor + hosts_failed
//
// 边界：
//   - 不缓存远端结果,每次请求都重新拉
//   - 单 host 错误降级而非整体失败
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
)

const (
	remoteSearchTimeout    = 3 * time.Second
	remoteSearchDefaultLim = 200
	remoteSearchMaxLim     = 1000
)

type remoteSearchResponse struct {
	Entries     []MergeItem    `json:"entries"`
	TotalByHost map[string]int `json:"total_by_host"`
	HostsFailed []string       `json:"hosts_failed"`
	NextCursor  string         `json:"next_cursor"`
	HasMore     bool           `json:"has_more"`
}

type remoteProjectSearchResponse struct {
	Query          string                       `json:"query"`
	Status         string                       `json:"status"`
	ServiceColumns []remoteProjectServiceColumn `json:"service_columns"`
	Failures       []remoteProjectFailure       `json:"failures"`
	NextCursor     string                       `json:"next_cursor"`
	HasMore        bool                         `json:"has_more"`
}

type remoteProjectServiceColumn struct {
	ServiceID   string                   `json:"service_id"`
	ServiceName string                   `json:"service_name"`
	Status      string                   `json:"status"`
	ResultCount int                      `json:"result_count"`
	NodeCount   int                      `json:"node_count"`
	Nodes       []remoteProjectNode      `json:"nodes"`
	Entries     []remoteProjectEntryItem `json:"entries"`
}

type remoteProjectNode struct {
	HostID   string `json:"host_id"`
	HostName string `json:"host_name"`
	Status   string `json:"status"`
	Count    int    `json:"count"`
	Error    string `json:"error,omitempty"`
}

type remoteProjectEntryItem struct {
	Key         string    `json:"key"`
	ID          int64     `json:"id"`
	ServiceID   string    `json:"service_id"`
	LogSourceID string    `json:"log_source_id"`
	HostID      string    `json:"host_id"`
	HostName    string    `json:"host_name"`
	RunID       string    `json:"run_id"`
	Timestamp   time.Time `json:"timestamp"`
	Level       string    `json:"level"`
	Message     string    `json:"message"`
	Stream      string    `json:"stream"`
}

type remoteProjectFailure struct {
	ServiceID string `json:"service_id"`
	HostID    string `json:"host_id"`
	Kind      string `json:"kind"`
	Message   string `json:"message,omitempty"`
}

type remoteProjectTarget struct {
	serviceID   string
	serviceName string
	logSource   model.LogSource
	host        model.Host
}

type remoteProjectTargetResult struct {
	target  remoteProjectTarget
	cursor  HostCursor
	entries []model.LogEntry
	err     error
}

// remoteLogSearch 处理 GET /api/remote-log-search。
func (a *App) remoteLogSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if projectID := q.Get("project_id"); projectID != "" {
		a.remoteProjectLogSearch(w, r, projectID)
		return
	}

	logSourceID := q.Get("log_source_id")
	group := q.Get("group")
	query := searchQueryText(q)
	if logSourceID == "" || group == "" {
		jsonError(w, http.StatusBadRequest, "log_source_id and group are required")
		return
	}

	limit := remoteSearchLimit(q)

	cursor, err := DecodeMergeCursor(q.Get("cursor"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var logSource model.LogSource
	for _, item := range logSources {
		if item.ID == logSourceID {
			logSource = item
			break
		}
	}
	if logSource.ID == "" {
		jsonError(w, http.StatusNotFound, "log source not found")
		return
	}

	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	hostByID := make(map[string]model.Host, len(hosts))
	for _, h := range hosts {
		hostByID[h.ID] = h
	}
	relevant := selectHostsForGroup(logSource.HostIDs, group, hostByID)
	if len(relevant) == 0 {
		jsonOK(w, remoteSearchResponse{
			Entries:     []MergeItem{},
			TotalByHost: map[string]int{},
			HostsFailed: []string{},
			NextCursor:  MergeCursor{}.Encode(),
		})
		return
	}

	collectorID := collector.CollectorID(logSource.Name, logSource.Type)
	streams := map[string][]model.LogEntry{}
	totals := map[string]int{}
	failed := []string{}
	oldCursorByHost := map[string]HostCursor{}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, h := range relevant {
		hostCursor := cursor[h.ID]
		oldCursorByHost[h.ID] = hostCursor
		if hostCursor.Exhausted {
			streams[h.ID] = []model.LogEntry{}
			totals[h.ID] = 0
			continue
		}
		wg.Add(1)
		go func(host model.Host, hc HostCursor) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(r.Context(), remoteSearchTimeout)
			defer cancel()

			entries, total, err := a.fetchOneHost(ctx, host.ID, collectorID, query, limit, hc, q.Get("from"), q.Get("to"))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed = append(failed, host.ID)
				return
			}
			streams[host.ID] = entries
			totals[host.ID] = total
		}(h, hostCursor)
	}
	wg.Wait()
	sort.Strings(failed)

	merged := MergeStreams(streams, limit)
	nextCursor, hasMore := buildNextMergeCursor(relevant, oldCursorByHost, streams, failed, merged, limit)
	jsonOK(w, remoteSearchResponse{
		Entries:     merged,
		TotalByHost: totals,
		HostsFailed: failed,
		NextCursor:  nextCursor.Encode(),
		HasMore:     hasMore,
	})
}

// selectHostsForGroup 选择 LogSource 在指定 group 下关联的 Host 子集。
//
// group="all" 返回所有 LogSource.HostIDs;
// 其他 group 只返回 Tags 包含该 group 的 Host。
func selectHostsForGroup(hostIDs []string, group string, hostByID map[string]model.Host) []model.Host {
	out := []model.Host{}
	for _, hostID := range hostIDs {
		h, ok := hostByID[hostID]
		if !ok {
			continue
		}
		if group == "all" {
			out = append(out, h)
			continue
		}
		for _, tag := range h.Tags {
			if tag == group {
				out = append(out, h)
				break
			}
		}
	}
	return out
}

func searchQueryText(values url.Values) string {
	query := strings.TrimSpace(values.Get("q"))
	if query != "" {
		return query
	}
	return strings.TrimSpace(values.Get("query"))
}

func remoteSearchLimit(values url.Values) int {
	limit := remoteSearchDefaultLim
	if v := values.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > remoteSearchMaxLim {
		return remoteSearchMaxLim
	}
	return limit
}

// buildNextMergeCursor 根据本次已实际返回的 merged entries 推进每个 Host 的游标。
//
// 游标只能推进到已经返回给前端的最后一条日志;如果直接推进到远端本批最后一条,
// 当某个 Host 的后续日志尚未进入当前合并页时会被跳过。
func buildNextMergeCursor(hosts []model.Host, oldCursors map[string]HostCursor, streams map[string][]model.LogEntry, failed []string, merged []MergeItem, limit int) (MergeCursor, bool) {
	failedSet := make(map[string]bool, len(failed))
	for _, hostID := range failed {
		failedSet[hostID] = true
	}
	emittedCount := map[string]int{}
	lastEmitted := map[string]model.LogEntry{}
	for _, item := range merged {
		emittedCount[item.HostID]++
		lastEmitted[item.HostID] = item.Entry
	}

	next := MergeCursor{}
	hasMore := len(failed) > 0
	for _, host := range hosts {
		if failedSet[host.ID] {
			continue
		}
		oldCursor := oldCursors[host.ID]
		if oldCursor.Exhausted {
			next[host.ID] = oldCursor
			continue
		}
		entries := streams[host.ID]
		count := emittedCount[host.ID]
		switch {
		case count == 0 && len(entries) == 0:
			next[host.ID] = HostCursor{Exhausted: true}
		case count == 0:
			next[host.ID] = oldCursor
			hasMore = true
		case count == len(entries) && len(entries) < limit:
			next[host.ID] = HostCursor{Exhausted: true}
		default:
			last := lastEmitted[host.ID]
			next[host.ID] = HostCursor{CursorTime: last.Timestamp, CursorID: last.ID}
			hasMore = true
		}
	}
	return next, hasMore
}

// fetchOneHost 通过隧道调一个远端的 /api/log-search。
//
// 返回：
//   - 该 Host 返回的本批日志
//   - 该 Host 对当前查询的 total
//   - 无隧道、HTTP 失败或解析失败时返回错误
func (a *App) fetchOneHost(ctx context.Context, hostID, serviceID, query string, limit int, hc HostCursor, from, to string) ([]model.LogEntry, int, error) {
	base, err := a.tunnelResolver.BaseURL(hostID)
	if err != nil {
		return nil, 0, err
	}
	u, err := url.Parse(base + "/api/log-search")
	if err != nil {
		return nil, 0, err
	}
	q := u.Query()
	q.Set("deployment", serviceID)
	q.Set("q", query)
	q.Set("query", query)
	q.Set("limit", strconv.Itoa(limit))
	if !hc.CursorTime.IsZero() {
		q.Set("cursor_time", hc.CursorTime.Format(time.RFC3339Nano))
		q.Set("cursor_id", strconv.FormatInt(hc.CursorID, 10))
	}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, 0, errors.New("remote returned non-2xx")
	}
	var payload struct {
		Items []model.LogEntry `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, 0, err
	}
	if payload.Items == nil {
		payload.Items = []model.LogEntry{}
	}
	return payload.Items, payload.Total, nil
}

func (a *App) remoteProjectLogSearch(w http.ResponseWriter, r *http.Request, projectID string) {
	q := r.URL.Query()
	group := q.Get("group")
	query := searchQueryText(q)
	if group == "" {
		jsonError(w, http.StatusBadRequest, "group is required")
		return
	}
	if query == "" {
		jsonError(w, http.StatusBadRequest, "query is required")
		return
	}

	limit := remoteSearchLimit(q)
	cursor, err := DecodeMergeCursor(q.Get("cursor"))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	targets, serviceOrder, err := a.remoteProjectSearchTargets(projectID, group, q["service_id"], q["host_id"])
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(serviceOrder) == 0 {
		jsonOK(w, remoteProjectSearchResponse{Query: query, Status: "failed", ServiceColumns: []remoteProjectServiceColumn{}, Failures: []remoteProjectFailure{}, NextCursor: MergeCursor{}.Encode()})
		return
	}

	results := make([]remoteProjectTargetResult, 0, len(targets))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(t remoteProjectTarget) {
			defer wg.Done()
			hostCursor := cursor[remoteProjectCursorKey(t)]
			ctx, cancel := context.WithTimeout(r.Context(), remoteSearchTimeout)
			defer cancel()
			collectorID := collector.CollectorID(t.logSource.Name, t.logSource.Type)
			entries, _, err := a.fetchOneHost(ctx, t.host.ID, collectorID, query, limit, hostCursor, q.Get("from"), q.Get("to"))
			mu.Lock()
			results = append(results, remoteProjectTargetResult{target: t, cursor: hostCursor, entries: entries, err: err})
			mu.Unlock()
		}(target)
	}
	wg.Wait()

	jsonOK(w, buildRemoteProjectSearchResponse(query, serviceOrder, results, limit))
}

func buildRemoteProjectSearchResponse(query string, serviceOrder []string, results []remoteProjectTargetResult, limit int) remoteProjectSearchResponse {
	columnsByService := map[string]*remoteProjectServiceColumn{}
	cursorHosts := make([]model.Host, 0, len(results))
	oldCursorByTarget := map[string]HostCursor{}
	streamsByTarget := map[string][]model.LogEntry{}
	failedCursorKeys := []string{}
	successfulTargets := 0
	failures := []remoteProjectFailure{}
	for _, serviceID := range serviceOrder {
		columnsByService[serviceID] = &remoteProjectServiceColumn{ServiceID: serviceID, Status: "failed", Nodes: []remoteProjectNode{}, Entries: []remoteProjectEntryItem{}}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].target.serviceID == results[j].target.serviceID {
			return results[i].target.host.ID < results[j].target.host.ID
		}
		return serviceIndex(serviceOrder, results[i].target.serviceID) < serviceIndex(serviceOrder, results[j].target.serviceID)
	})
	for _, result := range results {
		cursorKey := remoteProjectCursorKey(result.target)
		cursorHosts = append(cursorHosts, model.Host{ID: cursorKey})
		oldCursorByTarget[cursorKey] = result.cursor
		column := columnsByService[result.target.serviceID]
		column.ServiceName = result.target.serviceName
		node := remoteProjectNode{HostID: result.target.host.ID, HostName: result.target.host.Name}
		if result.err != nil {
			kind := remoteProjectErrorKind(result.err)
			node.Status = kind
			node.Error = result.err.Error()
			failures = append(failures, remoteProjectFailure{ServiceID: result.target.serviceID, HostID: result.target.host.ID, Kind: kind, Message: result.err.Error()})
			failedCursorKeys = append(failedCursorKeys, cursorKey)
			column.Nodes = append(column.Nodes, node)
			continue
		}
		streamsByTarget[cursorKey] = result.entries
		successfulTargets++
		node.Status = "success"
		node.Count = len(result.entries)
		column.Nodes = append(column.Nodes, node)
		for _, entry := range result.entries {
			column.Entries = append(column.Entries, remoteProjectEntry(result.target, entry))
		}
	}

	columns := make([]remoteProjectServiceColumn, 0, len(serviceOrder))
	for _, serviceID := range serviceOrder {
		column := columnsByService[serviceID]
		sort.Slice(column.Entries, func(i, j int) bool {
			if column.Entries[i].Timestamp.Equal(column.Entries[j].Timestamp) {
				return column.Entries[i].ID < column.Entries[j].ID
			}
			return column.Entries[i].Timestamp.Before(column.Entries[j].Timestamp)
		})
		if len(column.Entries) > limit {
			column.Entries = column.Entries[:limit]
		}
		column.ResultCount = len(column.Entries)
		column.NodeCount = len(column.Nodes)
		column.Status = remoteProjectColumnStatus(column.Nodes)
		columns = append(columns, *column)
	}

	returnedItems := make([]MergeItem, 0)
	for _, column := range columns {
		for _, entry := range column.Entries {
			cursorKey := remoteProjectEntryCursorKey(entry)
			returnedItems = append(returnedItems, MergeItem{
				HostID: cursorKey,
				Entry: model.LogEntry{
					ID:           entry.ID,
					DeploymentID: entry.ServiceID,
					RunID:        entry.RunID,
					Timestamp:    entry.Timestamp,
					Level:        entry.Level,
					Message:      entry.Message,
					Stream:       entry.Stream,
				},
			})
		}
	}
	nextCursor, hasMore := buildNextMergeCursor(cursorHosts, oldCursorByTarget, streamsByTarget, failedCursorKeys, returnedItems, limit)

	return remoteProjectSearchResponse{
		Query:          query,
		Status:         remoteProjectSearchStatus(successfulTargets, failures),
		ServiceColumns: columns,
		Failures:       failures,
		NextCursor:     nextCursor.Encode(),
		HasMore:        hasMore,
	}
}

func remoteProjectCursorKey(target remoteProjectTarget) string {
	return target.serviceID + "/" + target.logSource.ID + "/" + target.host.ID
}

func remoteProjectEntryCursorKey(entry remoteProjectEntryItem) string {
	return entry.ServiceID + "/" + entry.LogSourceID + "/" + entry.HostID
}

func remoteProjectErrorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		return "timeout"
	}
	return "failed"
}

func remoteProjectEntry(target remoteProjectTarget, entry model.LogEntry) remoteProjectEntryItem {
	return remoteProjectEntryItem{
		Key:         target.serviceID + "/" + target.logSource.ID + "/" + target.host.ID + ":" + strconv.FormatInt(entry.ID, 10),
		ID:          entry.ID,
		ServiceID:   target.serviceID,
		LogSourceID: target.logSource.ID,
		HostID:      target.host.ID,
		HostName:    target.host.Name,
		RunID:       entry.RunID,
		Timestamp:   entry.Timestamp,
		Level:       entry.Level,
		Message:     entry.Message,
		Stream:      entry.Stream,
	}
}

func remoteProjectColumnStatus(nodes []remoteProjectNode) string {
	successNodes := 0
	timeoutNodes := 0
	for _, node := range nodes {
		switch node.Status {
		case "success":
			successNodes++
		case "timeout":
			timeoutNodes++
		}
	}
	switch {
	case successNodes == 0 && timeoutNodes == len(nodes):
		return "timeout"
	case successNodes == 0:
		return "failed"
	case successNodes < len(nodes):
		return "partial_failed"
	default:
		return "success"
	}
}

func remoteProjectSearchStatus(successfulTargets int, failures []remoteProjectFailure) string {
	if successfulTargets == 0 {
		return "failed"
	}
	if len(failures) > 0 {
		return "partial_failed"
	}
	return "success"
}

func (a *App) remoteProjectSearchTargets(projectID, group string, serviceFilter, hostFilter []string) ([]remoteProjectTarget, []string, error) {
	a.mu.RLock()
	project, ok := a.findProject(projectID)
	a.mu.RUnlock()
	if !ok {
		return nil, nil, nil
	}
	serviceNames := map[string]string{}
	for _, svc := range project.Services {
		serviceNames[svc.ID] = svc.Name
	}
	serviceAllowed := stringSet(serviceFilter)
	hostAllowed := stringSet(hostFilter)

	logSources, err := a.remoteStore.ListLogSources()
	if err != nil {
		return nil, nil, err
	}
	hosts, err := a.remoteStore.ListHosts()
	if err != nil {
		return nil, nil, err
	}
	hostByID := make(map[string]model.Host, len(hosts))
	for _, h := range hosts {
		hostByID[h.ID] = h
	}

	targets := []remoteProjectTarget{}
	serviceOrder := []string{}
	seenService := map[string]bool{}
	for _, ls := range logSources {
		if ls.ProjectID != projectID || ls.ServiceID == "" {
			continue
		}
		if len(serviceAllowed) > 0 && !serviceAllowed[ls.ServiceID] {
			continue
		}
		if _, ok := serviceNames[ls.ServiceID]; !ok {
			continue
		}
		if !seenService[ls.ServiceID] {
			seenService[ls.ServiceID] = true
			serviceOrder = append(serviceOrder, ls.ServiceID)
		}
		for _, host := range selectHostsForGroup(ls.HostIDs, group, hostByID) {
			if len(hostAllowed) > 0 && !hostAllowed[host.ID] {
				continue
			}
			targets = append(targets, remoteProjectTarget{serviceID: ls.ServiceID, serviceName: serviceNames[ls.ServiceID], logSource: ls, host: host})
		}
	}
	return targets, serviceOrder, nil
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out[value] = true
		}
	}
	return out
}

func serviceIndex(order []string, serviceID string) int {
	for i, item := range order {
		if item == serviceID {
			return i
		}
	}
	return len(order)
}
