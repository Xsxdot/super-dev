// Package collector_test 验证命令模板和 name 校验逻辑。
package collector_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
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

	// docker 模板:docker logs -f <name>
	args, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-worker", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "nova-worker"}, args)

	// 不允许的 type
	_, err = collector.BuildCommand(model.LogSourceType("file"), "anything", nil)
	require.Error(t, err)

	// name 非法时整体失败
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "; rm -rf /", nil)
	require.Error(t, err)
}

func TestBuildCommandExtraArgs(t *testing.T) {
	// 合法的 extra args 正常追加
	argv, err := collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h"})
	require.NoError(t, err)
	assert.Equal(t, []string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager", "--since", "1h"}, argv)

	// docker 同样支持
	argv, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-api", []string{"--tail", "100"})
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "nova-api", "--tail", "100"}, argv)

	// 非法 arg（无 -- 前缀，纯字母单词）被拒绝
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"rm"})
	assert.Error(t, err)

	// 非法 arg（含注入字符）被拒绝
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api", []string{"--since", "1h; rm -rf /"})
	assert.Error(t, err)

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
