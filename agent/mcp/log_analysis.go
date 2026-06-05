// log_analysis.go 提供 MCP 日志证据整理逻辑。
//
// 职责：
//   - 构建跨 deployment 时间线
//   - 用固定规则识别失败信号
//   - 对错误日志做稳定聚类
//
// 边界：
//   - 不调用 AI 模型
//   - 不断言根因
//   - 不查询 agent HTTP API
package mcp

import (
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

var (
	ipPortPattern = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}:\d+\b`)
	numberPattern = regexp.MustCompile(`\b\d+\b`)
	spacePattern  = regexp.MustCompile(`\s+`)
)

type signalDefinition struct {
	Code     string
	Label    string
	Severity string
}

var signalDefinitions = []signalDefinition{
	{Code: "panic", Label: "panic detected", Severity: "critical"},
	{Code: "fatal", Label: "fatal error detected", Severity: "critical"},
	{Code: "timeout", Label: "timeout detected", Severity: "high"},
	{Code: "connection_refused", Label: "connection refused", Severity: "high"},
	{Code: "address_in_use", Label: "address already in use", Severity: "high"},
	{Code: "health_check_failed", Label: "health check failed", Severity: "high"},
	{Code: "permission_denied", Label: "permission denied", Severity: "high"},
	{Code: "file_not_found", Label: "file not found", Severity: "medium"},
	{Code: "database_error", Label: "database error signal", Severity: "high"},
	{Code: "retry_exhausted", Label: "retry exhausted", Severity: "medium"},
}

// TimelineEntry 是跨 deployment 日志时间线中的一条记录。
type TimelineEntry struct {
	ID           int64     `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	Stream       string    `json:"stream,omitempty"`
}

// LogSignal 描述从日志中识别出的确定性失败信号。
type LogSignal struct {
	Code        string   `json:"code"`
	Label       string   `json:"label"`
	Severity    string   `json:"severity"`
	Count       int      `json:"count"`
	Deployments []string `json:"deployments"`
	EvidenceIDs []int64  `json:"evidence_ids"`
}

// LogEvidence 是用于支持信号判断的日志证据。
type LogEvidence struct {
	ID           int64     `json:"id"`
	DeploymentID string    `json:"deployment_id"`
	Timestamp    time.Time `json:"timestamp"`
	Level        string    `json:"level"`
	Message      string    `json:"message"`
	SignalCodes  []string  `json:"signal_codes,omitempty"`
}

// TraceAnalysis 是 trace/request 级日志证据分析结果。
type TraceAnalysis struct {
	Timeline      []TimelineEntry `json:"timeline"`
	ServicesSeen  []string        `json:"services_seen"`
	Signals       []LogSignal     `json:"signals"`
	Evidence      []LogEvidence   `json:"evidence"`
	NextSteps     []string        `json:"next_steps"`
	SearchSummary map[string]any  `json:"search_summary,omitempty"`
}

// ErrorGroup 描述一个归一化错误消息分组。
type ErrorGroup struct {
	GroupKey    string    `json:"group_key"`
	Count       int       `json:"count"`
	FirstSeen   time.Time `json:"first_seen"`
	LastSeen    time.Time `json:"last_seen"`
	Deployments []string  `json:"deployments"`
	EvidenceIDs []int64   `json:"evidence_ids"`
	Sample      string    `json:"sample"`
}

// ErrorWindowSummary 是时间窗口内错误日志的聚类摘要。
type ErrorWindowSummary struct {
	ErrorGroups    []ErrorGroup   `json:"error_groups"`
	TopSignals     []LogSignal    `json:"top_signals"`
	SampleEvidence []LogEvidence  `json:"sample_evidence"`
	Window         map[string]any `json:"window"`
	NextSteps      []string       `json:"next_steps"`
}

type analysisOptions struct {
	Limit         int
	SearchSummary map[string]any
}

type errorWindowOptions struct {
	Limit  int
	Window map[string]any
}

func analyzeLogEntries(entries []model.LogEntry, opts analysisOptions) TraceAnalysis {
	ordered := sortedLogEntries(entries)
	limit := logToolLimit(opts.Limit)

	timeline := make([]TimelineEntry, 0, minInt(len(ordered), limit))
	servicesSeen := make([]string, 0)
	evidence := make([]LogEvidence, 0)
	signalBuckets := map[string]*signalAccumulator{}
	for _, entry := range ordered {
		if len(timeline) < limit {
			timeline = append(timeline, timelineEntry(entry))
		}
		servicesSeen = append(servicesSeen, entry.DeploymentID)

		codes := detectSignals(entry)
		if len(codes) == 0 {
			continue
		}
		for _, code := range codes {
			accumulateSignal(signalBuckets, code, entry)
		}
		if len(evidence) < limit {
			evidence = append(evidence, logEvidence(entry, codes))
		}
	}

	signals := signalAccumulatorsToList(signalBuckets)
	return TraceAnalysis{
		Timeline:      timeline,
		ServicesSeen:  uniqueSortedStrings(servicesSeen),
		Signals:       signals,
		Evidence:      evidence,
		NextSteps:     nextStepsForSignals(signals),
		SearchSummary: opts.SearchSummary,
	}
}

