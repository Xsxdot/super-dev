// Package template_test 验证模板 Store 的导入、草稿与发布行为。
//
// 职责：
//   - 验证同版本不同 digest 不能覆盖
//   - 验证草稿发布后版本不可变
//
// 边界：
//   - 不测试 builtin embed
//   - 不通过 HTTP API 访问模板 Store
package template_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pipelinetemplate "github.com/superdev/agent/template"
	"gopkg.in/yaml.v3"
)

func writeTemplateFile(t *testing.T, path string, tpl pipelinetemplate.Template) {
	t.Helper()
	data, err := yaml.Marshal(tpl)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

func TestStoreImportRejectsSameVersionDifferentDigest(t *testing.T) {
	dir := t.TempDir()
	store := pipelinetemplate.NewStore(dir, nil, "")
	first := pipelinetemplate.Template{
		ID: "go-build", Name: "Go Build", Version: "1.0.0",
		Steps: []pipelinetemplate.Step{{Name: "Build", Type: "local_command", With: map[string]interface{}{"cmd": "go build"}}},
	}
	second := pipelinetemplate.Template{
		ID: "go-build", Name: "Go Build", Version: "1.0.0",
		Steps: []pipelinetemplate.Step{{Name: "Build", Type: "local_command", With: map[string]interface{}{"cmd": "go test ./..."}}},
	}
	firstPath := filepath.Join(dir, "first.yaml")
	secondPath := filepath.Join(dir, "second.yaml")
	writeTemplateFile(t, firstPath, first)
	writeTemplateFile(t, secondPath, second)

	_, err := store.ImportFile(firstPath)
	require.NoError(t, err)
	_, err = store.ImportFile(secondPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different digest")
}

func TestDraftPublishCreatesImmutableVersion(t *testing.T) {
	dir := t.TempDir()
	store := pipelinetemplate.NewStore(dir, nil, "")
	tpl := pipelinetemplate.Template{
		ID: "go-build", Name: "Go Build", Version: "1.0.0",
		Steps: []pipelinetemplate.Step{{Name: "Build", Type: "local_command", With: map[string]interface{}{"cmd": "go build"}}},
	}
	require.NoError(t, store.SaveDraft("user", tpl))
	published, err := store.PublishDraft("user", "go-build", "1.0.0")
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", published.Template.Version)
	assert.Contains(t, published.Digest, "sha256:")

	tpl.Steps[0].With["cmd"] = "go test ./..."
	require.NoError(t, store.SaveDraft("user", tpl))
	_, err = store.PublishDraft("user", "go-build", "1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}
