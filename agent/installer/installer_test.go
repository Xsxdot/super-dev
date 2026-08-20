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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

type fakeRemote struct {
	outputs         []string
	commandOutputs  map[string][]string
	uploads         []string
	commands        []string
	failCommands    map[string][]error
	systemJobLoaded bool
	userJobLoaded   bool
}

func (f *fakeRemote) Run(ctx context.Context, cmd string) (string, error) {
	f.commands = append(f.commands, cmd)
	explicitLaunchdSuccess := false
	if failures := f.failCommands[cmd]; len(failures) > 0 {
		err := failures[0]
		f.failCommands[cmd] = failures[1:]
		if err != nil {
			f.recordLaunchdProbe(cmd, false)
			return "", err
		}
		f.recordLaunchdProbe(cmd, true)
		explicitLaunchdSuccess = strings.HasPrefix(cmd, "launchctl print ")
	}
	if outputs := f.commandOutputs[cmd]; len(outputs) > 0 {
		out := outputs[0]
		f.commandOutputs[cmd] = outputs[1:]
		f.recordLaunchdProbe(cmd, true)
		return out, nil
	}
	if explicitLaunchdSuccess {
		return "", nil
	}
	if cmd == "launchctl print gui/501/dev.superdev.agent" {
		f.recordLaunchdProbe(cmd, false)
		return "", errors.New("Could not find service dev.superdev.agent")
	}
	if cmd == "launchctl print system/dev.superdev.agent" {
		f.recordLaunchdProbe(cmd, true)
	}
	if output, ok := defaultOwnershipProbeOutput(f, cmd); ok {
		return output, nil
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

func (f *fakeRemote) recordLaunchdProbe(command string, loaded bool) {
	switch command {
	case "launchctl print system/dev.superdev.agent":
		f.systemJobLoaded = loaded
	case "launchctl print gui/501/dev.superdev.agent":
		f.userJobLoaded = loaded
	}
}

const canonicalLinuxSystemdStatus = "LoadState=loaded\nActiveState=active\nFragmentPath=/etc/systemd/system/superdev-agent.service\nExecStart={ path=/usr/local/bin/superdev-agent ; argv[]=/usr/local/bin/superdev-agent --data /var/lib/superdev-agent ; }\n"

func defaultOwnershipProbeOutput(remote *fakeRemote, command string) (string, bool) {
	switch command {
	case linuxSystemdLoadStateCommand:
		return canonicalLinuxSystemdStatus, true
	case macOSPlistProgramProbe("/Library/LaunchDaemons/dev.superdev.agent.plist", true):
		if remote.systemJobLoaded {
			return "/usr/local/bin/superdev-agent\n", true
		}
		return macOSOwnershipAbsentMarker, true
	case macOSPlistProgramProbe("/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist", false):
		if remote.userJobLoaded {
			return "/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent\n", true
		}
		return macOSOwnershipAbsentMarker, true
	case "printf %s \"$HOME\"":
		return "/Users/sycm", true
	case "id -u":
		return "501\n", true
	case macOSUninstallModeProbe(false):
		return macOSLayoutAbsentMarker, true
	case windowsTaskQueryCommand:
		return defaultWindowsTaskXML(), true
	default:
		return "", false
	}
}

func defaultWindowsTaskXML() string {
	return `<?xml version="1.0" encoding="UTF-16"?><Task><Actions><Exec><Command>C:\ProgramData\SuperDev\Agent\superdev-agent.exe</Command></Exec></Actions></Task>`
}

func installerTestHost(id, sshHost, sshUser string, sshPort, remoteAgentPort int) model.Host {
	return model.Host{ID: id, SSHHost: sshHost, SSHPort: sshPort, SSHUser: sshUser}
}

func installerVerifyCommand(port int) string {
	return fmt.Sprintf("curl -fsS http://127.0.0.1:%d/api/security/health >/dev/null", port)
}

func TestInstallerInstallsLinuxAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), installerTestHost("h1", "10.0.0.1", "root", 22, 57019))
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.Equal(t, "linux/amd64", result.Platform)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-linux-amd64"}, remote.uploads)
	assert.Contains(t, remote.commands, "uname -s")
	assert.Contains(t, remote.commands, "uname -m")
	assert.Contains(t, remote.commands, "sudo -n install -m 0755 /tmp/superdev-agent-linux-amd64 /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n systemctl restart superdev-agent.service")
	assert.Contains(t, remote.commands, installerVerifyCommand(57017))
}

func TestInstallerInjectsLookedUpHomeNotConvention(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{
		outputs: []string{"Linux\n", "x86_64\n"},
		commandOutputs: map[string][]string{
			getentPasswdCommand("alice"): {"alice:x:1000:1000:Alice:/opt/app:/bin/bash\n"},
		},
	}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), installerTestHost("h1", "10.0.0.1", "alice", 22, 57019), ServiceOptions{
		Port:           57017,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
		User:           "alice",
	})
	require.NoError(t, err)

	unit := catCommandContaining(t, remote.commands, "/tmp/superdev-agent.service")
	assert.Contains(t, unit, "Environment=HOME=/opt/app")
	assert.NotContains(t, unit, "Environment=HOME=/home/alice")
	assert.Contains(t, remote.commands, getentPasswdCommand("alice"))
}

