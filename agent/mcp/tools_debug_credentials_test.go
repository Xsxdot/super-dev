// tools_debug_credentials_test.go 验证 get_debug_credentials 工具与快照剥字段。
//
// 职责：
//   - 验证 MCP 工具会通过 AgentClient 返回调试凭据明文
//   - 验证快照类工具不会暴露 debug_credentials 字段
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不验证 agent 端点合并逻辑，该逻辑由 api/model 包测试覆盖
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestGetDebugCredentialsToolReturnsPlaintext(t *testing.T) {
	client := &fakeAgentClient{
		debugCredentials: []model.MergedDebugCredential{
			{DebugCredential: model.DebugCredential{Name: "test_login", Value: "p", Desc: "登录"}, Source: "project"},
		},
	}
	server := NewServer(client)
	tool, ok := server.tools["get_debug_credentials"]
	require.True(t, ok)

	result, err := tool.Handler(context.Background(), []byte(`{"project_id":"p1"}`))

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	data := payload.Data.(map[string]any)
	credentials := data["credentials"].([]model.MergedDebugCredential)
	require.Len(t, credentials, 1)
	assert.Equal(t, "test_login", credentials[0].Name)
	assert.Equal(t, "p", credentials[0].Value)
	assert.True(t, credentials[0].ValuePresent)
	assert.Equal(t, "project", credentials[0].Source)
	assert.Equal(t, "p1", client.lastDebugCredentialsQuery.Get("project_id"))
}

func TestGetDebugCredentialsToolRequiresProjectSelector(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	tool, ok := server.tools["get_debug_credentials"]
	require.True(t, ok)

	result, err := tool.Handler(context.Background(), []byte(`{}`))

	require.NoError(t, err)
	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "invalid_arguments", payload.Code)
}

func TestGetDebugCredentialsToolDescriptionMentionsCredentialHints(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	tool, ok := server.tools["get_debug_credentials"]
	require.True(t, ok)

	assert.Contains(t, tool.Tool.Description, "has_debug_credentials/debug_credential_hints")
	assert.Contains(t, tool.Tool.Description, "auth_hint")
	assert.Contains(t, tool.Tool.Description, "desc")
	assert.Contains(t, tool.Tool.Description, "do not fabricate tokens")
}

func TestSanitizeServiceExposesCredentialHintsWithoutValues(t *testing.T) {
	svc := model.Service{
		ID:               "s1",
		DebugCredentials: []model.DebugCredential{{Name: "x", Value: "secret", Desc: "d"}},
	}

	got := sanitizeService(svc)

	assert.Nil(t, got.DebugCredentials)
	assert.True(t, got.HasDebugCredentials)
	require.Len(t, got.DebugCredentialHints, 1)
	assert.Equal(t, model.DebugCredentialHint{Name: "x", Desc: "d", Source: "service"}, got.DebugCredentialHints[0])
	assert.NotContains(t, got.DebugCredentialHints[0].Name, "secret")
	assert.NotContains(t, got.DebugCredentialHints[0].Desc, "secret")
}

func TestSanitizeProjectExposesCredentialHintsWithoutValues(t *testing.T) {
	project := model.Project{
		ID:       "p1",
		AINote:   "Use seeded login",
		AuthHint: "Prefer session cookie",
		DebugCredentials: []model.DebugCredential{{
			Name:  "project_login",
			Value: "secret",
			Desc:  "登录",
		}},
	}

	got := sanitizeProject(project)

	assert.Equal(t, "Use seeded login", got.AINote)
	assert.Equal(t, "Prefer session cookie", got.AuthHint)
	assert.Nil(t, got.DebugCredentials)
	assert.True(t, got.HasDebugCredentials)
	require.Len(t, got.DebugCredentialHints, 1)
	assert.Equal(t, model.DebugCredentialHint{Name: "project_login", Desc: "登录", Source: "project"}, got.DebugCredentialHints[0])
	assert.NotContains(t, got.DebugCredentialHints[0].Name, "secret")
	assert.NotContains(t, got.DebugCredentialHints[0].Desc, "secret")
}
