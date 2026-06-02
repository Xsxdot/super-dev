// Package mcp 验证日志分析 MCP 工具。
//
// 职责：
//   - 验证 trace/request 搜索和错误窗口聚类
//   - 验证工具只返回证据和建议，不断言根因
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不修改 debug session
package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestAnalyzeTraceLogsSearchesProjectTrace(t *testing.T) {
	base := time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		search: LogSearchResponse{
			Query: "trace_id=t1",
			Items: []model.LogEntry{
				{ID: 1, DeploymentID: "dep-api-dev", Timestamp: base, Level: "INFO", Message: "trace_id=t1 accepted"},
				{ID: 2, DeploymentID: "dep-api-dev", Timestamp: base.Add(time.Second), Level: "ERROR", Message: "trace_id=t1 retry exhausted"},
			},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "analyze_trace_logs", `{"project_name":"demo","trace_id":"t1","limit":20}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	assert.Contains(t, body.Summary, "trace")
	assert.Contains(t, body.Data, "timeline")
	assert.Contains(t, body.Data, "signals")
}

func TestSummarizeErrorWindowRequiresProject(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "summarize_error_window", `{"since":"10m"}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}