func TestInstallerFallsBackToConventionHomeWhenLookupFails(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{
		outputs: []string{"Linux\n", "x86_64\n"},
		failCommands: map[string][]error{
			getentPasswdCommand("alice"):  {errors.New("getent: command not found")},
			evalTildeHomeCommand("alice"): {errors.New("no such user")},
		},
	}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), installerTestHost("h1", "10.0.0.1", "alice", 22, 57019), ServiceOptions{
		Port:           57017,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
		User:           "alice",
	})
	require.NoError(t, err)

	unit := catCommandContaining(t, remote.commands, "/tmp/superdev-agent.service")
	assert.Contains(t, unit, "Environment=HOME=/home/alice")
	assert.Contains(t, remote.commands, getentPasswdCommand("alice"))
	assert.Contains(t, remote.commands, evalTildeHomeCommand("alice"))
}

func catCommandContaining(t *testing.T, commands []string, needle string) string {
	t.Helper()
	for _, cmd := range commands {
		if strings.Contains(cmd, "cat >") && strings.Contains(cmd, needle) {
			return cmd
		}
	}
	t.Fatalf("no cat command containing %q in %#v", needle, commands)
	return ""
}

func TestInstallerInstallsWindowsAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-windows-amd64.exe")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{
		outputs: []string{
			"Microsoft Windows [Version 10.0.20348.0]\n",
			"AMD64\n",
			`C:\Users\dev\AppData\Local\Temp` + "\n",
		},
		failCommands: map[string][]error{
			"uname -s": {errors.New("uname is not recognized")},
		},
	}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.InstallWithOptions(context.Background(), installerTestHost("win1", "10.0.0.3", "dev", 22, 57019), ServiceOptions{
		BindAddress:    "0.0.0.0",
		Port:           57019,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
	})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "windows/amd64", result.Platform)
	assert.Equal(t, []string{binary + "->C:/Users/dev/AppData/Local/Temp/superdev-agent-windows-amd64.exe"}, remote.uploads)
	assert.Contains(t, remote.commands, "cmd /c ver")
	assert.Contains(t, remote.commands, "cmd /c echo %PROCESSOR_ARCHITECTURE%")
	assert.Contains(t, remote.commands, "cmd /c echo %TEMP%")
	assert.Contains(t, remote.commands, `cmd /c if not exist "C:\ProgramData\SuperDev\Agent" mkdir "C:\ProgramData\SuperDev\Agent"`)
	assert.Contains(t, remote.commands, `cmd /c copy /Y "C:\Users\dev\AppData\Local\Temp\superdev-agent-windows-amd64.exe" "C:\ProgramData\SuperDev\Agent\superdev-agent.exe"`)
	assert.Contains(t, remote.commands, `cmd /c del /F /Q "C:\ProgramData\SuperDev\Agent\data\security.json" 2>NUL`)
	assert.Contains(t, remote.commands, `cmd /c schtasks /Run /TN SuperDevAgent`)
	assert.Contains(t, remote.commands, installerVerifyCommand(57019))
}

func TestInstallerResetsSecurityStateWhenBootstrappingLinuxAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), model.Host{ID: "h1"}, ServiceOptions{
		Port:           57017,
		RequireAuth:    true,
		BootstrapToken: "fresh-bootstrap",
	})

	require.NoError(t, err)
	assert.Contains(t, remote.commands, "sudo -n rm -f '/var/lib/superdev-agent/security.json'")
	// 必须先停服务再删状态：顺序反了的话仍在运行的旧 agent 会把 provisioned
	// 重新落盘，重装下发的新 bootstrap token 永久失效（真机上复现过）。
	assertCommandOrder(t, remote.commands,
		"sudo -n systemctl stop superdev-agent.service || true",
		"sudo -n rm -f '/var/lib/superdev-agent/security.json'")
}

func TestInstallerStopsServiceBeforeResettingSecurityOnDarwin(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-darwin-arm64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Darwin\n", "arm64\n"}}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), model.Host{ID: "h1"}, ServiceOptions{
		Port:           57017,
		RequireAuth:    true,
		BootstrapToken: "fresh-bootstrap",
	})

	require.NoError(t, err)
	assertCommandOrder(t, remote.commands,
		"sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true",
		"sudo -n rm -f '/Library/Application Support/SuperDev/Agent/security.json'")
}

func TestInstallerStopsTaskBeforeResettingSecurityOnWindows(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-windows-amd64.exe")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{
		"Microsoft Windows [Version 10.0.20348.2402]\n",
		"AMD64\n",
		`C:\Users\dev\AppData\Local\Temp`,
	}}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), installerTestHost("win1", "10.0.0.3", "dev", 22, 57019), ServiceOptions{
		BindAddress:    "0.0.0.0",
		Port:           57019,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
	})

	require.NoError(t, err)
	assertCommandOrder(t, remote.commands,
		`cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`,
		`cmd /c del /F /Q "C:\ProgramData\SuperDev\Agent\data\security.json" 2>NUL`)
}

// 未下发 bootstrap token（如仅更新二进制）时不重置状态，也就不该多停一次服务。
func TestInstallerSkipsStopWhenNotResettingSecurity(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.InstallWithOptions(context.Background(), model.Host{ID: "h1"}, ServiceOptions{
		Port: 57017,
	})

	require.NoError(t, err)
	assert.NotContains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service || true")
	assert.NotContains(t, remote.commands, "sudo -n rm -f '/var/lib/superdev-agent/security.json'")
}

