// Package mcp 验证流水线模板 MCP 工具。
//
// 职责：
//   - 验证模板 preview 错误映射
//   - 验证模板 import 参数校验和 agent 调用
//
// 边界：
//   - 不写真实模板库
package mcp

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewPipelineTemplateReturnsValidationErrorsAsToolError(t *testing.T) {
	client := &fakeAgentClient{
		templatePreview: PipelineTemplatePreview{Errors: []string{"id is required"}},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "preview_pipeline_template", `{"yaml":"name: Missing ID"}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestImportPipelineTemplateRequiresPath(t *testing.T) {
	server := NewServer(&fakeAgentClient{})

	result, err := server.callToolForTest(context.Background(), "import_pipeline_template", `{}`)

	require.NoError(t, err)
	assert.True(t, result.IsError)
}

func TestImportPipelineTemplateCallsAgent(t *testing.T) {
	client := &fakeAgentClient{
		importedTemplate: PipelineTemplateSummary{Source: "user", ID: "custom", Version: "1.0.0", Digest: "sha256:abc"},
	}
	server := NewServer(client)

	result, err := server.callToolForTest(context.Background(), "import_pipeline_template", `{"path":"/tmp/custom.yaml"}`)

	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, "/tmp/custom.yaml", client.importedTemplatePath)
}
