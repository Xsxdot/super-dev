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

func TestConfigChangeSchemaDocumentsCanonicalHostIDs(t *testing.T) {
	server := NewServer(&fakeAgentClient{})
	schema := server.tools["preview_config_change"].Tool.InputSchema
	properties := schema["properties"].(map[string]any)

	service := properties["service"].(map[string]any)

	assert.Contains(t, service["description"], "list_hosts")
	assert.Contains(t, service["description"], "Host.id")
	assert.Contains(t, service["description"], "not host name")
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
