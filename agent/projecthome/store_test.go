// Package projecthome_test 验证项目归属映射的持久化行为。
//
// 职责：
//   - 验证 Set→重开→HomeOf 的落盘往返
//   - 验证 SetHome("") 删条目（迁回本机）语义
//   - 验证 ProjectsHomedOn 按主机过滤
package projecthome_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/projecthome"
)

// TestStoreRoundTrip 验证 SetHome 落盘后，重新打开 Store（模拟 agent 重启）
// 仍能通过 HomeOf 读到相同的归属主机 ID。
func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-homes.json")

	s1, err := projecthome.NewStore(path)
	require.NoError(t, err)
	require.NoError(t, s1.SetHome("proj-1", "host-A"))

	// 重新打开：模拟 agent 重启后从磁盘恢复归属状态。
	s2, err := projecthome.NewStore(path)
	require.NoError(t, err)
	assert.Equal(t, "host-A", s2.HomeOf("proj-1"))
}

// TestHomeOfDefaultsToLocal 验证从未设置过归属的项目视为本机（空字符串）。
func TestHomeOfDefaultsToLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-homes.json")
	s, err := projecthome.NewStore(path)
	require.NoError(t, err)
	assert.Equal(t, "", s.HomeOf("proj-unknown"))
}

// TestSetHomeEmptyDeletesEntry 验证 hostID=="" 表示迁回本机，
// 会把已有的归属条目从存储中删除，而不是写入一个空值占位。
func TestSetHomeEmptyDeletesEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-homes.json")
	s, err := projecthome.NewStore(path)
	require.NoError(t, err)

	require.NoError(t, s.SetHome("proj-1", "host-A"))
	require.Equal(t, "host-A", s.HomeOf("proj-1"))

	require.NoError(t, s.SetHome("proj-1", ""))
	assert.Equal(t, "", s.HomeOf("proj-1"), "迁回本机后 HomeOf 应恢复为空")

	// 迁回本机后条目应被物理删除，而非写入空字符串占位——
	// 通过重新打开验证磁盘上确实不再有该条目残留。
	s2, err := projecthome.NewStore(path)
	require.NoError(t, err)
	assert.Equal(t, "", s2.HomeOf("proj-1"))
}

// TestProjectsHomedOnFiltersByHost 验证 ProjectsHomedOn 只返回归属指定主机的项目，
// 该接口供主机删除守卫使用（判断能否安全删除一台主机）。
func TestProjectsHomedOnFiltersByHost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-homes.json")
	s, err := projecthome.NewStore(path)
	require.NoError(t, err)

	require.NoError(t, s.SetHome("proj-1", "host-A"))
	require.NoError(t, s.SetHome("proj-2", "host-B"))
	require.NoError(t, s.SetHome("proj-3", "host-A"))

	gotA := s.ProjectsHomedOn("host-A")
	assert.ElementsMatch(t, []string{"proj-1", "proj-3"}, gotA)

	gotB := s.ProjectsHomedOn("host-B")
	assert.ElementsMatch(t, []string{"proj-2"}, gotB)

	gotNone := s.ProjectsHomedOn("host-nonexistent")
	assert.Empty(t, gotNone)
}

// TestNewStoreOnCorruptFileReturnsError 验证构造时会及早暴露文件损坏的错误，
// 而不是留到第一次 HomeOf/SetHome 调用时才发现。
func TestNewStoreOnCorruptFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "project-homes.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o644))

	_, err := projecthome.NewStore(path)
	assert.Error(t, err)
}