// assertCommandOrder 断言 earlier 在 later 之前执行，两者都必须出现。
func assertCommandOrder(t *testing.T, commands []string, earlier string, later string) {
	t.Helper()
	earlierIdx := indexOfCommand(commands, earlier)
	laterIdx := indexOfCommand(commands, later)
	require.GreaterOrEqual(t, earlierIdx, 0, "expected command not executed: %s", earlier)
	require.GreaterOrEqual(t, laterIdx, 0, "expected command not executed: %s", later)
	assert.Less(t, earlierIdx, laterIdx, "%q must run before %q", earlier, later)
}

func indexOfCommand(commands []string, target string) int {
	for i, cmd := range commands {
		if cmd == target {
			return i
		}
	}
	return -1
}

func TestInstallerUpdatesLinuxAgentBinaryWithoutResettingSecurityOrService(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.UpdateBinary(context.Background(), model.Host{ID: "h1"})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.Equal(t, "linux/amd64", result.Platform)
	assert.Equal(t, "Agent binary updated and service restarted", result.Message)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-linux-amd64"}, remote.uploads)
	assert.Contains(t, remote.commands, "uname -s")
	assert.Contains(t, remote.commands, "uname -m")
	assert.Contains(t, remote.commands, "sudo -n install -m 0755 /tmp/superdev-agent-linux-amd64 /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n systemctl restart superdev-agent.service")
	assert.NotContains(t, remote.commands, "sudo -n rm -f '/var/lib/superdev-agent/security.json'")
	assert.NotContains(t, remote.commands, "cat > /tmp/superdev-agent.service <<'EOF'\n")
	assert.NotContains(t, remote.commands, installerVerifyCommand(57017))
}

func TestInstallerWaitsForAgentReadyWhenVerifyIsTransientlyUnavailable(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-linux-amd64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	verifyCmd := installerVerifyCommand(57017)
	remote := &fakeRemote{
		outputs: []string{"Linux\n", "x86_64\n"},
		failCommands: map[string][]error{
			verifyCmd: {errors.New("connection refused"), nil},
		},
	}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir, VerifyDelay: time.Millisecond}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), installerTestHost("h1", "10.0.0.1", "root", 22, 57019))
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, 2, countCommand(remote.commands, verifyCmd))
}

func TestInstallerDefaultVerifyWindowCoversSlowAgentStartup(t *testing.T) {
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return nil, nil
	})

	window := time.Duration(inst.verifyAttempts()) * inst.verifyDelay()
	assert.GreaterOrEqual(t, window, 60*time.Second)
}

func TestInstallerRestartsLinuxAgent(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Linux\n"}}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Restart(context.Background(), model.Host{ID: "h1"})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.Equal(t, "linux", result.Platform)
	assert.Equal(t, "Agent restarted", result.Message)
	assert.Contains(t, remote.commands, "uname -s")
	assert.Contains(t, remote.commands, "sudo -n systemctl restart superdev-agent.service")
}

func TestInstallerRestartsMacOSUserLaunchAgentWhenSudoNeedsPassword(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "/Users/sycm", "501\n"},
		failCommands: map[string][]error{
			"sudo -n launchctl kickstart -k system/dev.superdev.agent": {
				errors.New("sudo: a password is required"),
			},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Restart(context.Background(), model.Host{ID: "mac1"})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "darwin", result.Platform)
	assert.Equal(t, "Agent restarted in user LaunchAgent mode", result.Message)
	assert.Contains(t, remote.commands, "printf %s \"$HOME\"")
	assert.Contains(t, remote.commands, "id -u")
	assert.Contains(t, remote.commands, "launchctl kickstart -k gui/501/dev.superdev.agent")
}

func TestInstallerInstallsMacOSAgent(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-darwin-arm64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{outputs: []string{"Darwin\n", "arm64\n"}}

	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Install(context.Background(), installerTestHost("mac1", "10.0.0.2", "root", 22, 57020))
	require.NoError(t, err)
	assert.Equal(t, "darwin/arm64", result.Platform)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-darwin-arm64"}, remote.uploads)
	assert.Contains(t, remote.commands, "sudo -n install -m 0755 /tmp/superdev-agent-darwin-arm64 /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n launchctl bootstrap system /Library/LaunchDaemons/dev.superdev.agent.plist")
	assert.Contains(t, remote.commands, installerVerifyCommand(57017))
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

	result, err := inst.Install(context.Background(), installerTestHost("mac1", "10.0.0.2", "sycm", 22, 57020))

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
	assert.Contains(t, remote.commands, installerVerifyCommand(57017))
}

func TestInstallerUpdatesMacOSUserLaunchAgentBinaryWhenSudoNeedsPassword(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "superdev-agent-darwin-arm64")
	require.NoError(t, os.WriteFile(binary, []byte("bin"), 0o755))
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "arm64\n", "/Users/sycm", "501\n", "", "/Users/sycm", "501\n"},
		failCommands: map[string][]error{
			"sudo -n install -m 0755 /tmp/superdev-agent-darwin-arm64 /usr/local/bin/superdev-agent": {
				errors.New("sudo: a password is required"),
			},
		},
	}
	inst := NewWithRemoteFactory(Options{BinaryDir: dir}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.UpdateBinary(context.Background(), model.Host{ID: "mac1"})

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "darwin/arm64", result.Platform)
	assert.Equal(t, "Agent binary updated and user LaunchAgent restarted", result.Message)
	assert.Equal(t, []string{binary + "->/tmp/superdev-agent-darwin-arm64"}, remote.uploads)
	assert.Contains(t, remote.commands, "install -m 0755 /tmp/superdev-agent-darwin-arm64 '/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent'")
	assert.Contains(t, remote.commands, "launchctl kickstart -k gui/501/dev.superdev.agent")
	assert.NotContains(t, remote.commands, "cat > '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' <<'EOF'\n")
}

