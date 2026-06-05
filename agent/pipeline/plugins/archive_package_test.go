// Package plugins_test 验证多文件归档插件。
//
// 职责：
//   - 验证 archive_package 能按 files 清单创建 tar.gz 产物
//   - 验证归档内路径禁止穿越目标目录
//
// 边界：
//   - 不上传归档产物
//   - 不测试模板变量渲染
package plugins_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
	"github.com/xsxdot/super-dev/agent/pipeline/plugins"
)

func TestArchivePackageCreatesTarGzFromFiles(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "bin", "api")
	config := filepath.Join(dir, "config", "app.env")
	require.NoError(t, os.MkdirAll(filepath.Dir(binary), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Dir(config), 0o755))
	require.NoError(t, os.WriteFile(binary, []byte("binary"), 0o755))
	require.NoError(t, os.WriteFile(config, []byte("PORT=8080"), 0o644))
	artifact := filepath.Join(dir, "out", "api.tar.gz")

	step := model.Step{Name: "Package", Type: "archive_package", With: map[string]interface{}{
		"artifact": artifact,
		"format":   "tar.gz",
		"files": []interface{}{
			map[string]interface{}{"from": binary, "to": "bin/api"},
			map[string]interface{}{"from": config, "to": "config/app.env"},
		},
	}}

	err := plugins.NewArchivePackage().Execute(pipeline.NewRunContext(context.Background(), pipeline.RunContextOptions{}), step, nil)

	require.NoError(t, err)
	entries := readTarGzEntries(t, artifact)
	assert.Equal(t, "binary", entries["bin/api"])
	assert.Equal(t, "PORT=8080", entries["config/app.env"])
}

func TestArchivePackageRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "api")
	require.NoError(t, os.WriteFile(source, []byte("ok"), 0o644))
	step := model.Step{Name: "Package", Type: "archive_package", With: map[string]interface{}{
		"artifact": filepath.Join(dir, "api.tar.gz"),
		"files": []interface{}{
			map[string]interface{}{"from": source, "to": "../api"},
		},
	}}

	err := plugins.NewArchivePackage().Validate(step)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func readTarGzEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	gr, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer gr.Close()
	tr := tar.NewReader(gr)
	entries := map[string]string{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if header.FileInfo().IsDir() {
			continue
		}
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		entries[header.Name] = string(data)
	}
	return entries
}
