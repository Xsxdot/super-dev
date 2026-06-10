// Package collector_test 验证命令模板和 name 校验逻辑。
package collector_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"nova-api", true},
		{"nova_api.service", true},
		{"abc.123", true},
		{"", false},
		{"nova-api; rm -rf /", false},
		{"nova api", false},  // 含空格
		{"$(whoami)", false}, // 命令替换
		{"nova-api`id`", false},
		{"../escape", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := collector.ValidateName(c.name)
			if c.ok {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestBuildCommand(t *testing.T) {
	// journalctl 模板:journalctl -fu <name> -o cat --no-pager
	args, err := collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", nil)
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager"},
		args,
	)

	// macOS unified log 模板:log stream --predicate <label>
	args, err = collector.BuildCommand(model.LogSourceTypeMacOSLog, "com.example.api", nil)
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"log", "stream", "--style", "compact", "--predicate", `subsystem == "com.example.api" OR process == "com.example.api" OR eventMessage CONTAINS[c] "com.example.api"`},
		args,
	)

	// docker 模板:docker logs -f --tail 0 <name>,避免 collector 重启后重放容器历史日志。
	args, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-worker", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "--tail", "0", "nova-worker"}, args)

	// 不允许的 type
	_, err = collector.BuildCommand(model.LogSourceType("file"), "anything", nil)
	require.Error(t, err)

	// name 非法时整体失败
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "; rm -rf /", nil)
	require.Error(t, err)
}

func TestBuildCommandSupportsFileTailAndCustomCommand(t *testing.T) {
	args, err := collector.BuildCommand(model.LogSourceTypeFileTail, "/var/log/nova-api/app.log", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"tail", "-F", "/var/log/nova-api/app.log"}, args)

	home := t.TempDir()
	t.Setenv("HOME", home)
	args, err = collector.BuildCommand(model.LogSourceTypeFileTail, "~/Library/Logs/nova-api/app.log", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"tail", "-F", filepath.Join(home, "Library/Logs/nova-api/app.log")}, args)

	args, err = collector.BuildCommand(model.LogSourceTypeCommand, "tail -F /var/log/nova-api/app.log", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"sh", "-lc", "tail -F /var/log/nova-api/app.log"}, args)
}

func TestNormalizeFileTailPathRejectsUnsafePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	_, err := collector.NormalizeFileTailPath("relative/app.log")
	require.ErrorIs(t, err, collector.ErrInvalidPath)

	_, err = collector.NormalizeFileTailPath("~/Library/Logs/app.log; rm -rf /")
	require.ErrorIs(t, err, collector.ErrInvalidPath)

	got, err := collector.NormalizeFileTailPath("~")
	require.NoError(t, err)
	assert.Equal(t, os.Getenv("HOME"), got)
}

func TestBuildCommandExtraArgs(t *testing.T) {
	// 合法的 extra args 正常追加
	argv, err := collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h"})
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager", "--since", "1h"}, argv)

	// docker 同样支持,extra args 追加在默认模板后。
	argv, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-api", []string{"--tail", "100"})
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "--tail", "0", "nova-api", "--tail", "100"}, argv)

	// 非法 arg（含空格注入字符）被拒绝
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h; rm -rf /"})
	assert.Error(t, err)

	// 非法 arg（含 $ 字符）被拒绝
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--priority", "$(id)"})
	assert.Error(t, err)

	// 合法纯字母值（如 --output cat, --priority err）被允许
	argv, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--output", "cat"})
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager", "--output", "cat"}, argv)

	argv, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--priority", "err"})
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager", "--priority", "err"}, argv)

	// 空 extra args 不影响结果
	argv, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager"}, argv)
}

func TestCollectorID(t *testing.T) {
	// 相同 (name, type) → 同一 ID（幂等）
	a := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	b := collector.CollectorID("nova-api", model.LogSourceTypeJournalctl)
	assert.Equal(t, a, b)

	// 不同 type → 不同 ID
	c := collector.CollectorID("nova-api", model.LogSourceTypeDocker)
	assert.NotEqual(t, a, c)
}