func summarizeErrorEntries(entries []model.LogEntry, opts errorWindowOptions) ErrorWindowSummary {
	ordered := sortedLogEntries(entries)
	limit := logToolLimit(opts.Limit)
	groups := map[string]*errorGroupAccumulator{}
	signalBuckets := map[string]*signalAccumulator{}
	evidence := make([]LogEvidence, 0)

	for _, entry := range ordered {
		codes := detectSignals(entry)
		if !isErrorEntry(entry, codes) {
			continue
		}
		key := normalizeErrorMessage(entry.Message)
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(entry.Level))
		}
		accumulateErrorGroup(groups, key, entry)
		for _, code := range codes {
			accumulateSignal(signalBuckets, code, entry)
		}
		if len(evidence) < limit {
			evidence = append(evidence, logEvidence(entry, codes))
		}
	}

	errorGroups := errorGroupAccumulatorsToList(groups)
	if len(errorGroups) > limit {
		errorGroups = errorGroups[:limit]
	}
	signals := signalAccumulatorsToList(signalBuckets)
	window := opts.Window
	if window == nil {
		window = map[string]any{}
	}
	window["input_count"] = len(entries)
	window["error_count"] = len(evidence)
	return ErrorWindowSummary{
		ErrorGroups:    errorGroups,
		TopSignals:     signals,
		SampleEvidence: evidence,
		Window:         window,
		NextSteps:      nextStepsForSignals(signals),
	}
}

func detectSignals(entry model.LogEntry) []string {
	level := strings.ToUpper(strings.TrimSpace(entry.Level))
	message := strings.ToLower(entry.Message)
	codes := make([]string, 0, 2)
	if strings.Contains(message, "panic") || level == "PANIC" {
		codes = append(codes, "panic")
	}
	if level == "FATAL" || strings.Contains(message, "fatal") {
		codes = append(codes, "fatal")
	}
	if strings.Contains(message, "timeout") || strings.Contains(message, "timed out") {
		codes = append(codes, "timeout")
	}
	if strings.Contains(message, "connection refused") {
		codes = append(codes, "connection_refused")
	}
	if strings.Contains(message, "address already in use") || strings.Contains(message, "bind: address in use") {
		codes = append(codes, "address_in_use")
	}
	if strings.Contains(message, "health check failed") || strings.Contains(message, "healthcheck failed") {
		codes = append(codes, "health_check_failed")
	}
	if strings.Contains(message, "permission denied") {
		codes = append(codes, "permission_denied")
	}
	if strings.Contains(message, "no such file or directory") || strings.Contains(message, "file not found") {
		codes = append(codes, "file_not_found")
	}
	if strings.Contains(message, "database") &&
		(strings.Contains(message, "error") || strings.Contains(message, "refused") || strings.Contains(message, "failed")) {
		codes = append(codes, "database_error")
	}
	if strings.Contains(message, "retry exhausted") {
		codes = append(codes, "retry_exhausted")
	}
	return uniqueSignalCodes(codes)
}

