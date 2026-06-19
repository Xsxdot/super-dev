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
	"github.com/xsxdot/super-dev/agent/model"
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

func TestTailLogsAmbiguousTargetSanitizesCredentialCandidates(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProjectWithDebugCredentials()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "tail_logs", `{"project_name":"demo","service_name":"api"}`)

	require.NoError(t, err)
	assertAmbiguousCredentialCandidatesSanitized(t, result)
}

func TestSearchLogsRequiresQuery(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "search_logs", `{}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestSearchLogsAmbiguousProjectSanitizesCredentialCandidates(t *testing.T) {
	client := &fakeAgentClient{projects: sampleDuplicateProjectsWithDebugCredentials()}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "search_logs", `{"q":"panic","project_name":"demo"}`)

	require.NoError(t, err)
	assertAmbiguousProjectCredentialCandidatesSanitized(t, result)
}

func TestGetLogContextCallsAgentWithProjectAndID(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_log_context", `{"project_name":"demo","id":42}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "42", client.contextQuery.Get("id"))
}

func TestGetLogContextAmbiguousProjectSanitizesCredentialCandidates(t *testing.T) {
	client := &fakeAgentClient{projects: sampleDuplicateProjectsWithDebugCredentials()}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_log_context", `{"project_name":"demo","id":42}`)

	require.NoError(t, err)
	assertAmbiguousProjectCredentialCandidatesSanitized(t, result)
}

func sampleDuplicateProjectsWithDebugCredentials() []model.Project {
	first := sampleProjectWithDebugCredentials()
	second := sampleProjectWithDebugCredentials()
	first.ID = "p1"
	second.ID = "p2"
	return []model.Project{first, second}
}

func assertAmbiguousProjectCredentialCandidatesSanitized(t *testing.T, result CallToolResult) {
	t.Helper()

	require.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "ambiguous_project", payload.Code)
	projects := payload.Data.([]model.Project)
	require.Len(t, projects, 2)
	for _, project := range projects {
		assert.Nil(t, project.DebugCredentials)
		assert.True(t, project.HasDebugCredentials)
		assert.Equal(t, []model.DebugCredentialHint{
			{Name: "shared_login", Desc: "项目默认登录", Source: "project"},
		}, project.DebugCredentialHints)
		require.Len(t, project.Services, 1)
		service := project.Services[0]
		assert.Nil(t, service.DebugCredentials)
		assert.True(t, service.HasDebugCredentials)
		assert.Equal(t, []model.DebugCredentialHint{
			{Name: "shared_login", Desc: "服务覆盖登录", Source: "service"},
			{Name: "service_only", Desc: "服务专用", Source: "service"},
		}, service.DebugCredentialHints)
		for _, hint := range append(project.DebugCredentialHints, service.DebugCredentialHints...) {
			assert.NotContains(t, hint.Name, "secret")
			assert.NotContains(t, hint.Desc, "secret")
		}
	}
}
