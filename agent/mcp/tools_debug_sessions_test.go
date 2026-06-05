// Package mcp 验证 debug session MCP 工具。
//
// 职责：
//   - 验证会话工具参数校验、脱敏和结构化输出
//   - 验证写入工具只写 debug session，不操作运行态
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

func TestCreateDebugSessionResolvesProjectName(t *testing.T) {
	client := &fakeAgentClient{
		projects: []model.Project{sampleProject()},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "create_debug_session", `{"project_name":"demo","title":"API failure","question":"Why does api fail?"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "p1", client.createdDebugSession.ProjectID)
	assert.Equal(t, "demo", client.createdDebugSession.ProjectName)
}

func TestAppendDebugSessionNoteRedactsSecretData(t *testing.T) {
	client := &fakeAgentClient{}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "append_debug_session_note", `{"session_id":"dbg_1","type":"observation","actor":"assistant","summary":"captured env","data":{"API_TOKEN":"secret","safe":"value"}}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "dbg_1", client.appendedSessionID)
	assert.Equal(t, "[redacted]", client.appendedEventRequest.Data["API_TOKEN"])
	assert.Equal(t, "value", client.appendedEventRequest.Data["safe"])
}

func TestCloseDebugSessionCallsAgent(t *testing.T) {
	client := &fakeAgentClient{}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "close_debug_session", `{"session_id":"dbg_1","summary":"done"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "dbg_1", client.closedSessionID)
}
