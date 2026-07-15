//go:build windows

// environment_readers_windows_test.go 验证 Windows batch probe 与受信系统 executor 的真实进程边界。
//
// 职责：
//   - 在含空格和非 ASCII 路径真实执行固定 npm.cmd/kotlinc.bat 版本 probe
//   - 锁定 cmd.exe SysProcAttr.CmdLine quoting 与 System32 PowerShell/cmd identity
//   - 证明批处理路径中的 cmd 元字符在启动前被拒绝
//
// 边界：
//   - 只执行测试临时目录中的无副作用 batch
//   - 不运行安装器、不修改产品配置，也不访问网络
package windowsvalidation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWindowsEnvironmentBatchProbeExecutesFixedScriptsWithTrustedCmd(t *testing.T) {
	shadowDirectory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(shadowDirectory, "cmd.exe"), []byte("shadow"), 0o644))
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := filepath.Join(t.TempDir(), "批处理 合同")
	require.NoError(t, os.MkdirAll(root, 0o755))
	tests := []struct {
		name     string
		filename string
		command  EnvironmentCommand
		output   string
	}{
		{
			name: "npm cmd", filename: "npm.cmd", output: "11.16.0",
			command: EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version"}},
		},
		{
			name: "kotlinc bat", filename: "kotlinc.bat", output: "2.4.0",
			command: EnvironmentCommand{Key: EnvironmentKeyToolchainKotlin, Executable: "kotlinc", Arguments: []string{"-version"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scriptPath := filepath.Join(root, test.filename)
			script := "@echo off\r\nif not \"%~1\"==\"" + test.command.Arguments[0] + "\" exit /b 91\r\necho " + test.output + "\r\n"
			require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o644))
			invocation, err := environmentCommandInvocationForOS("windows", test.command, scriptPath)
			require.NoError(t, err)
			process, err := newEnvironmentCommandProcess(context.Background(), invocation)
			require.NoError(t, err)
			trustedCmd, err := trustedWindowsCommandPath()
			require.NoError(t, err)
			assert.Equal(t, trustedCmd, process.Path)
			output, err := process.CombinedOutput()
			require.NoError(t, err, string(output))
			assert.Equal(t, test.output, strings.TrimSpace(string(output)))
		})
	}
}

func TestWindowsEnvironmentBatchProbeRejectsMetacharacterInjectionBeforeStart(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "injected-marker")
	command := EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version"}}
	for _, directory := range []string{"unsafe&mkdir injected-marker&", "unsafe!payload"} {
		unsafeDirectory := filepath.Join(root, directory)
		require.NoError(t, os.MkdirAll(unsafeDirectory, 0o755))
		scriptPath := filepath.Join(unsafeDirectory, "npm.cmd")
		require.NoError(t, os.WriteFile(scriptPath, []byte("@echo 11.16.0\r\n"), 0o644))

		invocation, err := environmentCommandInvocationForOS("windows", command, scriptPath)
		if err == nil {
			process, processErr := newEnvironmentCommandProcess(context.Background(), invocation)
			if processErr == nil {
				process.Dir = root
				_, _ = process.CombinedOutput()
			}
		}
		require.ErrorContains(t, err, "unsafe characters")
		_, markerErr := os.Stat(marker)
		assert.ErrorIs(t, markerErr, os.ErrNotExist)
	}
}

func TestWindowsEnvironmentPowerShellResolutionIgnoresPATHShadow(t *testing.T) {
	shadowDirectory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(shadowDirectory, "powershell.exe"), []byte("shadow"), 0o644))
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SystemRoot", shadowDirectory)

	expected, err := trustedWindowsPowerShellPath()
	require.NoError(t, err)
	resolved, source, err := resolveEnvironmentExecutable("powershell.exe")
	require.NoError(t, err)
	assert.Equal(t, expected, resolved)
	assert.Equal(t, "well_known_path", source)
}

func TestWindowsTrustedCmdIgnoresForgedEnvironmentIdentity(t *testing.T) {
	expected, err := trustedWindowsCommandPath()
	require.NoError(t, err)
	shadowDirectory := t.TempDir()
	shadowCmd := filepath.Join(shadowDirectory, "cmd.exe")
	require.NoError(t, os.WriteFile(shadowCmd, []byte("shadow"), 0o644))
	t.Setenv("SystemRoot", shadowDirectory)
	t.Setenv("ComSpec", shadowCmd)
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))

	actual, err := trustedWindowsCommandPath()
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
	assert.NotEqual(t, shadowCmd, actual)
}

func TestWindowsFixturePreflightChildUsesAdmittedBindingsInsteadOfAmbientEnvironment(t *testing.T) {
	root := t.TempDir()
	shadowDirectory := filepath.Join(root, "path-shadow")
	require.NoError(t, os.MkdirAll(shadowDirectory, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(shadowDirectory, "cmd.exe"), []byte("shadow"), 0o644))
	t.Setenv("PATH", shadowDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))
	jvmCommand := filepath.Join(root, "adapter space", "jvm-wrapper.exe")
	nodeCommand := filepath.Join(root, "node space", "node.exe")
	agentData := filepath.Join(root, "fresh profile", ".superdev")
	for _, path := range []string{jvmCommand, nodeCommand} {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("identity"), 0o644))
	}
	require.NoError(t, os.MkdirAll(agentData, 0o755))
	t.Setenv("SUPERDEV_JVM_ADAPTER_COMMAND", filepath.Join(root, "ambient-wrong.exe"))
	t.Setenv("SUPERDEV_AGENT_DATA_DIR", filepath.Join(root, "ambient-wrong-data"))

	executor := &ScenarioExecutor{
		campaignID: "windows-child-env", lane: "nsis_core", variables: map[string]any{},
		agentDataDirectory: agentData,
		providerAdapters: map[string]providerAdapterBinding{
			"java": {PrerequisiteKey: EnvironmentKeyAdapterJVM, Command: jvmCommand, Source: "explicit"},
			"node": {PrerequisiteKey: EnvironmentKeyAdapterNode, Command: nodeCommand, Source: "path_fallback"},
		},
	}
	tests := []struct {
		provider string
		variable string
		expected string
	}{
		{provider: "java", variable: "SUPERDEV_JVM_ADAPTER_COMMAND", expected: jvmCommand},
		{provider: "node", variable: "SUPERDEV_AGENT_DATA_DIR", expected: agentData},
	}
	for _, test := range tests {
		t.Run(test.provider, func(t *testing.T) {
			directory := filepath.Join(root, test.provider)
			require.NoError(t, os.MkdirAll(directory, 0o755))
			script := "@echo off\r\nif /I not \"%" + test.variable + "%\"==\"" + test.expected + "\" exit /b 91\r\necho admitted\r\n"
			require.NoError(t, os.WriteFile(filepath.Join(directory, "preflight.cmd"), []byte(script), 0o644))

			stage := executor.runFixtureCommand(context.Background(), directory, "preflight.cmd", "debug")
			assert.Equal(t, PhaseStatusPass, stage.Result.PhaseStatus, CanonicalJSON(stage))
		})
	}
}