func TestInstallerUninstallsLinuxAgentKeepingData(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "h1"}, false)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Equal(t, "h1", result.HostID)
	assert.False(t, result.RemovedData)
	assert.Contains(t, remote.commands, linuxSystemdLoadStateCommand)
	assert.Contains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service")
	assert.Contains(t, remote.commands, "if [ -e /etc/systemd/system/superdev-agent.service ]; then sudo -n systemctl disable superdev-agent.service; fi")
	assert.Contains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n systemctl daemon-reload")
	assert.NotContains(t, remote.commands, "sudo -n rm -rf /var/lib/superdev-agent")
}

func TestInstallerUninstallsLinuxAgentAndDeletesData(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "h1"}, true)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.RemovedData)
	assert.Contains(t, remote.commands, "sudo -n rm -rf /var/lib/superdev-agent")
}

func TestInstallerLinuxStopFailureIsNotIgnored(t *testing.T) {
	stopCommand := "sudo -n systemctl stop superdev-agent.service"
	remote := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus}},
		failCommands:   map[string][]error{stopCommand: {errors.New("sudo: a password is required")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "linux1"}, false)

	require.Error(t, err)
	var installErr *InstallError
	require.ErrorAs(t, err, &installErr)
	assert.Equal(t, "uninstall_systemd", installErr.Stage)
	assert.NotContains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
}

func TestInstallerLinuxLoadedUnitWithoutUnitFileIsStillStopped(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "linux-loaded"}, false)

	require.NoError(t, err)
	assert.Contains(t, remote.commands, linuxSystemdLoadStateCommand)
	assert.Contains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service")
	assert.NotContains(t, remote.commands, "if [ -e /etc/systemd/system/superdev-agent.service ]; then sudo -n systemctl stop superdev-agent.service; fi")
}

func TestInstallerLinuxMissingUnitDoesNotAttemptStop(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {"LoadState=not-found\nActiveState=inactive\nFragmentPath=\nExecStart=\n"}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "linux-missing"}, false)

	require.NoError(t, err)
	assert.NotContains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service")
	assert.NotContains(t, remote.commands, "sudo -n systemctl disable superdev-agent.service")
	assert.Contains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
}

func TestInstallerRejectsCustomSameNameLinuxSystemdUnitBeforeMutation(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {
			"LoadState=loaded\nActiveState=active\nFragmentPath=/etc/systemd/system/superdev-agent.service\nExecStart={ path=/opt/custom-worker ; argv[]=/opt/custom-worker ; }\n",
		}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "linux-custom"}, true)

	require.Error(t, err)
	assert.NotContains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service")
	assert.NotContains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
	assert.NotContains(t, remote.commands, "sudo -n rm -rf /var/lib/superdev-agent")
}

func TestInstallerRejectsLinuxUnitWithAdditionalExecStartBeforeMutation(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {
			"LoadState=loaded\nActiveState=active\nFragmentPath=/etc/systemd/system/superdev-agent.service\n" +
				"ExecStart={ path=/usr/local/bin/superdev-agent ; argv[]=/usr/local/bin/superdev-agent ; } { path=/opt/custom-worker ; argv[]=/opt/custom-worker ; }\n",
		}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "linux-multiple-exec"}, false)

	require.Error(t, err)
	assert.NotContains(t, remote.commands, "sudo -n systemctl stop superdev-agent.service")
	assert.NotContains(t, remote.commands, "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent")
}

func TestInstallerUninstallsMacOSAgentAndDeletesData(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(true): {"system"}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, true)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.True(t, result.RemovedData)
	assert.Contains(t, remote.commands, "launchctl print system/dev.superdev.agent")
	assert.Contains(t, remote.commands, "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist")
	assert.Contains(t, remote.commands, "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent")
	assert.Contains(t, remote.commands, "sudo -n rm -rf '/Library/Application Support/SuperDev/Agent'")
	assert.Contains(t, remote.commands, "sudo -n rm -f /var/log/superdev-agent.log /var/log/superdev-agent.err.log")
}

func TestInstallerUninstallsMacOSAgentKeepingDataAndLogs(t *testing.T) {
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.NoError(t, err)
	assert.False(t, result.RemovedData)
	assert.NotContains(t, remote.commands, "sudo -n rm -rf '/Library/Application Support/SuperDev/Agent'")
	assert.NotContains(t, remote.commands, "sudo -n rm -f /var/log/superdev-agent.log /var/log/superdev-agent.err.log")
}

