// Package onboarding_test 验证 agent 首启示例项目落地逻辑。
//
// 职责：
//   - 验证示例首次复制、注册、标记
//   - 验证幂等跳过和缺失二进制时不阻塞 agent
//   - 验证 Windows 路径序列化与旧版坏配置升级修复
//
// 边界：
//   - 不启动 HTTP API
//   - 不启动示例服务进程
package onboarding_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/config"
	"github.com/xsxdot/super-dev/agent/onboarding"
)

const legacySampleConfigTemplate = `name: superdev-sample
environments:
  - name: demo
    is_dev: false
    order: 1
env_selected_service_ids:
  demo:
    - sample-api
services:
  - id: sample-api
    name: sample-api
    required: true
    order: 1
    deployments:
      - id: sample-api-demo
        env: demo
        location: local
        control_mode: managed
        command: "{{SAMPLE_BINARY}} --port 18191"
        working_dir: "."
        runtime:
          type: command
          command: "{{SAMPLE_BINARY}} --port 18191"
          working_dir: "."
        logs:
          type: process
`

type fakeRegistry struct {
	paths []string
}

func testWindowsSampleBinaryPath(t *testing.T, dataDir string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		bin := filepath.Join(dataDir, "Program Files", "SuperDev", "superdev-sample.exe")
		require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o755))
		return bin
	}
	// 非 Windows 测试机把反斜杠保留为文件名字符，以复现 Windows 路径进入 YAML 的输入。
	return filepath.Join(dataDir, `C:\Users\alice\AppData\Local\SuperDev\superdev-sample.exe`)
}

func legacySampleConfig(binaryPath string) string {
	return strings.ReplaceAll(legacySampleConfigTemplate, "{{SAMPLE_BINARY}}", binaryPath)
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
	assert.Equal(t, onboarding.SampleSeedOutcomeSeeded, result.Outcome)
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

// TestSeedSampleProjectWritesLoadableConfigForWindowsBinaryPath 验证 Windows 路径不会破坏示例 YAML。
func TestSeedSampleProjectWritesLoadableConfigForWindowsBinaryPath(t *testing.T) {
	dataDir := t.TempDir()
	bin := testWindowsSampleBinaryPath(t, dataDir)
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
	require.Equal(t, onboarding.SampleSeedOutcomeSeeded, result.Outcome)
	projectDir := filepath.Join(dataDir, "examples", "superdev-sample")
	project, err := config.NewLoader(projectDir).Load()
	require.NoError(t, err)
	require.Len(t, project.Services, 1)
	require.Len(t, project.Services[0].Deployments, 1)
	assert.Equal(t, `"`+bin+`" --port 18191`, project.Services[0].Deployments[0].Command)
}

// TestSeedSampleProjectRepairsLegacyWindowsConfigMarkedSeeded 验证升级后会修复旧版半完成状态。
func TestSeedSampleProjectRepairsLegacyWindowsConfigMarkedSeeded(t *testing.T) {
	dataDir := t.TempDir()
	bin := testWindowsSampleBinaryPath(t, dataDir)
	require.NoError(t, os.WriteFile(bin, []byte("bin"), 0o755))
	projectDir := filepath.Join(dataDir, "examples", "superdev-sample")
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".superdev"), 0o755))
	legacyConfig := legacySampleConfig(bin)
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".superdev", "config.yaml"), []byte(legacyConfig), 0o644))
	settings := config.NewSettingsStore(dataDir)
	initial := config.DefaultAgentSettings()
	initial.SampleSeeded = true
	require.NoError(t, settings.Save(initial))
	reg := &fakeRegistry{}

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: bin,
		Registry:         reg,
		Settings:         settings,
	})

	require.NoError(t, err)
	assert.Equal(t, onboarding.SampleSeedOutcomeRepaired, result.Outcome)
	assert.Empty(t, reg.paths, "修复已有配置不能重新添加用户已主动移除的 registry 路径")
	project, err := config.NewLoader(projectDir).Load()
	require.NoError(t, err)
	require.Len(t, project.Services, 1)
	require.Len(t, project.Services[0].Deployments, 1)
	assert.Equal(t, `"`+bin+`" --port 18191`, project.Services[0].Deployments[0].Command)
}

// TestSeedSampleProjectPreservesUserModifiedInvalidConfig 验证自动修复不会覆盖用户自定义内容。
func TestSeedSampleProjectPreservesUserModifiedInvalidConfig(t *testing.T) {
	dataDir := t.TempDir()
	bin := testWindowsSampleBinaryPath(t, dataDir)
	require.NoError(t, os.WriteFile(bin, []byte("bin"), 0o755))
	projectDir := filepath.Join(dataDir, "examples", "superdev-sample")
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".superdev"), 0o755))
	configPath := filepath.Join(projectDir, ".superdev", "config.yaml")
	// 两个 command 都被用户改成相同自定义路径时，形状仍与旧模板一致，但绝不能当成内置生成物覆盖。
	userConfig := []byte(legacySampleConfig(`C:\Custom Tools\SuperDev\my-sample.exe`))
	require.NoError(t, os.WriteFile(configPath, userConfig, 0o644))
	settings := config.NewSettingsStore(dataDir)
	initial := config.DefaultAgentSettings()
	initial.SampleSeeded = true
	require.NoError(t, settings.Save(initial))
	reg := &fakeRegistry{}

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: bin,
		Registry:         reg,
		Settings:         settings,
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "preserved")
	assert.Empty(t, result.Outcome)
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, userConfig, raw)
	assert.Empty(t, reg.paths)
}

func TestSeedSampleProjectSkipsWhenAlreadyMarked(t *testing.T) {
	dataDir := t.TempDir()
	settings := config.NewSettingsStore(dataDir)
	initial := config.DefaultAgentSettings()
	initial.SampleSeeded = true
	require.NoError(t, settings.Save(initial))
	reg := &fakeRegistry{}

	result, err := onboarding.SeedSampleProject(onboarding.SampleSeedConfig{
		DataDir:          dataDir,
		SampleBinaryPath: filepath.Join(dataDir, "missing"),
		Registry:         reg,
		Settings:         settings,
	})

	require.NoError(t, err)
	assert.Equal(t, onboarding.SampleSeedOutcomeSkipped, result.Outcome)
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
	assert.Equal(t, onboarding.SampleSeedOutcomeSkipped, result.Outcome)
	assert.Equal(t, "sample_binary_missing", result.Reason)
	assert.Empty(t, reg.paths)
}
