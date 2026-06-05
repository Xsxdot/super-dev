// Package mcp 验证项目级 pipeline 部署 MCP 工具。
//
// 职责：
//   - 验证部署、回滚、历史、制品和日志工具会委托 AgentClient
//   - 验证工具成功响应包含稳定的顶层数据 key
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不测试 pipeline 引擎和制品仓库内部行为
package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

func TestValidateProjectPipelineToolReturnsPreview(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		pipelinePreview: ProjectPipelinePreview{
			Plan: pipeline.Plan{Phases: map[model.PipelinePhase][]model.Step{
				model.PhaseBuild: []model.Step{{Name: "Build", Type: "local_command"}},
			}},
			Run: model.Run{ID: "run-1", Status: model.StatusPending},
		},
	}
	server := NewServer(client)

	result := callPipelineTool(t, server, "validate_project_pipeline", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod",
		"env_name":"prod",
		"variables":{"version":"v1"}
	}`)

	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	assert.Equal(t, map[string]bool{"ok": true}, data["validation"])
	assert.Equal(t, "prod", client.lastPipelinePreviewRequest.EnvName)
	assert.Equal(t, "v1", client.lastPipelinePreviewRequest.Variables["version"])
	assert.Contains(t, data, "plan")
	assert.Contains(t, data, "run")
}

func TestValidateProjectPipelineToolRequiresEnvName(t *testing.T) {
	server := NewServer(&fakeAgentClient{projects: []model.Project{sampleProject()}})

	result := callPipelineTool(t, server, "validate_project_pipeline", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod"
	}`)

	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "invalid_arguments", payload.Code)
	assert.Equal(t, "env_name is required", payload.Message)
}

func TestValidateProjectPipelineToolMapsAgentError(t *testing.T) {
	client := &fakeAgentClient{
		projects:           []model.Project{sampleProject()},
		pipelinePreviewErr: errors.New("agent error: pipeline validation failed: remote_command requires targets"),
	}
	server := NewServer(client)

	result := callPipelineTool(t, server, "validate_project_pipeline", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod",
		"env_name":"prod"
	}`)

	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "pipeline_validation_failed", payload.Code)
	assert.Contains(t, payload.Message, "remote_command requires targets")
}

func TestValidateProjectPipelineToolResolvesProjectName(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
		pipelinePreview: ProjectPipelinePreview{
			Run: model.Run{ID: "run-1", Status: model.StatusPending},
		},
	}
	server := NewServer(client)

	result := callPipelineTool(t, server, "validate_project_pipeline", `{
		"project_name":"demo",
		"pipeline_id":"deploy-prod",
		"env_name":"prod"
	}`)

	assert.False(t, result.IsError)
	assert.Equal(t, "prod", client.lastPipelinePreviewRequest.EnvName)
}

func TestDeployProjectPipelineToolReturnsRun(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result := callPipelineTool(t, server, "deploy_project_pipeline", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod",
		"env_name":"prod",
		"variables":{"version":"v1"}
	}`)

	assert.False(t, result.IsError)
	assertPipelinePayloadHasKey(t, result, "run")
	assert.Equal(t, "v1", client.lastPipelineDeploy.Variables["version"])
}

func TestRollbackProjectPipelineToolReturnsRun(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result := callPipelineTool(t, server, "deploy_project_pipeline", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod",
		"env_name":"prod",
		"artifact_version":"old"
	}`)

	assert.False(t, result.IsError)
	assertPipelinePayloadHasKey(t, result, "run")
	assert.Equal(t, "old", client.lastPipelineDeploy.ArtifactVersion)
}

func TestListPipelineRunsToolReturnsRuns(t *testing.T) {
	server := NewServer(&fakeAgentClient{projects: []model.Project{sampleProject()}})

	result := callPipelineTool(t, server, "list_pipeline_runs", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod"
	}`)

	assert.False(t, result.IsError)
	assertPipelinePayloadHasKey(t, result, "runs")
}

func TestListPipelineArtifactsToolReturnsArtifacts(t *testing.T) {
	server := NewServer(&fakeAgentClient{projects: []model.Project{sampleProject()}})

	result := callPipelineTool(t, server, "list_pipeline_artifacts", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod"
	}`)

	assert.False(t, result.IsError)
	assertPipelinePayloadHasKey(t, result, "artifacts")
}

func TestReadPipelineRunLogsToolReturnsLogs(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result := callPipelineTool(t, server, "read_pipeline_run_logs", `{
		"project_id":"p1",
		"pipeline_id":"deploy-prod",
		"run_id":"run-1",
		"step_name":"Deploy Local",
		"host_id":"h1"
	}`)

	assert.False(t, result.IsError)
	assertPipelinePayloadHasKey(t, result, "logs")
	assert.Equal(t, "Deploy Local", client.lastPipelineLogQuery.Get("step_name"))
	assert.Equal(t, "h1", client.lastPipelineLogQuery.Get("host_id"))
}

func callPipelineTool(t *testing.T, server *Server, name string, args string) CallToolResult {
	t.Helper()
	_, ok := server.tools[name]
	require.Truef(t, ok, "tool %s should be registered", name)
	result, err := server.callToolForTest(context.Background(), name, args)
	require.NoError(t, err)
	return result
}

func assertPipelinePayloadHasKey(t *testing.T, result CallToolResult, key string) {
	t.Helper()
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	assert.Contains(t, data, key)
}