func TestInstallerBootsOutLoadedMacOSJobEvenWhenAgentProcessIsNotRunning(t *testing.T) {
	printJob := "launchctl print system/dev.superdev.agent"
	bootout := "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist"
	remote := &fakeRemote{
		outputs: []string{"Darwin\n"},
		commandOutputs: map[string][]string{
			macOSUninstallModeProbe(false): {"system"},
			printJob:                       {"state = waiting\npid = 0\n"},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.NoError(t, err)
	assert.Contains(t, remote.commands, printJob)
	assert.Contains(t, remote.commands, bootout)
	assert.Contains(t, remote.commands, macOSUninstallModeProbe(false))
	assert.NotContains(t, strings.Join(remote.commands, "\n"), "pgrep")
}

func TestInstallerUninstallsMacOSSystemDaemonWhenGUIUserDomainIsAbsent(t *testing.T) {
	userJob := "launchctl print gui/501/dev.superdev.agent"
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}},
		failCommands: map[string][]error{
			userJob: {errors.New("Could not find domain for user gui: 501 (exit status 112)")},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac-system-no-gui"}, false)

	require.NoError(t, err)
	assert.Contains(t, remote.commands, userJob)
	assert.Contains(t, remote.commands, "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist")
	assert.NotContains(t, remote.commands, "launchctl bootout gui/501")
}

func TestInstallerUninstallsMacOSUserLaunchAgentWhenSystemLayoutIsAbsent(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "/Users/sycm", "501\n"},
		commandOutputs: map[string][]string{
			macOSUninstallModeProbe(true):                {"user_launch_agent"},
			"launchctl print gui/501/dev.superdev.agent": {"", ""},
		},
		failCommands: map[string][]error{
			"launchctl print system/dev.superdev.agent": {errors.New("Could not find service dev.superdev.agent")},
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
	assert.Contains(t, remote.commands, "launchctl print gui/501/dev.superdev.agent")
	assert.Contains(t, remote.commands, "launchctl bootout gui/501 '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist'")
	assert.Contains(t, remote.commands, "rm -f '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' '/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent'")
	assert.Contains(t, remote.commands, "rm -rf '/Users/sycm/Library/Application Support/SuperDev/Agent'")
	assert.Contains(t, remote.commands, "rm -f '/Users/sycm/Library/Logs/superdev-agent.log' '/Users/sycm/Library/Logs/superdev-agent.err.log'")
}

func TestInstallerKeepsMacOSUserLaunchAgentDataAndLogsByDefault(t *testing.T) {
	remote := &fakeRemote{
		outputs: []string{"Darwin\n", "/Users/sycm", "501\n"},
		commandOutputs: map[string][]string{
			macOSUninstallModeProbe(false):               {"user_launch_agent"},
			"launchctl print gui/501/dev.superdev.agent": {"", ""},
		},
		failCommands: map[string][]error{
			"launchctl print system/dev.superdev.agent": {errors.New("Could not find service dev.superdev.agent")},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.NoError(t, err)
	assert.False(t, result.RemovedData)
	assert.NotContains(t, remote.commands, "rm -rf '/Users/sycm/Library/Application Support/SuperDev/Agent'")
	assert.NotContains(t, remote.commands, "rm -f '/Users/sycm/Library/Logs/superdev-agent.log' '/Users/sycm/Library/Logs/superdev-agent.err.log'")
}

func TestInstallerMacOSSystemPermissionFailureDoesNotFallbackToUserLayout(t *testing.T) {
	removeSystemResources := "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent"
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}},
		failCommands: map[string][]error{
			removeSystemResources: {errors.New("sudo: a password is required")},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.Error(t, err)
	var installErr *InstallError
	require.ErrorAs(t, err, &installErr)
	assert.Equal(t, "uninstall_launchd", installErr.Stage)
	assert.NotContains(t, remote.commands, "rm -f '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist'")
}

func TestInstallerMacOSStopFailuresAreNotIgnored(t *testing.T) {
	systemStop := "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist"
	userBinary := "/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent"
	userStop := "launchctl bootout gui/501 '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist'"
	tests := []struct {
		name           string
		mode           string
		outputs        []string
		stopCommand    string
		deletedCommand string
	}{
		{
			name:           "system LaunchDaemon",
			mode:           "system",
			outputs:        []string{"Darwin\n"},
			stopCommand:    systemStop,
			deletedCommand: "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent",
		},
		{
			name:           "user LaunchAgent",
			mode:           "user_launch_agent",
			outputs:        []string{"Darwin\n", "/Users/sycm", "501\n"},
			stopCommand:    userStop,
			deletedCommand: "rm -f '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist' '" + userBinary + "'",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			failCommands := map[string][]error{tc.stopCommand: {errors.New("fixture stop failure")}}
			if tc.mode == "user_launch_agent" {
				failCommands["launchctl print system/dev.superdev.agent"] = []error{errors.New("Could not find service dev.superdev.agent")}
			}
			remote := &fakeRemote{
				outputs:        tc.outputs,
				commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {tc.mode}},
				failCommands:   failCommands,
			}
			if tc.mode == "user_launch_agent" {
				remote.commandOutputs["launchctl print gui/501/dev.superdev.agent"] = []string{"", ""}
			}
			inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

			_, err := inst.Uninstall(context.Background(), model.Host{ID: tc.name}, false)

			require.Error(t, err)
			assert.NotContains(t, remote.commands, tc.deletedCommand)
		})
	}
}

func TestInstallerMacOSMissingLaunchdJobIsTheOnlyIgnoredStopCondition(t *testing.T) {
	printJob := "launchctl print system/dev.superdev.agent"
	bootout := "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist"
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}},
		failCommands: map[string][]error{
			printJob: {
				errors.New(`Bad request. Could not find service "dev.superdev.agent" in domain for system`),
				errors.New(`Bad request. Could not find service "dev.superdev.agent" in domain for system`),
			},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.NoError(t, err)
	assert.Contains(t, remote.commands, printJob)
	assert.NotContains(t, remote.commands, bootout)
	assert.Contains(t, remote.commands, "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent")
}

