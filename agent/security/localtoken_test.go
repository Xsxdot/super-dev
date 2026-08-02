package security_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/security"
)

func TestRotateLocalTokenWritesOwnerOnlyFileAndInvalidatesOld(t *testing.T) {
	dir := t.TempDir()

	first, err := security.RotateLocalToken(dir)
	require.NoError(t, err)
	require.Len(t, first, 64, "GenerateToken 产 64 位 hex")

	info, err := os.Stat(security.LocalTokenPath(dir))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "仅属主可读写——文件系统权限即信任边界")

	read, err := security.ReadLocalToken(dir)
	require.NoError(t, err)
	require.Equal(t, first, read, "ReadLocalToken 去除尾部换行后应与生成值一致")

	second, err := security.RotateLocalToken(dir)
	require.NoError(t, err)
	require.NotEqual(t, first, second, "轮换必须换值")

	store, err := security.NewStore(filepath.Join(dir, "security.json"), security.Options{})
	require.NoError(t, err)
	store.SetLocalToken(second)
	require.True(t, store.VerifyLocalToken(second))
	require.False(t, store.VerifyLocalToken(first), "旧 token 轮换后必须失效")
	require.False(t, store.VerifyLocalToken(""))
	require.Equal(t, second, store.LocalToken())
}

func TestVerifyLocalTokenWithoutInjectionRejectsEverything(t *testing.T) {
	store, err := security.NewStore(filepath.Join(t.TempDir(), "security.json"), security.Options{})
	require.NoError(t, err)
	require.False(t, store.VerifyLocalToken("anything"), "未注入时宁可拒绝也不放行")
}

func TestRotateLocalTokenFixesLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := security.LocalTokenPath(dir)
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

	_, err := security.RotateLocalToken(dir)
	require.NoError(t, err)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "历史宽权限文件必须被收紧")

	// 原子替换路径下，rename 成功后目录里只该剩最终文件，不许残留 .tmp-* 中间产物。
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "轮换成功后不应残留临时文件")
}

func TestReadLocalTokenMissingFileReturnsError(t *testing.T) {
	_, err := security.ReadLocalToken(t.TempDir())
	require.Error(t, err)
}
