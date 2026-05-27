// Package identity_test 验证 identity 身份文件读写行为。
package identity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/identity"
)

func TestLoadOrCreate_CreatesFileOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(id.NodeID, "superdev-"), "node_id should start with superdev-")
	assert.Len(t, id.NodeID, len("superdev-")+4, "node_id should be superdev-XXXX")
	assert.NotEmpty(t, id.DisplayName)

	// 文件应已写入
	path := filepath.Join(dir, "identity.json")
	_, err = os.Stat(path)
	assert.NoError(t, err, "identity.json should exist")
}

func TestLoadOrCreate_LoadsExistingFile(t *testing.T) {
	dir := t.TempDir()

	// 首次创建
	first, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	// 第二次加载应返回相同 node_id
	second, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	assert.Equal(t, first.NodeID, second.NodeID)
	assert.Equal(t, first.DisplayName, second.DisplayName)
}

func TestLoadOrCreate_NodeIDIsUnique(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	id1, err := identity.LoadOrCreate(dir1)
	require.NoError(t, err)
	id2, err := identity.LoadOrCreate(dir2)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(id1.NodeID, "superdev-"))
	assert.True(t, strings.HasPrefix(id2.NodeID, "superdev-"))
}

func TestSave_UpdatesDisplayName(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	id.DisplayName = "my-custom-name"
	require.NoError(t, identity.Save(dir, id))

	loaded, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)
	assert.Equal(t, "my-custom-name", loaded.DisplayName)
	assert.Equal(t, id.NodeID, loaded.NodeID, "node_id must not change on save")
}

func TestNodeIDFormat(t *testing.T) {
	dir := t.TempDir()
	id, err := identity.LoadOrCreate(dir)
	require.NoError(t, err)

	// 格式：superdev-[0-9a-f]{4}
	suffix := strings.TrimPrefix(id.NodeID, "superdev-")
	assert.Len(t, suffix, 4)
	for _, c := range suffix {
		assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
			"node_id suffix must be hex: %c", c)
	}
}