func TestInstallerMacOSLaunchdProbeFailureIsNotIgnored(t *testing.T) {
	printJob := "launchctl print system/dev.superdev.agent"
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}},
		failCommands:   map[string][]error{printJob: {errors.New("launchctl manager unavailable")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac1"}, false)

	require.Error(t, err)
	assert.NotContains(t, remote.commands, "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent")
}

func TestInstallerRejectsCustomSameNameMacOSLaunchdResourcesBeforeMutation(t *testing.T) {
	userPaths := macOSUserAgentPaths("/Users/sycm")
	tests := []struct {
		name            string
		outputs         []string
		mode            string
		plistProbe      string
		failCommands    map[string][]error
		forbiddenStop   string
		forbiddenDelete string
	}{
		{
			name:            "system LaunchDaemon",
			outputs:         []string{"Darwin\n"},
			mode:            "system",
			plistProbe:      macOSPlistProgramProbe("/Library/LaunchDaemons/dev.superdev.agent.plist", true),
			forbiddenStop:   "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist",
			forbiddenDelete: "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent",
		},
		{
			name:            "user LaunchAgent",
			outputs:         []string{"Darwin\n", "/Users/sycm", "501\n"},
			mode:            "user_launch_agent",
			plistProbe:      macOSPlistProgramProbe(userPaths.plist, false),
			failCommands:    map[string][]error{"launchctl print system/dev.superdev.agent": {errors.New("Could not find service dev.superdev.agent")}},
			forbiddenStop:   "launchctl bootout gui/501 " + shellQuote(userPaths.plist),
			forbiddenDelete: "rm -f " + shellQuote(userPaths.plist) + " " + shellQuote(userPaths.binary),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			remote := &fakeRemote{
				outputs: tc.outputs,
				commandOutputs: map[string][]string{
					macOSUninstallModeProbe(true): {tc.mode},
					tc.plistProbe:                 {"/opt/custom-worker\n"},
				},
				failCommands: tc.failCommands,
			}
			inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

			_, err := inst.Uninstall(context.Background(), model.Host{ID: tc.name}, true)

			require.Error(t, err)
			assert.NotContains(t, remote.commands, tc.forbiddenStop)
			assert.NotContains(t, remote.commands, tc.forbiddenDelete)
		})
	}
}

func TestInstallerRejectsAmbiguousMacOSLayoutsBeforeMutation(t *testing.T) {
	userJob := "launchctl print gui/501/dev.superdev.agent"
	systemNotFound := errors.New("Could not find service dev.superdev.agent")
	tests := []struct {
		name           string
		pathEvidence   string
		commandOutputs map[string][]string
		failCommands   map[string][]error
	}{
		{
			name:         "simultaneously canonical system and user jobs",
			pathEvidence: macOSLayoutAbsentMarker,
			commandOutputs: map[string][]string{
				userJob: {""},
			},
		},
		{
			name:         "loaded system job and user files",
			pathEvidence: string(installModeUserLaunchAgent),
		},
		{
			name:         "system residual paths and real user layout",
			pathEvidence: macOSLayoutAmbiguousMarker,
			commandOutputs: map[string][]string{
				userJob: {""},
			},
			failCommands: map[string][]error{
				"launchctl print system/dev.superdev.agent": {systemNotFound},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.commandOutputs == nil {
				tc.commandOutputs = make(map[string][]string)
			}
			tc.commandOutputs[macOSUninstallModeProbe(false)] = []string{tc.pathEvidence}
			remote := &fakeRemote{
				outputs:        []string{"Darwin\n"},
				commandOutputs: tc.commandOutputs,
				failCommands:   tc.failCommands,
			}
			inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

			_, err := inst.Uninstall(context.Background(), model.Host{ID: tc.name}, false)

			require.Error(t, err)
			var installErr *InstallError
			require.ErrorAs(t, err, &installErr)
			assert.Equal(t, "uninstall_launchd", installErr.Stage)
			for _, command := range remote.commands {
				assert.NotContains(t, command, "launchctl bootout")
				assert.NotContains(t, command, "rm -f")
				assert.NotContains(t, command, "rm -rf")
			}
		})
	}
}

func TestInstallerMacOSAbsentJobAndPlistRemainNoOp(t *testing.T) {
	printJob := "launchctl print system/dev.superdev.agent"
	plistProbe := macOSPlistProgramProbe("/Library/LaunchDaemons/dev.superdev.agent.plist", true)
	remote := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(false): {"system"}, plistProbe: {macOSOwnershipAbsentMarker}},
		failCommands: map[string][]error{printJob: {
			errors.New("Could not find service dev.superdev.agent"),
			errors.New("Could not find service dev.superdev.agent"),
		}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "mac-absent"}, false)

	require.NoError(t, err)
	assert.NotContains(t, remote.commands, "sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist")
}

