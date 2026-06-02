// tools_log_analysis.go 实现日志分析 MCP 工具。
//
// 职责：
//   - 通过 agent 日志搜索 API 收集日志
//   - 调用确定性分析 helper 生成 timeline、signals 和 evidence
//   - 返回下一步排障建议
//
// 边界：
//   - 不断言根因
//   - 不修改配置或运行态
//   - 不触发流水线
package mcp

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/superdev/agent/model"
)

var errorWindowSearchTerms = []string{"error", "fatal", "panic", "timeout", "refused", "exhausted", "failed"}

type traceLogAnalysisArgs struct {
	ProjectID   string `json:"project_id"`
	ProjectName string `json:"project_name"`
	TraceID     string `json:"trace_id"`
	RequestID   string `json:"request_id"`
	Limit       int    `json:"limit"`
	BeforeMS    int    `json:"before_ms"`
	AfterMS     int    `json:"after_ms"`
}

type errorWindowArgs struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	DeploymentID string `json:"deployment_id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Since        string `json:"since"`
	Limit        int    `json:"limit"`
}

type appendLogAnalysisArgs struct {
	traceLogAnalysisArgs
	SessionID    string `json:"session_id"`
	AnalysisType string `json:"analysis_type"`
	DeploymentID string `json:"deployment_id"`
	From         string `json:"from"`
	To           string `json:"to"`
	Since        string `json:"since"`
}

func (s *Server) analyzeTraceLogsTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req traceLogAnalysisArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	analysis, result, ok := s.collectTraceLogAnalysis(ctx, req)
	if !ok {
		return result, nil
	}
	return toolSuccess("trace log evidence collected", traceAnalysisPayload(analysis), nil, analysis.NextSteps), nil
}

func (s *Server) summarizeErrorWindowTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req errorWindowArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	summary, result, ok := s.collectErrorWindowSummary(ctx, req)
	if !ok {
		return result, nil
	}
	return toolSuccess("error window summarized", errorWindowPayload(summary), nil, summary.NextSteps), nil
}

func (s *Server) appendLogAnalysisToSessionTool(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
	var req appendLogAnalysisArgs
	if err := decodeToolArgs(args, &req); err != nil {
		return toolError("invalid_arguments", err.Error(), nil), nil
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.AnalysisType = strings.TrimSpace(req.AnalysisType)
	if req.SessionID == "" {
		return toolError("invalid_arguments", "session_id is required", nil), nil
	}
	if req.AnalysisType != "trace" && req.AnalysisType != "error_window" {
		return toolError("invalid_arguments", "analysis_type must be trace or error_window", nil), nil
	}

	var analysis any
	var nextSteps []string
	var result CallToolResult
	var ok bool
	switch req.AnalysisType {
	case "trace":
		traceReq := req.traceLogAnalysisArgs
		analysisResult, toolResult, collected := s.collectTraceLogAnalysis(ctx, traceReq)
		result = toolResult
		ok = collected
		analysis = traceAnalysisPayload(analysisResult)
		nextSteps = analysisResult.NextSteps
	case "error_window":
		errorReq := errorWindowArgs{
			ProjectID:    req.ProjectID,
			ProjectName:  req.ProjectName,
			DeploymentID: req.DeploymentID,
			From:         req.From,
			To:           req.To,
			Since:        req.Since,
			Limit:        req.Limit,
		}
		summary, toolResult, collected := s.collectErrorWindowSummary(ctx, errorReq)
		result = toolResult
		ok = collected
		analysis = errorWindowPayload(summary)
		nextSteps = summary.NextSteps
	}
	if !ok {
		return result, nil
	}

	event, err := s.client.AppendDebugSessionEvent(ctx, req.SessionID, DebugSessionAppendEventRequest{
		Type:    "log_analysis",
		Actor:   "assistant",
		Summary: req.AnalysisType + " analysis collected",
		Data: map[string]any{
			"analysis_type": req.AnalysisType,
			"analysis":      analysis,
		},
	})
	if err != nil {
		return clientToolError(err), nil
	}
	data := map[string]any{
		"event":         event,
		"analysis_type": req.AnalysisType,
		"analysis":      analysis,
	}
	return toolSuccess("log analysis appended to debug session", data, nil, nextSteps), nil
}

func (s *Server) collectTraceLogAnalysis(ctx context.Context, req traceLogAnalysisArgs) (TraceAnalysis, CallToolResult, bool) {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.TraceID = strings.TrimSpace(req.TraceID)
	req.RequestID = strings.TrimSpace(req.RequestID)
	if (req.TraceID == "" && req.RequestID == "") || (req.TraceID != "" && req.RequestID != "") {
		return TraceAnalysis{}, toolError("invalid_arguments", "exactly one of trace_id or request_id is required", nil), false
	}
	project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return TraceAnalysis{}, result, false
	}

	queryText := "trace_id=" + req.TraceID
	if req.RequestID != "" {
		queryText = "request_id=" + req.RequestID
	}
	limit := logToolLimit(req.Limit)
	query := url.Values{}
	query.Set("project", project.ID)
	query.Set("q", queryText)
	query.Set("limit", strconv.Itoa(limit))

	resp, err := s.client.SearchLogs(ctx, query)
	if err != nil {
		return TraceAnalysis{}, clientToolError(err), false
	}
	analysis := analyzeLogEntries(resp.Items, analysisOptions{
		Limit: limit,
		SearchSummary: map[string]any{
			"project_id":          project.ID,
			"query":               resp.Query,
			"requested_query":     queryText,
			"total":               resp.Total,
			"has_more":            resp.HasMore,
			"deployment_counts":   resp.DeploymentCounts,
			"context_before_ms":   req.BeforeMS,
			"context_after_ms":    req.AfterMS,
			"search_result_count": len(resp.Items),
		},
	})
	return analysis, CallToolResult{}, true
}

func (s *Server) collectErrorWindowSummary(ctx context.Context, req errorWindowArgs) (ErrorWindowSummary, CallToolResult, bool) {
	req.ProjectID = strings.TrimSpace(req.ProjectID)
	req.ProjectName = strings.TrimSpace(req.ProjectName)
	req.DeploymentID = strings.TrimSpace(req.DeploymentID)
	req.From = strings.TrimSpace(req.From)
	req.To = strings.TrimSpace(req.To)
	req.Since = strings.TrimSpace(req.Since)

	project, result, ok := s.resolveProjectForLogs(ctx, req.ProjectID, req.ProjectName)
	if !ok {
		return ErrorWindowSummary{}, result, false
	}
	from, to, result, ok := parseErrorWindow(req)
	if !ok {
		return ErrorWindowSummary{}, result, false
	}

	limit := logToolLimit(req.Limit)
	entriesByID := map[int64]model.LogEntry{}
	total := 0
	hasMore := false
	deploymentCounts := map[string]int{}
	for _, term := range errorWindowSearchTerms {
		query := url.Values{}
		query.Set("project", project.ID)
		query.Set("q", term)
		query.Set("limit", strconv.Itoa(limit))
		if req.DeploymentID != "" {
			query.Add("deployment", req.DeploymentID)
		}
		resp, err := s.client.SearchLogs(ctx, query)
		if err != nil {
			return ErrorWindowSummary{}, clientToolError(err), false
		}
		total += resp.Total
		hasMore = hasMore || resp.HasMore
		for deploymentID, count := range resp.DeploymentCounts {
			deploymentCounts[deploymentID] += count
		}
		for _, entry := range resp.Items {
			if !entryInWindow(entry, from, to) {
				continue
			}
			entriesByID[entry.ID] = entry
		}
	}
	entries := make([]model.LogEntry, 0, len(entriesByID))
	for _, entry := range entriesByID {
		entries = append(entries, entry)
	}
	sortLogEntries(entries)
	if len(entries) > limit {
		entries = entries[:limit]
	}

	window := map[string]any{
		"project_id":          project.ID,
		"deployment_id":       req.DeploymentID,
		"search_terms":        errorWindowSearchTerms,
		"total":               total,
		"has_more":            hasMore,
		"deployment_counts":   deploymentCounts,
		"search_result_count": len(entriesByID),
	}
	if from != nil {
		window["from"] = from.Format(time.RFC3339Nano)
	}
	if to != nil {
		window["to"] = to.Format(time.RFC3339Nano)
	}
	summary := summarizeErrorEntries(entries, errorWindowOptions{Limit: limit, Window: window})
	return summary, CallToolResult{}, true
}

func parseErrorWindow(req errorWindowArgs) (*time.Time, *time.Time, CallToolResult, bool) {
	if req.Since != "" && (req.From != "" || req.To != "") {
		return nil, nil, toolError("invalid_arguments", "since cannot be combined with from or to", nil), false
	}
	var from *time.Time
	var to *time.Time
	if req.Since != "" {
		parsed, err := parseSince(req.Since)
		if err != nil {
			return nil, nil, toolError("invalid_arguments", "since must be RFC3339 time or duration", nil), false
		}
		from = &parsed
	}
	if req.From != "" {
		parsed, err := time.Parse(time.RFC3339Nano, req.From)
		if err != nil {
			return nil, nil, toolError("invalid_arguments", "from must be RFC3339 time", nil), false
		}
		from = &parsed
	}
	if req.To != "" {
		parsed, err := time.Parse(time.RFC3339Nano, req.To)
		if err != nil {
			return nil, nil, toolError("invalid_arguments", "to must be RFC3339 time", nil), false
		}
		to = &parsed
	}
	if from != nil && to != nil && from.After(*to) {
		return nil, nil, toolError("invalid_arguments", "from must be before to", nil), false
	}
	return from, to, CallToolResult{}, true
}

func entryInWindow(entry model.LogEntry, from *time.Time, to *time.Time) bool {
	if entry.Timestamp.IsZero() {
		return true
	}
	if from != nil && entry.Timestamp.Before(*from) {
		return false
	}
	if to != nil && entry.Timestamp.After(*to) {
		return false
	}
	return true
}

func sortLogEntries(entries []model.LogEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Timestamp.Equal(entries[j].Timestamp) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}

func traceAnalysisPayload(analysis TraceAnalysis) map[string]any {
	return map[string]any{
		"timeline":       analysis.Timeline,
		"services_seen":  analysis.ServicesSeen,
		"signals":        analysis.Signals,
		"evidence":       analysis.Evidence,
		"next_steps":     analysis.NextSteps,
		"search_summary": analysis.SearchSummary,
	}
}

func errorWindowPayload(summary ErrorWindowSummary) map[string]any {
	return map[string]any{
		"error_groups":    summary.ErrorGroups,
		"top_signals":     summary.TopSignals,
		"sample_evidence": summary.SampleEvidence,
		"window":          summary.Window,
		"next_steps":      summary.NextSteps,
	}
}
