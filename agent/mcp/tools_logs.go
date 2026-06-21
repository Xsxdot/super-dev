// tools_logs.go 实现日志查询、搜索和上下文 MCP 工具。
//
// 职责：
//   - 拉取 deployment 最近日志并应用 MCP 侧过滤
//   - 调用 agent 搜索和上下文 API
//   - 控制返回日志数量，避免工具结果过大
//
// 边界：
//   - 不直接读取 SQLite
//   - 不订阅 WebSocket 实时流
//   - 不修改项目日志规则
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const defaultLogToolLimit = 200
const maxTailScanPages = 25
const defaultFollowDuration = 5 * time.Second
const maxFollowDuration = 30 * time.Second
const defaultFollowPollInterval = time.Second
const minFollowPollInterval = 100 * time.Millisecond

func (s *Server) tailLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		targetArgs
		Limit             int    `json:"limit"`
		RunID             string `json:"run_id"`
		Before            int64  `json:"before"`
		Level             string `json:"level"`
		Since             string `json:"since"`
		ApplyProjectRules *bool  `json:"apply_project_rules"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	target, errResp := resolveDeploymentTarget(projects, req.targetArgs)
	if errResp != nil {
		errResp = sanitizeResolveError(errResp)
		return toolError(errResp.Code, errResp.Message, errResp), nil
	}

	limit := logToolLimit(req.Limit)
	var since *time.Time
	filtersApplied := []string{}
	if req.Level != "" {
		filtersApplied = append(filtersApplied, "level")
	}
	if req.Since != "" {
		parsed, err := parseSince(req.Since)
		if err != nil {
			return toolError("invalid_arguments", "since must be RFC3339 time or duration", nil), nil
		}
		since = &parsed
		filtersApplied = append(filtersApplied, "since")
	}
	applyRules := true
	if req.ApplyProjectRules != nil {
		applyRules = *req.ApplyProjectRules
	}
	var rules []model.LogRule
	if applyRules {
		rules, err = s.client.ProjectRules(ctx, target.Project.ID)
		if err != nil {
			return clientToolError(err), nil
		}
		filtersApplied = append(filtersApplied, "project_rules")
	}

	entries := []model.LogEntry{}
	var next any
	scanPages := 0
	scanTruncated := false
	before := ""
	if req.Before > 0 {
		before = strconv.FormatInt(req.Before, 10)
	}
	scanFiltered := req.Level != "" || since != nil
	for {
		scanPages++
		query := url.Values{}
		// 筛选场景要多拿一页再筛，否则 limit=1 时很容易只扫到一条非目标日志。
		pageLimit := limit
		if scanFiltered && pageLimit < defaultLogToolLimit {
			pageLimit = defaultLogToolLimit
		}
		query.Set("limit", strconv.Itoa(pageLimit))
		if req.RunID != "" {
			query.Set("run", req.RunID)
		}
		if before != "" {
			query.Set("before", before)
		}
		resp, err := s.client.FetchDeploymentLogs(ctx, target.Deployment.ID, query)
		if err != nil {
			return clientToolError(err), nil
		}
		page := resp.Items
		next = resp.Next

		filtered := page
		if req.Level != "" {
			filtered = filterLogLevel(filtered, req.Level)
		}
		if since != nil {
			filtered = filterLogSince(filtered, *since)
		}
		if applyRules {
			filtered = applyLogRules(filtered, rules)
		}
		entries = append(entries, filtered...)
		if len(entries) >= limit {
			entries = entries[:limit]
			break
		}
		if !scanFiltered || resp.Next.ID == "" {
			break
		}
		if since != nil && pageStartsBefore(page, *since) {
			break
		}
		if scanPages >= maxTailScanPages {
			scanTruncated = true
			break
		}
		before = resp.Next.ID
	}

	entries, truncated := truncateLogEntries(entries, limit)
	data := map[string]any{
		"target":          sanitizeTarget(target),
		"entries":         entries,
		"count":           len(entries),
		"truncated":       truncated,
		"filters_applied": filtersApplied,
		"next":            next,
		"scan_pages":      scanPages,
		"scan_truncated":  scanTruncated,
	}
	return toolSuccess(
		fmt.Sprintf("%d log line(s)", len(entries)),
		data,
		nil,
		[]string{"Use search_logs for historical keyword search or get_log_context around a specific log ID."},
	), nil
}

func (s *Server) followLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		targetArgs
		Limit             int    `json:"limit"`
		Level             string `json:"level"`
		DurationMS        int    `json:"duration_ms"`
		PollIntervalMS    int    `json:"poll_interval_ms"`
		ApplyProjectRules *bool  `json:"apply_project_rules"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return clientToolError(err), nil
	}
	target, errResp := resolveDeploymentTarget(projects, req.targetArgs)
	if errResp != nil {
		errResp = sanitizeResolveError(errResp)
		return toolError(errResp.Code, errResp.Message, errResp), nil
	}

	limit := logToolLimit(req.Limit)
	duration := boundedFollowDuration(req.DurationMS)
	pollInterval := boundedFollowPollInterval(req.PollIntervalMS)
	applyRules := true
	if req.ApplyProjectRules != nil {
		applyRules = *req.ApplyProjectRules
	}
	var rules []model.LogRule
	if applyRules {
		rules, err = s.client.ProjectRules(ctx, target.Project.ID)
		if err != nil {
			return clientToolError(err), nil
		}
	}

	deadline := time.Now().Add(duration)
	entries := make([]model.LogEntry, 0, limit)
	seen := map[string]bool{}
	polls := 0
	for {
		polls++
		query := url.Values{}
		query.Set("limit", strconv.Itoa(limit))
		resp, err := s.client.FetchDeploymentLogs(ctx, target.Deployment.ID, query)
		if err != nil {
			return clientToolError(err), nil
		}
		page := resp.Items
		if req.Level != "" {
			page = filterLogLevel(page, req.Level)
		}
		if applyRules {
			page = applyLogRules(page, rules)
		}
		for _, entry := range page {
			key := logEntryIdentity(entry)
			if seen[key] {
				continue
			}
			seen[key] = true
			entries = append(entries, entry)
			if len(entries) >= limit {
				break
			}
		}
		if len(entries) >= limit || !time.Now().Before(deadline) {
			break
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return clientToolError(ctx.Err()), nil
		case <-timer.C:
		}
	}

	data := map[string]any{
		"target":           sanitizeTarget(target),
		"entries":          entries,
		"count":            len(entries),
		"duration_ms":      int(duration / time.Millisecond),
		"poll_interval_ms": int(pollInterval / time.Millisecond),
		"polls":            polls,
	}
	return toolSuccess(
		fmt.Sprintf("%d followed log line(s)", len(entries)),
		data,
		nil,
		[]string{"Use search_logs or summarize_error_window for historical errors outside the follow window."},
	), nil
}

