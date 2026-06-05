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
		return toolError(errResp.Code, errResp.Message, errResp), nil
	}

	limit := logToolLimit(req.Limit)
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if req.RunID != "" {
		query.Set("run", req.RunID)
	}
	if req.Before > 0 {
		query.Set("before", strconv.FormatInt(req.Before, 10))
	}
	resp, err := s.client.FetchDeploymentLogs(ctx, target.Deployment.ID, query)
	if err != nil {
		return clientToolError(err), nil
	}

	entries := resp.Items
	filtersApplied := []string{}
	if req.Level != "" {
		entries = filterLogLevel(entries, req.Level)
		filtersApplied = append(filtersApplied, "level")
	}
	if req.Since != "" {
		since, err := parseSince(req.Since)
		if err != nil {
			return toolError("invalid_arguments", "since must be RFC3339 time or duration", nil), nil
		}
		entries = filterLogSince(entries, since)
		filtersApplied = append(filtersApplied, "since")
	}
	applyRules := true
	if req.ApplyProjectRules != nil {
		applyRules = *req.ApplyProjectRules
	}
	if applyRules {
		rules, err := s.client.ProjectRules(ctx, target.Project.ID)
		if err != nil {
			return clientToolError(err), nil
		}
		entries = applyLogRules(entries, rules)
		filtersApplied = append(filtersApplied, "project_rules")
	}
	entries, truncated := truncateLogEntries(entries, limit)
	data := map[string]any{
		"target":          sanitizeTarget(target),
		"entries":         entries,
		"count":           len(entries),
		"truncated":       truncated,
		"filters_applied": filtersApplied,
		"next":            resp.Next,
	}
	return toolSuccess(
		fmt.Sprintf("%d log line(s)", len(entries)),
		data,
		nil,
		[]string{"Use search_logs for historical keyword search or get_log_context around a specific log ID."},
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
	project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return result, nil
	}
	query := url.Values{}
	query.Set("project", project.ID)
	query.Set("id", strconv.FormatInt(req.ID, 10))
	if req.DeploymentID != "" {
		query.Add("deployment", req.DeploymentID)
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
		"filters_applied": []string{"project", "id"},
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
		matches = append(matches, project)
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
