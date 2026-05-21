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
	args, err := collector.BuildCommand(model.LogSourceTypeJournalctl, "nova-api")
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"journalctl", "-fu", "nova-api", "-o", "cat", "--no-pager"},
		args,
	)

	// docker 模板:docker logs -f <name>
	args, err = collector.BuildCommand(model.LogSourceTypeDocker, "nova-worker")
	require.NoError(t, err)
	assert.Equal(t, []string{"docker", "logs", "-f", "nova-worker"}, args)

	// 不允许的 type
	_, err = collector.BuildCommand(model.LogSourceType("file"), "anything")
	require.Error(t, err)

	// name 非法时整体失败
	_, err = collector.BuildCommand(model.LogSourceTypeJournalctl, "; rm -rf /")
	require.Error(t, err)
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
