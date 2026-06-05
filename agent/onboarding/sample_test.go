// Package onboarding_test 验证 agent 首启示例项目落地逻辑。
//
// 职责：
//   - 验证示例首次复制、注册、标记
//   - 验证幂等跳过和缺失二进制时不阻塞 agent
//
// 边界：
//   - 不启动 HTTP API
//   - 不启动示例服务进程
package onboarding_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/onboarding"
)

type fakeRegistry struct {
	paths []string
}

func (f *fakeRegistry) Add(rootPath string) error {
	f.paths = append(f.paths, rootPath)
	return nil
}

func TestSeedSampleProjectCopiesRegistersAndMarks(t *testing.T) {
	dataDir := t.TempDir()
	bin := filepath.Join(dataDir, "superdev-sample")
	require.NoError(t, os.WriteFile(bin, []byte("bin"), 0o755))
	reg := &fakeRegistry{}
	settings := config.NewSettingsStore(dataDir)

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: bin,
		Registry:         reg,
		Settings:         settings,
	})

	require.NoError(t, err)
	assert.True(t, result.Seeded)
	assert.Len(t, reg.paths, 1)
	projectDir := filepath.Join(dataDir, "examples", "superdev-sample")
	assert.Equal(t, projectDir, reg.paths[0])
	raw, err := os.ReadFile(filepath.Join(projectDir, ".superdev", "config.yaml"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), bin)
	assert.Contains(t, string(raw), "is_dev: false")
	loaded, err := settings.Load()
	require.NoError(t, err)
	assert.True(t, loaded.SampleSeeded)
}

func TestSeedSampleProjectSkipsWhenAlreadyMarked(t *testing.T) {
	dataDir := t.TempDir()
	settings := config.NewSettingsStore(dataDir)
	require.NoError(t, settings.Save(config.AgentSettings{LogRetentionDays: 7, SampleSeeded: true}))
	reg := &fakeRegistry{}

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: filepath.Join(dataDir, "missing"),
		Registry:         reg,
		Settings:         settings,
	})

	require.NoError(t, err)
	assert.False(t, result.Seeded)
	assert.Equal(t, "already_seeded", result.Reason)
	assert.Empty(t, reg.paths)
}

func TestSeedSampleProjectSkipsMissingBinary(t *testing.T) {
	dataDir := t.TempDir()
	settings := config.NewSettingsStore(dataDir)
	reg := &fakeRegistry{}

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: filepath.Join(dataDir, "missing"),
		Registry:         reg,
		Settings:         settings,
	})

	require.NoError(t, err)
	assert.False(t, result.Seeded)
	assert.Equal(t, "sample_binary_missing", result.Reason)
	assert.Empty(t, reg.paths)
}
