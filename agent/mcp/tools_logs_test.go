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
	"time"

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

func TestTailLogsLevelSinceScansOlderPages(t *testing.T) {
	base := time.Now().UTC().Add(-time.Minute)
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		logPages: map[string]LogsResponse{
			"": {
				Items: []model.LogEntry{
					{ID: 101, DeploymentID: "dep-api-prod", Timestamp: base.Add(2 * time.Second), Level: "INFO", Message: "request completed"},
				},
				Next: struct {
					Time string `json:"time,omitempty"`
					ID   string `json:"id,omitempty"`
				}{Time: base.Format(time.RFC3339Nano), ID: "100"},
			},
			"100": {
				Items: []model.LogEntry{
					{ID: 99, DeploymentID: "dep-api-prod", Timestamp: base, Level: "ERROR", Message: "older page failure"},
				},
			},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "tail_logs", `{"deployment_id":"dep-api-prod","limit":1,"level":"error","since":"2h"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	data := body.Data.(map[string]any)
	assert.Equal(t, 1, data["count"])
	entries := data["entries"].([]model.LogEntry)
	require.Len(t, entries, 1)
	assert.Equal(t, int64(99), entries[0].ID)
	require.Len(t, client.fetchLogQueries, 2)
	assert.Empty(t, client.fetchLogQueries[0].Get("before"))
	assert.Equal(t, "100", client.fetchLogQueries[1].Get("before"))
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

func TestSearchLogsAddsProjectWhenOnlyDeploymentIsProvided(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		search: LogSearchResponse{
			Query: "panic",
			Items: []model.LogEntry{{ID: 1, DeploymentID: "dep-api-prod", Level: "ERROR", Message: "panic"}},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "search_logs", `{"deployment_id":"dep-api-prod","q":"panic"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "p1", client.searchQuery.Get("project"))
	assert.Equal(t, []string{"dep-api-prod"}, client.searchQuery["deployment"])
}

func TestGetLogContextCallsAgentWithProjectAndID(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_log_context", `{"project_name":"demo","id":42}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "42", client.contextQuery.Get("id"))
}

func TestGetLogContextAllowsDeploymentOnly(t *testing.T) {
	client := &fakeAgentClient{
		contextResp: LogContextResponse{
			TargetID: 42,
			ItemsByDeployment: map[string][]model.LogEntry{
				"dep-api-prod": {{ID: 42, DeploymentID: "dep-api-prod", Level: "ERROR", Message: "boom"}},
			},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_log_context", `{"deployment_id":"dep-api-prod","id":42}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "42", client.contextQuery.Get("id"))
	assert.Empty(t, client.contextQuery.Get("project"))
	assert.Equal(t, []string{"dep-api-prod"}, client.contextQuery["deployment"])
}

func TestFollowLogsFetchesFiniteRecentWindow(t *testing.T) {
	base := time.Now().UTC()
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		logs: LogsResponse{Items: []model.LogEntry{
			{ID: 1, DeploymentID: "dep-api-dev", Timestamp: base, Level: "INFO", Message: "ready"},
		}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "follow_logs", `{"deployment_id":"dep-api-dev","duration_ms":1,"poll_interval_ms":1,"limit":10}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	body := result.StructuredContent.(toolPayload)
	data := body.Data.(map[string]any)
	assert.Equal(t, 1, data["count"])
	assert.GreaterOrEqual(t, len(client.fetchLogQueries), 1)
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
