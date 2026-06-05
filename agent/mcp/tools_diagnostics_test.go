// Package mcp 验证服务诊断 MCP 工具。
//
// 职责：
//   - 验证 diagnose_service 聚合运行状态和最近错误日志
//
// 边界：
//   - 不访问真实 agent HTTP 服务
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestDiagnoseServiceAggregatesStatusAndRecentErrors(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		services: []model.Service{sampleService("api", model.StatusFailed, "dep-api-dev")},
		logs: LogsResponse{Items: []model.LogEntry{
			{ID: 10, DeploymentID: "dep-api-dev", Level: "ERROR", Message: "listen tcp :8080: bind: address already in use"},
			{ID: 11, DeploymentID: "dep-api-dev", Level: "INFO", Message: "shutdown"},
		}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "diagnose_service", `{"deployment_id":"dep-api-dev"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	data := body.Data.(map[string]any)
	assert.Equal(t, "failed", data["status"])
	assert.NotEmpty(t, data["evidence"])
	assert.NotEmpty(t, data["hints"])
}