func TestInstallerMacOSSystemKeepDataRetryStaysInSystemLayout(t *testing.T) {
	printJob := "launchctl print system/dev.superdev.agent"
	plistProbe := macOSPlistProgramProbe("/Library/LaunchDaemons/dev.superdev.agent.plist", true)
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"uname -s":                     {"Darwin\n", "Darwin\n"},
			macOSUninstallModeProbe(false): {"system", "system"},
			plistProbe: {"/usr/local/bin/superdev-agent\n", "/usr/local/bin/superdev-agent\n",
				macOSOwnershipAbsentMarker},
		},
		failCommands: map[string][]error{printJob: {
			nil,
			nil,
			errors.New("Could not find service dev.superdev.agent"),
			errors.New("Could not find service dev.superdev.agent"),
		}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, firstErr := inst.Uninstall(context.Background(), model.Host{ID: "mac-system-retry"}, false)
	_, secondErr := inst.Uninstall(context.Background(), model.Host{ID: "mac-system-retry"}, false)

	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.Contains(t, macOSUninstallModeProbe(false), "'/Library/Application Support/SuperDev/Agent'")
	assert.Contains(t, macOSUninstallModeProbe(false), "$HOME/Library/Application Support/SuperDev/Agent")
	assert.NotContains(t, remote.commands, "rm -f '/Users/sycm/Library/LaunchAgents/dev.superdev.agent.plist'")
}

func TestInstallerUninstallsWindowsAgentKeepingDataWithMissingResourceGuards(t *testing.T) {
	remote := &fakeRemote{
		outputs:      []string{"Microsoft Windows [Version 10.0.20348.0]\n", "AMD64\n"},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	result, err := inst.Uninstall(context.Background(), model.Host{ID: "win1"}, false)

	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.False(t, result.RemovedData)
	assert.Contains(t, remote.commands, `cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`)
	assert.Contains(t, remote.commands, `cmd /c schtasks /Delete /TN SuperDevAgent /F`)
	assert.Contains(t, remote.commands, `cmd /c if exist "C:\ProgramData\SuperDev\Agent\superdev-agent.exe" del /F /Q "C:\ProgramData\SuperDev\Agent\superdev-agent.exe"`)
	assert.NotContains(t, remote.commands, `cmd /c if exist "C:\ProgramData\SuperDev\Agent" rmdir /S /Q "C:\ProgramData\SuperDev\Agent"`)
}

func TestInstallerRejectsCustomSameNameWindowsScheduledTaskBeforeMutation(t *testing.T) {
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"cmd /c ver":                           {"Microsoft Windows [Version 10.0.20348.0]\n"},
			"cmd /c echo %PROCESSOR_ARCHITECTURE%": {"AMD64\n"},
			windowsTaskQueryCommand:                {`<?xml version="1.0"?><Task><Actions><Exec><Command>C:\Tools\custom-worker.exe</Command></Exec></Actions></Task>`},
		},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "win-custom"}, true)

	require.Error(t, err)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /Delete /TN SuperDevAgent /F`)
	assert.NotContains(t, remote.commands, `cmd /c if exist "C:\ProgramData\SuperDev\Agent" rmdir /S /Q "C:\ProgramData\SuperDev\Agent"`)
}

func TestInstallerRejectsWindowsTaskWithAdditionalActionBeforeMutation(t *testing.T) {
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"cmd /c ver":                           {"Microsoft Windows [Version 10.0.20348.0]\n"},
			"cmd /c echo %PROCESSOR_ARCHITECTURE%": {"AMD64\n"},
			windowsTaskQueryCommand: {`<?xml version="1.0"?><Task><Actions>` +
				`<Exec><Command>C:\ProgramData\SuperDev\Agent\superdev-agent.exe</Command></Exec>` +
				`<ComHandler><ClassId>{11111111-1111-1111-1111-111111111111}</ClassId></ComHandler>` +
				`</Actions></Task>`},
		},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "win-mixed-actions"}, false)

	require.Error(t, err)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /Delete /TN SuperDevAgent /F`)
}