func (s *Server) searchLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		Q            string `json:"q"`
		ProjectID    string `json:"project_id"`
		ProjectName  string `json:"project_name"`
		DeploymentID string `json:"deployment_id"`
		Limit        int    `json:"limit"`
		CursorTime   string `json:"cursor_time"`
		CursorID     int64  `json:"cursor_id"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	if strings.TrimSpace(req.Q) == "" {
		return toolError("invalid_arguments", "q is required", nil), nil
	}
	query := url.Values{}
	query.Set("q", req.Q)
	if req.DeploymentID != "" {
		query.Add("deployment", req.DeploymentID)
	}
	if req.DeploymentID != "" && req.ProjectID == "" && req.ProjectName == "" {
		projects, err := s.client.ListProjects(ctx)
		if err != nil {
			return clientToolError(err), nil
		}
		project, result, found := resolveProjectByDeployment(projects, req.DeploymentID)
		if !found && result.IsError {
			return result, nil
		}
		if found {
			query.Set("project", project.ID)
		}
	}
	if req.ProjectID != "" || req.ProjectName != "" {
		project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
		if !ok {
			return result, nil
		}
		query.Set("project", project.ID)
	}
	if query.Get("project") == "" && len(query["deployment"]) == 0 {
		return toolError("invalid_arguments", "project_id, project_name, or deployment_id is required", nil), nil
	}
	limit := logToolLimit(req.Limit)
	query.Set("limit", strconv.Itoa(limit))
	if req.CursorTime != "" {
		if req.CursorID <= 0 {
			return toolError("invalid_arguments", "cursor_id is required when cursor_time is set", nil), nil
		}
		query.Set("cursor_time", req.CursorTime)
		query.Set("cursor_id", strconv.FormatInt(req.CursorID, 10))
	} else if req.CursorID > 0 {
		return toolError("invalid_arguments", "cursor_time is required when cursor_id is set", nil), nil
	}
	resp, err := s.client.SearchLogs(ctx, query)
	if err != nil {
		return clientToolError(err), nil
	}
	entries, truncated := truncateLogEntries(resp.Items, limit)
	data := map[string]any{
		"query":             resp.Query,
		"entries":           entries,
		"count":             len(entries),
		"total":             resp.Total,
		"deployment_counts": resp.DeploymentCounts,
		"has_more":          resp.HasMore,
		"truncated":         truncated,
		"filters_applied":   []string{"query"},
	}
	return toolSuccess(
		fmt.Sprintf("%d matching log line(s)", len(entries)),
		data,
		nil,
		[]string{"Use get_log_context with a log ID to inspect surrounding service logs."},
	), nil
}

func resolveProjectByDeployment(projects []model.Project, deploymentID string) (model.Project, CallToolResult, bool) {
	matches := make([]model.Project, 0, 1)
	for _, project := range projects {
		for _, service := range project.Services {
			for _, deployment := range service.Deployments {
				if deployment.ID == deploymentID {
					matches = append(matches, sanitizeProject(project))
				}
			}
		}
	}
	if len(matches) == 0 {
		return model.Project{}, CallToolResult{}, false
	}
	if len(matches) > 1 {
		return model.Project{}, toolError("ambiguous_project", "deployment belongs to multiple projects; specify project_id", matches), false
	}
	return matches[0], CallToolResult{}, true
}

func (s *Server) getLogContextTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req struct {
		ID           int64  `json:"id"`
		ProjectID    string `json:"project_id"`
		ProjectName  string `json:"project_name"`
		DeploymentID string `json:"deployment_id"`
		BeforeMS     int    `json:"before_ms"`
		AfterMS      int    `json:"after_ms"`
		Limit        int    `json:"limit"`
	}
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	if req.ID <= 0 {
		return toolError("invalid_arguments", "id is required", nil), nil
	}
	query := url.Values{}
	query.Set("id", strconv.FormatInt(req.ID, 10))
	filtersApplied := []string{"id"}
	if req.ProjectID != "" || req.ProjectName != "" {
		project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
		if !ok {
			return result, nil
		}
		query.Set("project", project.ID)
		filtersApplied = append(filtersApplied, "project")
	}
	if req.DeploymentID != "" {
		query.Add("deployment", req.DeploymentID)
		filtersApplied = append(filtersApplied, "deployment")
	}
	if query.Get("project") == "" && req.DeploymentID == "" {
		return toolError("invalid_arguments", "project_id, project_name, or deployment_id is required", nil), nil
	}
	if req.BeforeMS > 0 {
		query.Set("before_ms", strconv.Itoa(req.BeforeMS))
	}
	if req.AfterMS > 0 {
		query.Set("after_ms", strconv.Itoa(req.AfterMS))
	}
	resp, err := s.client.FetchLogContext(ctx, query)
	if err != nil {
		return clientToolError(err), nil
	}
	limitedItems, count, truncated := truncateContextItems(resp.ItemsByDeployment, logToolLimit(req.Limit))
	resp.ItemsByDeployment = limitedItems
	data := map[string]any{
		"context":         resp,
		"count":           count,
		"truncated":       truncated,
		"filters_applied": filtersApplied,
	}
	return toolSuccess(
		fmt.Sprintf("%d context log line(s)", count),
		data,
		nil,
		[]string{"Use tail_logs on a specific deployment for newer logs."},
	), nil
}

func (s *Server) resolveProjectForLogs(ctx context.Context, projectID, projectName string) (model.Project, CallToolResult, bool) {
	if projectID == "" && projectName == "" {
		return model.Project{}, toolError("invalid_arguments", "project_id or project_name is required", nil), false
	}
	projects, err := s.client.ListProjects(ctx)
	if err != nil {
		return model.Project{}, clientToolError(err), false
	}
	matches := make([]model.Project, 0, 1)
	for _, project := range projects {
		if projectID != "" && project.ID != projectID {
			continue
		}
		if projectName != "" && project.Name != projectName {
			continue
		}
		matches = append(matches, sanitizeProject(project))
	}
	if len(matches) == 0 {
		return model.Project{}, toolError("project_not_found", "project not found", nil), false
	}
	if len(matches) > 1 {
		return model.Project{}, toolError("ambiguous_project", "multiple projects matched; specify project_id", matches), false
	}
	return matches[0], CallToolResult{}, true
}

func logToolLimit(limit int) int {
	if limit <= 0 || limit > defaultLogToolLimit {
		return defaultLogToolLimit
	}
	return limit
}

func filterLogLevel(entries []model.LogEntry, level string) []model.LogEntry {
	want := strings.ToUpper(level)
	out := make([]model.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.ToUpper(entry.Level) == want {
			out = append(out, entry)
		}
	}
	return out
}

func parseSince(raw string) (time.Time, error) {
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().Add(-d), nil
	}
	return time.Parse(time.RFC3339Nano, raw)
}

func filterLogSince(entries []model.LogEntry, since time.Time) []model.LogEntry {
	out := make([]model.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Timestamp.IsZero() || !entry.Timestamp.Before(since) {
			out = append(out, entry)
		}
	}
	return out
}

func pageStartsBefore(entries []model.LogEntry, since time.Time) bool {
	if len(entries) == 0 {
		return false
	}
	for _, entry := range entries {
		if !entry.Timestamp.IsZero() {
			return entry.Timestamp.Before(since)
		}
	}
	return false
}

func boundedFollowDuration(ms int) time.Duration {
	if ms <= 0 {
		return defaultFollowDuration
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxFollowDuration {
		return maxFollowDuration
	}
	return d
}

func boundedFollowPollInterval(ms int) time.Duration {
	if ms <= 0 {
		return defaultFollowPollInterval
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minFollowPollInterval {
		return minFollowPollInterval
	}
	return d
}

func logEntryIdentity(entry model.LogEntry) string {
	if entry.ID > 0 {
		return entry.DeploymentID + "#" + strconv.FormatInt(entry.ID, 10)
	}
	return entry.DeploymentID + "#" + entry.Timestamp.Format(time.RFC3339Nano) + "#" + entry.Message
}

func truncateContextItems(items map[string][]model.LogEntry, limit int) (map[string][]model.LogEntry, int, bool) {
	if items == nil {
		return nil, 0, false
	}
	out := make(map[string][]model.LogEntry, len(items))
	remaining := limit
	count := 0
	truncated := false
	deploymentIDs := make([]string, 0, len(items))
	for deploymentID := range items {
		deploymentIDs = append(deploymentIDs, deploymentID)
	}
	sort.Strings(deploymentIDs)
	for _, deploymentID := range deploymentIDs {
		entries := items[deploymentID]
		if remaining <= 0 {
			out[deploymentID] = []model.LogEntry{}
			if len(entries) > 0 {
				truncated = true
			}
			continue
		}
		if len(entries) > remaining {
			out[deploymentID] = entries[:remaining]
			count += remaining
			remaining = 0
			truncated = true
			continue
		}
		out[deploymentID] = entries
		count += len(entries)
		remaining -= len(entries)
	}
	return out, count, truncated
}

func applyLogRules(entries []model.LogEntry, rules []model.LogRule) []model.LogEntry {
	excludes := enabledRulesOfType(rules, model.RuleTypeExclude)
	includes := enabledRulesOfType(rules, model.RuleTypeInclude)
	out := make([]model.LogEntry, 0, len(entries))
	for _, entry := range entries {
		if matchesAnyRule(entry, excludes) {
			continue
		}
		if len(includes) > 0 && !matchesAnyRule(entry, includes) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func enabledRulesOfType(rules []model.LogRule, ruleType model.RuleType) []model.LogRule {
	out := make([]model.LogRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Enabled && rule.Type == ruleType {
			out = append(out, rule)
		}
	}
	return out
}

func matchesAnyRule(entry model.LogEntry, rules []model.LogRule) bool {
	for _, rule := range rules {
		if matchesLogRule(entry, rule) {
			return true
		}
	}
	return false
}

func matchesLogRule(entry model.LogEntry, rule model.LogRule) bool {
	if len(rule.Keywords) == 0 {
		return false
	}
	message := strings.ToLower(entry.Message)
	if rule.Logic == model.RuleLogicAND {
		for _, keyword := range rule.Keywords {
			if !strings.Contains(message, strings.ToLower(keyword)) {
				return false
			}
		}
		return true
	}
	for _, keyword := range rule.Keywords {
		if strings.Contains(message, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}
