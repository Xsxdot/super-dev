// Package plugins_test 验证内置归档插件。
//
// 职责：
//   - 验证 archive 插件能创建 tar.gz 产物
//   - 验证 archive 插件校验必填 source
//
// 边界：
//   - 不上传归档产物
//   - 不测试模板变量渲染
package plugins_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/pipeline/plugins"
)

func TestArchiveRequiresSource(t *testing.T) {
	p := plugins.NewArchive()
	err := p.Validate(model.Step{Name: "Archive", Type: "archive", With: map[string]interface{}{}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "with.source")
}

func TestArchiveCreatesTarGz(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "dist")
	require.NoError(t, os.MkdirAll(source, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(source, "app"), []byte("ok"), 0o644))
	dest := filepath.Join(dir, "out")

	p := plugins.NewArchive()
	step := model.Step{Name: "Archive", Type: "archive", With: map[string]interface{}{
		"source":   source,
		"dest":     dest,
		"basename": "api",
		"format":   "tar.gz",
	}}
	err := p.Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, nil)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dest, "api.tar.gz"))
}
