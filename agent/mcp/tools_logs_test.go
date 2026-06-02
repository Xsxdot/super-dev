// Package mcp 验证日志相关 MCP 工具。
//
// 职责：
//   - 验证 tail_logs 应用项目日志规则
//   - 验证 search_logs 参数校验
//   - 验证 get_log_context 调用 agent context API
//
// 边界：
//   - 不访问真实 agent HTTP 服务
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestTailLogsAppliesExcludeRule(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		rules: []model.LogRule{{
			Name: "heartbeat", Type: model.RuleTypeExclude, Keywords: []string{"heartbeat"}, Logic: model.RuleLogicOR, Enabled: true,
		}},
		logs: LogsResponse{Items: []model.LogEntry{
			{ID: 1, DeploymentID: "dep-api-dev", Message: "heartbeat ok"},
			{ID: 2, DeploymentID: "dep-api-dev", Message: "server ready"},
		}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "tail_logs", `{"deployment_id":"dep-api-dev","limit":100,"apply_project_rules":true}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	data := body.Data.(map[string]any)
	assert.Equal(t, 1, data["count"])
}

func TestSearchLogsRequiresQuery(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "search_logs", `{}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestGetLogContextCallsAgentWithProjectAndID(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_log_context", `{"project_name":"demo","id":42}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "42", client.contextQuery.Get("id"))
}
