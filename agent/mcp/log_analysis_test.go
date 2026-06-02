// Package mcp 验证确定性日志分析辅助逻辑。
//
// 职责：
//   - 验证 trace 时间线、失败信号识别和错误聚类
//   - 保证 MCP 不依赖 AI 推断来生成证据摘要
//
// 边界：
//   - 不访问 agent HTTP API
//   - 不写 debug session
package mcp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestAnalyzeEntriesBuildsTimelineAndSignals(t *testing.T) {
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	entries := []model.LogEntry{
		{ID: 2, DeploymentID: "worker-dev", Timestamp: base.Add(2 * time.Second), Level: "ERROR", Message: "retry exhausted trace_id=t1"},
		{ID: 1, DeploymentID: "api-dev", Timestamp: base, Level: "INFO", Message: "request accepted trace_id=t1"},
		{ID: 3, DeploymentID: "api-dev", Timestamp: base.Add(3 * time.Second), Level: "ERROR", Message: "database connection refused trace_id=t1"},
	}

	analysis := analyzeLogEntries(entries, analysisOptions{Limit: 10})

	require.Len(t, analysis.Timeline, 3)
	assert.Equal(t, int64(1), analysis.Timeline[0].ID)
	assert.ElementsMatch(t, []string{"api-dev", "worker-dev"}, analysis.ServicesSeen)
	assertSignalCodes(t, analysis.Signals, []string{"retry_exhausted", "connection_refused", "database_error"})
	assert.Len(t, analysis.Evidence, 2)
}

func TestSummarizeErrorWindowGroupsNormalizedMessages(t *testing.T) {
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	entries := []model.LogEntry{
		{ID: 1, DeploymentID: "api-dev", Timestamp: base, Level: "ERROR", Message: "connection refused 127.0.0.1:5432 request 123"},
		{ID: 2, DeploymentID: "api-dev", Timestamp: base.Add(time.Second), Level: "ERROR", Message: "connection refused 127.0.0.1:5433 request 456"},
		{ID: 3, DeploymentID: "worker-dev", Timestamp: base.Add(2 * time.Second), Level: "WARN", Message: "heartbeat"},
	}

	summary := summarizeErrorEntries(entries, errorWindowOptions{Limit: 10})

	require.Len(t, summary.ErrorGroups, 1)
	assert.Equal(t, 2, summary.ErrorGroups[0].Count)
	assert.Contains(t, summary.ErrorGroups[0].GroupKey, "connection refused")
	assertSignalCodes(t, summary.TopSignals, []string{"connection_refused"})
}

func assertSignalCodes(t *testing.T, signals []LogSignal, want []string) {
	t.Helper()
	got := make([]string, 0, len(signals))
	for _, signal := range signals {
		got = append(got, signal.Code)
	}
	assert.ElementsMatch(t, want, got)
}
