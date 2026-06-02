// Package mcp 验证 operation 安全 MCP 工具。
//
// 职责：
//   - 验证预检、审批查询和审计查询工具
//   - 验证写工具遇到 approval_required 时不吞掉结构化信息
//
// 边界：
//   - 不访问真实 agent HTTP 服务
//   - 不执行真实进程
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestPreviewOperationToolReturnsPlan(t *testing.T) {
	client := &fakeAgentClient{
		operationPlan: OperationPlan{ID: "op_1", Kind: "runtime.restart", RequiresApproval: true, Fingerprint: "fp_1"},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "preview_operation", `{"kind":"runtime.restart","deployment_id":"api-prod"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	assert.Equal(t, "op_1", payload.Data.(map[string]any)["plan"].(OperationPlan).ID)
}

func TestGetOperationApprovalReturnsTokenWhenApproved(t *testing.T) {
	client := &fakeAgentClient{
		operationApprovalDetail: OperationApprovalDetail{
			Approval:      OperationApproval{ID: "opa_1", Status: "approved"},
			ApprovalToken: "tok_1",
		},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "get_operation_approval", `{"approval_id":"opa_1"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	payload := result.StructuredContent.(toolPayload)
	assert.Equal(t, "tok_1", payload.Data.(map[string]any)["approval_token"])
}

func TestRestartServicePassesApprovalToken(t *testing.T) {
	client := &fakeAgentClient{projects: []model.Project{sampleProject()}}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "restart_service", `{"deployment_id":"dep-api-prod","approval_token":"tok_1"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "tok_1", client.lastApprovalToken)
}
