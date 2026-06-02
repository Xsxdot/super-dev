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

	result, err := server.callToolForTest(context.Background(), "apply_config_change", `{"kind":"config.service.upsert","project_id":"p1","service":{"name":"api"}}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
	payload := result.StructuredContent.(toolErrorPayload)
	assert.Equal(t, "approval_required", payload.Code)
	assert.Equal(t, "opa_cfg", payload.Data.(map[string]any)["approval"].(OperationApproval).ID)
}
