// Package template_test 验证流水线模板 preview 纯函数。
//
// 职责：
//   - 验证模板 YAML preview 不写入模板库
//   - 验证 preview 返回 digest、模板结构和校验错误
//
// 边界：
//   - 不测试 HTTP handler
//   - 不执行流水线步骤
package template_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinetemplate "github.com/xsxdot/super-dev/agent/template"
)

func TestPreviewYAMLReturnsTemplateDigestAndNoErrors(t *testing.T) {
	yamlText := []byte(`
id: custom-build
name: Custom Build
version: 1.0.0
inputs:
  app:
    label: App
    type: string
    required: true
steps:
  - name: Build
    type: local_command
    with:
      cmd: go build ./...
`)

	preview := pipelinetemplate.PreviewYAML(yamlText)

	require.Empty(t, preview.Errors)
	assert.Equal(t, "custom-build", preview.Template.ID)
	assert.Equal(t, "Custom Build", preview.Template.Name)
	assert.NotEmpty(t, preview.Digest)
}

func TestPreviewYAMLReturnsValidationErrors(t *testing.T) {
	preview := pipelinetemplate.PreviewYAML([]byte(`name: Missing ID`))

	require.NotEmpty(t, preview.Errors)
	assert.Contains(t, preview.Errors[0], "id is required")
	assert.Empty(t, preview.Digest)
}

func TestPreviewFileReadsTemplateWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "template.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
id: file-build
name: File Build
version: 1.0.0
steps:
  - name: Build
    type: local_command
    with:
      cmd: go test ./...
`), 0o644))

	preview := pipelinetemplate.PreviewFile(path)

	require.Empty(t, preview.Errors)
	assert.Equal(t, "file-build", preview.Template.ID)
	assert.NotEmpty(t, preview.Digest)
}