func normalizeErrorMessage(message string) string {
	out := strings.ToLower(strings.TrimSpace(message))
	out = ipPortPattern.ReplaceAllString(out, "<addr>")
	out = numberPattern.ReplaceAllString(out, "<num>")
	out = spacePattern.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

func uniqueSortedStrings(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		seen[value] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type signalAccumulator struct {
	Definition  signalDefinition
	Count       int
	Deployments []string
	EvidenceIDs []int64
}

type errorGroupAccumulator struct {
	GroupKey    string
	Count       int
	FirstSeen   time.Time
	LastSeen    time.Time
	Deployments []string
	EvidenceIDs []int64
	Sample      string
}

func sortedLogEntries(entries []model.LogEntry) []model.LogEntry {
	out := make([]model.LogEntry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Timestamp.Equal(out[j].Timestamp) {
			return out[i].ID < out[j].ID
		}
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out
}

func timelineEntry(entry model.LogEntry) TimelineEntry {
	return TimelineEntry{
		ID:           entry.ID,
		DeploymentID: entry.DeploymentID,
		Timestamp:    entry.Timestamp,
		Level:        entry.Level,
		Message:      entry.Message,
		Stream:       entry.Stream,
	}
}

func logEvidence(entry model.LogEntry, codes []string) LogEvidence {
	return LogEvidence{
		ID:           entry.ID,
		DeploymentID: entry.DeploymentID,
		Timestamp:    entry.Timestamp,
		Level:        entry.Level,
		Message:      entry.Message,
		SignalCodes:  codes,
	}
}

func accumulateSignal(buckets map[string]*signalAccumulator, code string, entry model.LogEntry) {
	def := signalDefinitionFor(code)
	acc, ok := buckets[code]
	if !ok {
		acc = &signalAccumulator{Definition: def}
		buckets[code] = acc
	}
	acc.Count++
	acc.Deployments = append(acc.Deployments, entry.DeploymentID)
	acc.EvidenceIDs = append(acc.EvidenceIDs, entry.ID)
}

func signalAccumulatorsToList(buckets map[string]*signalAccumulator) []LogSignal {
	out := make([]LogSignal, 0, len(buckets))
	for _, acc := range buckets {
		out = append(out, LogSignal{
			Code:        acc.Definition.Code,
			Label:       acc.Definition.Label,
			Severity:    acc.Definition.Severity,
			Count:       acc.Count,
			Deployments: uniqueSortedStrings(acc.Deployments),
			EvidenceIDs: uniqueInt64s(acc.EvidenceIDs),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return signalOrder(out[i].Code) < signalOrder(out[j].Code)
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func signalDefinitionFor(code string) signalDefinition {
	for _, def := range signalDefinitions {
		if def.Code == code {
			return def
		}
	}
	return signalDefinition{Code: code, Label: code, Severity: "medium"}
}

func signalOrder(code string) int {
	for i, def := range signalDefinitions {
		if def.Code == code {
			return i
		}
	}
	return len(signalDefinitions)
}

func accumulateErrorGroup(groups map[string]*errorGroupAccumulator, key string, entry model.LogEntry) {
	group, ok := groups[key]
	if !ok {
		group = &errorGroupAccumulator{
			GroupKey:  key,
			FirstSeen: entry.Timestamp,
			LastSeen:  entry.Timestamp,
			Sample:    entry.Message,
		}
		groups[key] = group
	}
	group.Count++
	if entry.Timestamp.Before(group.FirstSeen) {
		group.FirstSeen = entry.Timestamp
	}
	if entry.Timestamp.After(group.LastSeen) {
		group.LastSeen = entry.Timestamp
	}
	group.Deployments = append(group.Deployments, entry.DeploymentID)
	group.EvidenceIDs = append(group.EvidenceIDs, entry.ID)
}

func errorGroupAccumulatorsToList(groups map[string]*errorGroupAccumulator) []ErrorGroup {
	out := make([]ErrorGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, ErrorGroup{
			GroupKey:    group.GroupKey,
			Count:       group.Count,
			FirstSeen:   group.FirstSeen,
			LastSeen:    group.LastSeen,
			Deployments: uniqueSortedStrings(group.Deployments),
			EvidenceIDs: uniqueInt64s(group.EvidenceIDs),
			Sample:      group.Sample,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].FirstSeen.Before(out[j].FirstSeen)
		}
		return out[i].Count > out[j].Count
	})
	return out
}

func isErrorEntry(entry model.LogEntry, codes []string) bool {
	if len(codes) > 0 {
		return true
	}
	level := strings.ToUpper(strings.TrimSpace(entry.Level))
	return level == "ERROR" || level == "FATAL" || level == "PANIC"
}

func uniqueSignalCodes(codes []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func uniqueInt64s(values []int64) []int64 {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func nextStepsForSignals(signals []LogSignal) []string {
	if len(signals) == 0 {
		return []string{"继续收集更近的日志上下文，确认是否存在稳定失败信号。"}
	}
	steps := []string{"根据 evidence_ids 拉取上下文日志，确认失败信号出现前后的服务顺序。"}
	for _, signal := range signals {
		switch signal.Code {
		case "connection_refused", "database_error":
			steps = append(steps, "检查目标服务、数据库或依赖端口是否已启动并监听。")
		case "address_in_use":
			steps = append(steps, "检查端口占用，确认 deployment 配置的监听地址未被其他进程使用。")
		case "permission_denied":
			steps = append(steps, "检查工作目录、脚本和日志文件权限。")
		case "retry_exhausted":
			steps = append(steps, "向前查看首次 retry 失败日志，定位重试耗尽之前的原始错误。")
		}
	}
	return uniqueOrderedStrings(steps)
}

func uniqueOrderedStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
