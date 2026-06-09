// settings_test.go 验证 agent 级 settings 的默认值、兼容加载与校验。
//
// 职责：
//   - 覆盖日志保留、容量上限和后台清理周期的范围校验
//   - 防止老 settings.json 缺少新字段时加载失败
//
// 边界：
//   - 只验证配置层，不启动 API 或日志清理任务
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
)

func TestValidateLogMaxBytes(t *testing.T) {
	settings := config.DefaultAgentSettings()
	settings.LogMaxBytes = config.MinLogMaxBytes - 1
	require.Error(t, config.ValidateAgentSettings(settings))

	settings.LogMaxBytes = config.DefaultLogMaxBytes
	require.NoError(t, config.ValidateAgentSettings(settings))
}

func TestLoadBackfillsLogCleanupDefaults(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"log_retention_days":7}`), 0o644))

	settings, err := config.NewSettingsStore(dir).Load()
	require.NoError(t, err)
	assert.Equal(t, int64(config.DefaultLogMaxBytes), settings.LogMaxBytes)
	assert.Equal(t, config.DefaultLogCleanupIntervalSeconds, settings.LogCleanupIntervalSeconds)
}
