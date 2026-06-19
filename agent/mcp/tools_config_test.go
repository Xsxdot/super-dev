// Package mcp 验证配置 upsert MCP 工具。
//
// 职责：
//   - 验证配置预览、应用和便捷 upsert 工具
//   - 验证 approval_required 结构化错误不丢失
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不读写 .superdev/config.yaml
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestPreviewConfigChangeToolReturnsDiffAndPlan(t *testing.T) {
	client := &fakeAgentClient{
		configPreview: ConfigChangePreview{
			Kind:       "config.pipeline.upsert",
			Diff:       []ConfigChangeDiffEntry{{Path: "pipelines[deploy-dev]", After: "Deploy Dev"}},
			Validation: ConfigChangeValidation{OK: true},
			Plan:       OperationPlan{ID: "op_cfg", Kind: "config.pipeline.upsert", RequiresApproval: true},
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "preview_config_change", `{"kind":"config.pipeline.upsert","project_id":"p1","pipeline":{"id":"deploy-dev","name":"Deploy Dev"}}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	assert.Equal(t, "op_cfg", payload.Data.(map[string]any)["preview"].(ConfigChangePreview).Plan.ID)
}

func TestPreviewConfigChangeSanitizesDebugCredentials(t *testing.T) {
	client := &fakeAgentClient{configPreview: configChangePreviewWithDebugCredentials()}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "preview_config_change", `{"kind":"config.service.upsert","project_id":"p1","service":{"name":"api"}}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assertConfigChangePreviewDebugCredentialsSanitized(t, result)
}

func TestApplyConfigChangeSanitizesDebugCredentials(t *testing.T) {
	client := &fakeAgentClient{configPreview: configChangePreviewWithDebugCredentials()}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "apply_config_change", `{"kind":"config.service.upsert","project_id":"p1","approval_token":"tok_cfg","service":{"name":"api"}}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assertConfigChangePreviewDebugCredentialsSanitized(t, result)
}

func TestConfigChangeSchemaDocumentsCanonicalHostIDs(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	schema := server.tools["preview_config_change"].Tool.InputSchema
	properties := schema["properties"].(map[string]any)

	service := properties["service"].(map[string]any)

	assert.Contains(t, service["description"], "list_hosts")
	assert.Contains(t, service["description"], "Host.id")
	assert.Contains(t, service["description"], "not host name")
}

func TestServiceUpsertSchemaDocumentsLanguageRuntimeContract(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	tool := server.tools["upsert_service"].Tool
	schema := tool.InputSchema
	properties := schema["properties"].(map[string]any)
	service := properties["service"].(map[string]any)

	assert.Contains(t, tool.Description, "service.language")
	assert.Contains(t, tool.Description, "runtime.type=language")
	assert.Contains(t, service["description"], "service.language")
	assert.Contains(t, service["description"], "required for local managed runtime.type=language")
}

func TestUpsertProjectPipelinePassesApprovalToken(t *testing.T) {
	client := &fakeAgentClient{configPreview: ConfigChangePreview{Kind: "config.pipeline.upsert", Validation: ConfigChangeValidation{OK: true}}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "upsert_project_pipeline", `{"project_id":"p1","approval_token":"tok_1","pipeline":{"id":"deploy-dev","name":"Deploy Dev","pipeline":{}}}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
	assert.Equal(t, "config.pipeline.upsert", client.lastConfigChange.Kind)
}

func TestApplyConfigChangePreservesApprovalRequiredError(t *testing.T) {
	client := &fakeAgentClient{configApplyErr: AgentError{
		Code:     "approval_required",
		Message:  "approval required",
		Plan:     OperationPlan{ID: "op_cfg", Kind: "config.service.upsert"},
		Approval: OperationApproval{ID: "opa_cfg", Status: "pending"},
	}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "apply_config_change", `{"kind":"config.service.upsert","project_id":"p1","service":{"name":"api"},"approval_wait_seconds":0}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "approval_required", payload.Code)
	assert.Equal(t, "opa_cfg", payload.Data.(map[string]any)["approval"].(OperationApproval).ID)
}

func TestApplyConfigChangeWaitsForApproval(t *testing.T) {
	client := &fakeAgentClient{
		configPreview: ConfigChangePreview{Kind: "config.service.upsert", Validation: ConfigChangeValidation{OK: true}},
		configApplyErrs: []error{AgentError{
			Code:     "approval_required",
			Message:  "approval required",
			Plan:     OperationPlan{ID: "op_cfg", Kind: "config.service.upsert"},
			Approval: OperationApproval{ID: "opa_cfg", Status: "pending"},
		}},
		operationApprovalDetail: OperationApprovalDetail{
			Approval:      OperationApproval{ID: "opa_cfg", Status: "approved"},
			ApprovalToken: "tok_cfg",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "apply_config_change", `{"kind":"config.service.upsert","project_id":"p1","service":{"name":"api"},"approval_wait_seconds":1}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 2, client.configApplyCallCount)
	assert.Equal(t, "tok_cfg", client.lastApprovalToken)
}

func configChangePreviewWithDebugCredentials() ConfigChangePreview {
	return ConfigChangePreview{
		Kind:       "config.service.upsert",
		Validation: ConfigChangeValidation{OK: true},
		Project: model.Project{
			ID:       "p1",
			Name:     "Debuggable",
			AINote:   "Use seeded login",
			AuthHint: "Exchange login for session cookie",
			DebugCredentials: []model.DebugCredential{{
				Name:  "project_login",
				Value: "project-secret-value",
				Desc:  "Project login",
			}},
			Services: []model.Service{{
				ID:       "svc-api",
				Name:     "api",
				AINote:   "Use service override",
				AuthHint: "Send service bearer token",
				DebugCredentials: []model.DebugCredential{{
					Name:  "service_token",
					Value: "service-secret-value",
					Desc:  "Service token",
				}},
			}},
		},
	}
}

func assertConfigChangePreviewDebugCredentialsSanitized(t *testing.T, result CallToolResult) {
	t.Helper()

	payload := result.StructuredContent.(toolPayload)
	preview := payload.Data.(map[string]any)["preview"].(ConfigChangePreview)
	project := preview.Project
	assert.Equal(t, "Use seeded login", project.AINote)
	assert.Equal(t, "Exchange login for session cookie", project.AuthHint)
	assert.Empty(t, project.DebugCredentials)
	assert.True(t, project.HasDebugCredentials)
	require.Len(t, project.DebugCredentialHints, 1)
	assert.Equal(t, model.DebugCredentialHint{Name: "project_login", Desc: "Project login", Source: "project"}, project.DebugCredentialHints[0])

	require.Len(t, project.Services, 1)
	service := project.Services[0]
	assert.Equal(t, "Use service override", service.AINote)
	assert.Equal(t, "Send service bearer token", service.AuthHint)
	assert.Empty(t, service.DebugCredentials)
	assert.True(t, service.HasDebugCredentials)
	require.Len(t, service.DebugCredentialHints, 2)
	assert.Equal(t, []model.DebugCredentialHint{
		{Name: "project_login", Desc: "Project login", Source: "project"},
		{Name: "service_token", Desc: "Service token", Source: "service"},
	}, service.DebugCredentialHints)
	for _, hint := range append(project.DebugCredentialHints, service.DebugCredentialHints...) {
		assert.NotContains(t, hint.Name, "project-secret-value")
		assert.NotContains(t, hint.Desc, "project-secret-value")
		assert.NotContains(t, hint.Name, "service-secret-value")
		assert.NotContains(t, hint.Desc, "service-secret-value")
	}
}