func TestInstallerMissingWindowsScheduledTaskIsNoOp(t *testing.T) {
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"cmd /c ver":                           {"Microsoft Windows [Version 10.0.20348.0]\n"},
			"cmd /c echo %PROCESSOR_ARCHITECTURE%": {"AMD64\n"},
			windowsTaskQueryCommand:                {windowsTaskAbsentMarker},
		},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "win-absent"}, false)

	require.NoError(t, err)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`)
	assert.NotContains(t, remote.commands, `cmd /c schtasks /Delete /TN SuperDevAgent /F`)
}

func TestInstallerWindowsUninstallFailsWhileAgentProcessIsStillRunning(t *testing.T) {
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"cmd /c ver":                           {"Microsoft Windows [Version 10.0.20348.0]\n"},
			"cmd /c echo %PROCESSOR_ARCHITECTURE%": {"AMD64\n"},
			`cmd /c tasklist /FI "IMAGENAME eq superdev-agent.exe" /NH`: {"superdev-agent.exe  4242 Services 0 10,000 K\n"},
		},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

	_, err := inst.Uninstall(context.Background(), model.Host{ID: "win1"}, false)

	require.Error(t, err)
	var installErr *InstallError
	require.ErrorAs(t, err, &installErr)
	assert.Equal(t, "uninstall_windows_task", installErr.Stage)
	assert.NotContains(t, remote.commands, `cmd /c if exist "C:\ProgramData\SuperDev\Agent\superdev-agent.exe" del /F /Q "C:\ProgramData\SuperDev\Agent\superdev-agent.exe"`)
}

func TestInstallerUninstallsWindowsAgentAndPurgesDataIdempotently(t *testing.T) {
	remote := &fakeRemote{
		commandOutputs: map[string][]string{
			"cmd /c ver":                           {"Microsoft Windows [Version 10.0.20348.0]\n", "Microsoft Windows [Version 10.0.20348.0]\n"},
			"cmd /c echo %PROCESSOR_ARCHITECTURE%": {"AMD64\n", "AMD64\n"},
			windowsTaskQueryCommand:                {defaultWindowsTaskXML(), windowsTaskAbsentMarker},
		},
		failCommands: map[string][]error{
			"uname -s": {errors.New("uname is not recognized"), errors.New("uname is not recognized")},
		},
	}
	inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	first, firstErr := inst.Uninstall(context.Background(), model.Host{ID: "win1"}, true)
	second, secondErr := inst.Uninstall(context.Background(), model.Host{ID: "win1"}, true)

	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.True(t, first.RemovedData)
	assert.True(t, second.RemovedData)
	purgeCommand := `cmd /c if exist "C:\ProgramData\SuperDev\Agent" rmdir /S /Q "C:\ProgramData\SuperDev\Agent"`
	assert.Equal(t, 2, countCommand(remote.commands, purgeCommand))
}

func TestInstallerUninstallIsRepeatableAcrossSupportedUnixLayouts(t *testing.T) {
	tests := []struct {
		name           string
		commandOutputs map[string][]string
		failCommands   map[string][]error
	}{
		{
			name: "linux systemd",
			commandOutputs: map[string][]string{
				"uname -s":                   {"Linux\n", "Linux\n"},
				linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus, "LoadState=not-found\nActiveState=inactive\nFragmentPath=\nExecStart=\n"},
			},
		},
		{
			name: "macOS system LaunchDaemon then missing resources",
			commandOutputs: map[string][]string{
				"uname -s":                    {"Darwin\n", "Darwin\n"},
				macOSUninstallModeProbe(true): {"system", macOSLayoutAbsentMarker},
				"printf %s \"$HOME\"":         {"/Users/sycm"},
				"id -u":                       {"501\n"},
			},
			failCommands: map[string][]error{
				"launchctl print system/dev.superdev.agent":  {nil, nil, errors.New("Could not find service dev.superdev.agent")},
				"launchctl print gui/501/dev.superdev.agent": {errors.New("Could not find service dev.superdev.agent")},
			},
		},
		{
			name: "macOS user LaunchAgent",
			commandOutputs: map[string][]string{
				"uname -s":                    {"Darwin\n", "Darwin\n"},
				macOSUninstallModeProbe(true): {"user_launch_agent", "user_launch_agent"},
				"printf %s \"$HOME\"":         {"/Users/sycm", "/Users/sycm"},
				"id -u":                       {"501\n", "501\n"},
			},
			failCommands: map[string][]error{
				"launchctl print system/dev.superdev.agent":  {errors.New("Could not find service dev.superdev.agent"), errors.New("Could not find service dev.superdev.agent")},
				"launchctl print gui/501/dev.superdev.agent": {nil, errors.New("Could not find service dev.superdev.agent")},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			remote := &fakeRemote{commandOutputs: tc.commandOutputs, failCommands: tc.failCommands}
			inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })

			_, firstErr := inst.Uninstall(context.Background(), model.Host{ID: tc.name}, true)
			_, secondErr := inst.Uninstall(context.Background(), model.Host{ID: tc.name}, true)

			require.NoError(t, firstErr)
			require.NoError(t, secondErr)
		})
	}
}

func TestInstallerUninstallCommandsOnlyAddressAgentOwnedResources(t *testing.T) {
	linux := &fakeRemote{
		outputs:        []string{"Linux\n"},
		commandOutputs: map[string][]string{linuxSystemdLoadStateCommand: {canonicalLinuxSystemdStatus}},
	}
	macOS := &fakeRemote{
		outputs:        []string{"Darwin\n"},
		commandOutputs: map[string][]string{macOSUninstallModeProbe(true): {"system"}},
	}
	windows := &fakeRemote{
		outputs:      []string{"Microsoft Windows [Version 10.0.20348.0]\n", "AMD64\n"},
		failCommands: map[string][]error{"uname -s": {errors.New("uname is not recognized")}},
	}

	for id, remote := range map[string]*fakeRemote{"linux": linux, "darwin": macOS, "windows": windows} {
		inst := NewWithRemoteFactory(Options{}, func(host model.Host) (Remote, error) { return remote, nil })
		_, err := inst.Uninstall(context.Background(), model.Host{ID: id}, true)
		require.NoError(t, err)
		for _, command := range remote.commands {
			lower := strings.ToLower(command)
			assert.NotContains(t, lower, "docker", "卸载器不得管理独立 Docker Runtime")
			if strings.Contains(lower, "systemctl stop") || strings.Contains(lower, "systemctl disable") {
				assert.Contains(t, command, "superdev-agent.service", "只允许停止 Agent 自有 unit")
			}
		}
	}
}

func TestInstallerWrapsStageOnMissingBinary(t *testing.T) {
	remote := &fakeRemote{outputs: []string{"Linux\n", "x86_64\n"}}
	inst := NewWithRemoteFactory(Options{BinaryDir: t.TempDir()}, func(host model.Host) (Remote, error) {
		return remote, nil
	})

	_, err := inst.Install(context.Background(), model.Host{ID: "h1"})
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
