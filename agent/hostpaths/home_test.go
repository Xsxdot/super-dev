// hostpaths home 解析测试。
//
// 职责：
//   - 钉住 $HOME 被清空时 UserHome 仍能从 passwd 回落到真实存在的目录
//   - 钉住 $HOME 有值时优先跟随环境，不误走 passwd
//
// 边界：
//   - 不覆盖 systemd unit / launchd plist 的 HOME 注入，那是 installer 的测试
package hostpaths

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserHomeFallsBackWhenHomeEmpty(t *testing.T) {
	t.Setenv("HOME", "")

	home, err := UserHome()
	require.NoError(t, err)
	require.NotEmpty(t, home)

	info, err := os.Stat(home)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "回落得到的 home 必须是已存在的目录，got=%q", home)
}

func TestUserHomePrefersEnvWhenSet(t *testing.T) {
	want := t.TempDir()
	t.Setenv("HOME", want)

	home, err := UserHome()
	require.NoError(t, err)
	require.Equal(t, want, home)
}
