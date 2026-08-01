package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRelativizePath(t *testing.T) {
	root := "/Users/dev/proj"
	assert.Equal(t, "", RelativizePath("", root), "空串原样")
	assert.Equal(t, "server", RelativizePath("/Users/dev/proj/server", root), "根内绝对路径转相对")
	assert.Equal(t, ".", RelativizePath("/Users/dev/proj", root), "根本身转 .")
	assert.Equal(t, "/opt/other", RelativizePath("/opt/other", root), "根外绝对路径原样保留")
	assert.Equal(t, "/Users/dev/proj2/x", RelativizePath("/Users/dev/proj2/x", root), "同前缀但根外（proj2 不是 proj 子目录）")
	assert.Equal(t, "a/b", RelativizePath("a//b/", root), "已是相对路径仅 Clean")
}

func TestPathEscapesRoot(t *testing.T) {
	assert.False(t, PathEscapesRoot(""))
	assert.False(t, PathEscapesRoot("server/cmd"))
	assert.False(t, PathEscapesRoot("/abs/path"), "绝对路径不算逃逸（由相对化规则处理）")
	assert.True(t, PathEscapesRoot(".."))
	assert.True(t, PathEscapesRoot("../sibling"))
	assert.True(t, PathEscapesRoot("a/../../b"), "Clean 后逃逸")
	assert.False(t, PathEscapesRoot("a/../b"), "Clean 后不逃逸")
}
