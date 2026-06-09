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

func TestDefaultAgentSettingsApprovalPolicy(t *testing.T) {
	s := config.DefaultAgentSettings()
	if !s.Approval.ConfigUpsert || !s.Approval.PipelineUpsert ||
		!s.Approval.PipelineRun || !s.Approval.TemplateImport {
		t.Fatalf("default approval switches must all be true, got %+v", s.Approval)
	}
	if s.Approval.GraceMinutes != config.DefaultGraceMinutes {
		t.Fatalf("default grace minutes = %d, want %d", s.Approval.GraceMinutes, config.DefaultGraceMinutes)
	}
}

func TestLoadLegacySettingsFillsApprovalDefaults(t *testing.T) {
	dir := t.TempDir()
	// 旧文件：没有 approval 字段
	legacy := `{"log_retention_days":7,"log_max_bytes":268435456,"log_cleanup_interval_seconds":3600}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o644))

	got, err := config.NewSettingsStore(dir).Load()
	require.NoError(t, err)
	if !got.Approval.ConfigUpsert || !got.Approval.PipelineRun {
		t.Fatalf("legacy load must default switches to true, got %+v", got.Approval)
	}
	if got.Approval.GraceMinutes != config.DefaultGraceMinutes {
		t.Fatalf("legacy grace minutes = %d, want %d", got.Approval.GraceMinutes, config.DefaultGraceMinutes)
	}
}

func TestLoadExplicitFalseSwitchPreserved(t *testing.T) {
	dir := t.TempDir()
	raw := `{"log_retention_days":7,"log_max_bytes":268435456,"log_cleanup_interval_seconds":3600,"approval":{"config_upsert":false,"pipeline_upsert":true,"pipeline_run":true,"template_import":true,"grace_minutes":30}}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0o644))

	got, err := config.NewSettingsStore(dir).Load()
	require.NoError(t, err)
	if got.Approval.ConfigUpsert {
		t.Fatal("explicit false config_upsert must be preserved")
	}
	if got.Approval.GraceMinutes != 30 {
		t.Fatalf("grace minutes = %d, want 30", got.Approval.GraceMinutes)
	}
}

func TestValidateGraceMinutesRange(t *testing.T) {
	s := config.DefaultAgentSettings()
	s.Approval.GraceMinutes = 0
	if err := config.ValidateAgentSettings(s); err == nil {
		t.Fatal("grace_minutes=0 must fail validation")
	}
	s.Approval.GraceMinutes = config.MaxGraceMinutes + 1
	if err := config.ValidateAgentSettings(s); err == nil {
		t.Fatal("grace_minutes above max must fail validation")
	}
}
