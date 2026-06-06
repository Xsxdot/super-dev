// installer 安装编排测试。
//
// 职责：
//   - 验证安装器按远端平台选择二进制并上传
//   - 验证 Linux systemd 与 macOS LaunchDaemon 的关键命令顺序
//   - 验证阶段化错误便于 API 和 UI 展示
//
// 边界：
//   - 不建立真实 SSH 连接
//   - 不写入真实系统服务目录
package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

type fakeRemote struct {
	outputs      []string
	uploads      []string
	commands     []string
	failCommands map[string][]error
}

func (f *fakeRemote) Run(ctx context.Context, cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	if failures := f.failCommands[cmd]; len(failures) > 0 {
		err := failures[0]
		f.failCommands[cmd] = failures[1:]
		if err != nil {
			return "", err
		}
	}
	if len(f.outputs) == 0 {
		return "", nil
	}
	out := f.outputs[0]
	f.outputs = f.outputs[1:]
	return out, nil
}

func (f *fakeRemote) Upload(ctx context.Context, localPath string, remotePath string) error {
	f.uploads = append(f.uploads, localPath+"->"+remotePath)
	return nil
}

func (f *fakeRemote) Close() error { return nil }

func TestInstallerInstallsLinuxAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), model.Host{
		ID: "h1", SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57019,
	})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.Equal(t, "linux/amd64", result.Platform)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-linux-amd64"}, remote.uploads)
	assert.Contains(t, remote.commands, "uname -s")
	assert.Contains(t, remote.commands, "uname -m")
	assert.Contains(t, remote.commands, "sudo -n install -m 0755 /tmp/superdev-agent-linux-amd64 /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n systemctl restart superdev-agent.service")
	assert.Contains(t, remote.commands, "curl -fsS http://127.0.0.1:57019/api/hosts >/dev/null")
}

func TestInstallerWaitsForAgentReadyWhenVerifyIsTransientlyUnavailable(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	verifyCmd := "curl -fsS http://127.0.0.1:57019/api/hosts >/dev/null"
	remote := &fakeRemote{
		outputs: []string{"Linux\n", "x86_64\n"},
		failCommands: map[string][]error{
			verifyCmd: {errors.New("connection refused"), nil},
		},
	}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir, VerifyDelay: time.Millisecond}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), model.Host{
		ID: "h1", SSHHost: "10.0.0.1", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57019,
	})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, 2, countCommand(remote.commands, verifyCmd))
}

func TestInstallerInstallsMacOSAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-darwin-arm64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Darwin\n", "arm64\n"}}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), model.Host{
		ID: "mac1", SSHHost: "10.0.0.2", SSHPort: 22, SSHUser: "root", RemoteAgentPort: 57020,
	})
	require.NoError(t, err)
	assert.Equal(t, "darwin/arm64", result.Platform)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-darwin-arm64"}, remote.uploads)
	assert.Contains(t, remote.commands, "sudo -n install -m 0755 /tmp/superdev-agent-darwin-arm64 /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n launchctl bootstrap system /Library/LaunchDaemons/dev.superdev.agent.plist")
	assert.Contains(t, remote.commands, "curl -fsS http://127.0.0.1:57020/api/hosts >/dev/null")
}

func TestInstallerDowngradesMacOSAgentToUserLaunchAgentWhenSudoNeedsPassword(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-darwin-arm64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "arm64\n", "/Users/sycm", "501\n"},
		failCommands: map[string][]error{
			"sudo -n install -m 0755 /tmp/superdev-agent-darwin-arm64 /usr/local/bin/superdev-agent": {
				errors.New("sudo: a password is required"),
			},
		},
	}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), model.Host{
		ID: "mac1", SSHHost: "10.0.0.2", SSHPort: 22, SSHUser: "sycm", RemoteAgentPort: 57020,
	})

	require.NoError(t, err)
	assert.Equal(t, "darwin/arm64", result.Platform)
	assert.Equal(t, "Agent installed and started in user LaunchAgent mode", result.Message)
	assert.Contains(t, remote.commands, "printf %s \"$HOME\"")
	assert.Contains(t, remote.commands, "id -u")
	assert.Contains(t, remote.commands, "mkdir -p '/Users/sycm/Library/Application Support/SuperDev/Agent/bin' '/Users/sycm/Library/Application Support/SuperDev/Agent/data' '/Users/sycm/Library/LaunchAgents' '/Users/sycm/Library/Logs'")
	assert.Contains(t, remote.commands, "install -m 0755 /tmp/superdev-agent-darwin-arm64 '/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent'")
	assert.Contains(t, remote.commands, "launchctl bootout gui/501 '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' || true")
	assert.Contains(t, remote.commands, "launchctl bootstrap gui/501 '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist'")
	assert.Contains(t, remote.commands, "launchctl kickstart -k gui/501/dev.superdev.agent")
	assert.NotContains(t, remote.commands, "sudo -n launchctl bootstrap system /Library/LaunchDaemons/dev.superdev.agent.plist")
	assert.Contains(t, remote.commands, "curl -fsS http://127.0.0.1:57020/api/hosts >/dev/null")
}

func TestInstallerUninstallsLinuxAgentKeepingData(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Linux\n"}}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "h1"}, false)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.False(t, result.RemovedData)
	assert.Contains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service || true")
	assert.Contains(t, remote.commands, "sudo -n systemctl disable superdev-agent.service || true")
	assert.Contains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n systemctl daemon-reload")
	assert.NotContains(t, remote.commands, "sudo -n rm -rf /var/lib/superdev-agent")
}

func TestInstallerUninstallsLinuxAgentAndDeletesData(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Linux\n"}}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "h1"}, true)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.RemovedData)
	assert.Contains(t, remote.commands, "sudo -n rm -rf /var/lib/superdev-agent")
}

func TestInstallerUninstallsMacOSAgentAndDeletesData(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Darwin\n"}}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, true)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.RemovedData)
	assert.Contains(t, remote.commands, "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true")
	assert.Contains(t, remote.commands, "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n rm -rf '/Library/Application Support/SuperDev/Agent'")
}

func TestInstallerDowngradesMacOSUninstallToUserLaunchAgentWhenSudoNeedsPassword(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "", "/Users/sycm", "501\n"},
		failCommands: map[string][]error{
			"sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent": {
				errors.New("sudo: a password is required"),
			},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, true)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.RemovedData)
	assert.Contains(t, remote.commands, "printf %s \"$HOME\"")
	assert.Contains(t, remote.commands, "id -u")
	assert.Contains(t, remote.commands, "launchctl bootout gui/501 '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' || true")
	assert.Contains(t, remote.commands, "rm -f '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' '/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent'")
	assert.Contains(t, remote.commands, "rm -rf '/Users/sycm/Library/Application Support/SuperDev/Agent'")
}

func TestInstallerWrapsStageOnMissingBinary(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}
	inst := NewWithRemoteFactory(Options{BinaryDir: t.TempDir()}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.Install(context.Background(), model.Host{ID: "h1", RemoteAgentPort: 57017})
	require.Error(t, err)
	var installErr *InstallError
	require.ErrorAs(t, err, &installErr)
	assert.Equal(t, "resolve_binary", installErr.Stage)
}

func countCommand(commands []string, target string) int {
	count := 0
	for _, cmd := range commands {
		if cmd == target {
			count++
		}
	}
	return count
}
