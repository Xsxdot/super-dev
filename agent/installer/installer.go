// Package installer installs the SuperDev remote agent on SSH hosts.
//
// 职责：
//   - 检测远端 macOS/Linux 平台与 CPU 架构
//   - 解析随桌面包携带的 agent 二进制
//   - 编排上传、系统级安装、自启配置和启动验证
//
// 边界：
//   - 不持久化 Host 或安装历史
//   - 不管理 SSH 隧道生命周期
//   - 不提供 Windows 安装能力
package installer

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// Options 配置远端 agent 安装器。
type Options struct {
	BinaryDir      string
	VerifyAttempts int
	VerifyDelay    time.Duration
}

// Result 描述一次安装结果。
type Result struct {
	OK       bool   `json:"ok"`
	HostID   string `json:"host_id"`
	Platform string `json:"platform"`
	Message  string `json:"message"`
}

// UninstallResult 描述一次远端 agent 卸载结果。
type UninstallResult struct {
	OK          bool   `json:"ok"`
	HostID      string `json:"host_id"`
	RemovedData bool   `json:"removed_data"`
	Message     string `json:"message"`
}

type installMode string

const (
	installModeSystem          installMode = "system"
	installModeUserLaunchAgent installMode = "user_launch_agent"
)

// InstallError 带有失败阶段，方便 API 和 UI 展示具体原因。
type InstallError struct {
	Stage string
	Err   error
}

// Error 返回包含阶段的错误信息。
//
// 返回：
//   - 空错误返回空字符串
//   - 有阶段时返回 "stage: error"，否则返回底层错误
func (e *InstallError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	if e.Stage == "" {
		return e.Err.Error()
	}
	return e.Stage + ": " + e.Err.Error()
}

// Unwrap 返回底层错误。
//
// 返回：
//   - 底层错误；接收者为空时返回 nil
func (e *InstallError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Remote 执行远端命令并上传文件。
type Remote interface {
	Run(ctx context.Context, cmd string) (string, error)
	Upload(ctx context.Context, localPath string, remotePath string) error
	Close() error
}

// RemoteFactory creates a remote command/upload client for one host.
type RemoteFactory func(host model.Host) (Remote, error)

// Installer installs a remote SuperDev agent.
type Installer struct {
	opts    Options
	factory RemoteFactory
}

// New creates a production installer.
//
// 参数：
//   - opts: 安装器配置，包含随包二进制目录
//
// 返回：
//   - 使用真实 SSH remote 的安装器
func New(opts Options) *Installer {
	return NewWithRemoteFactory(opts, NewSSHRemote)
}

// NewWithRemoteFactory creates an installer with an injected remote factory.
//
// 参数：
//   - opts: 安装器配置
//   - factory: 创建远端命令与上传客户端的工厂
//
// 返回：
//   - 使用指定工厂的安装器
//
// 注意：
//   - 该入口主要用于测试，生产代码应使用 New。
func NewWithRemoteFactory(opts Options, factory RemoteFactory) *Installer {
	return &Installer{opts: opts, factory: factory}
}

// Install installs or reinstalls the remote agent on host.
//
// 参数：
//   - ctx: 上下文，用于取消 SSH 命令和上传前置检查
//   - host: 远端主机配置，包含 SSH 凭据和 agent 端口
//
// 返回：
//   - 安装成功结果，包含 host ID 和平台
//   - 分阶段错误，便于上层定位失败原因
func (i *Installer) Install(ctx context.Context, host model.Host) (Result, error) {
	port := host.RemoteAgentPort
	if port == 0 {
		// 远端 Host 可能来自旧数据，缺省时沿用正式 agent 默认端口。
		port = 57017
	}
	remote, err := i.factory(host)
	if err != nil {
		return Result{}, stageErr("connect", err)
	}
	defer remote.Close() //nolint:errcheck

	osName, err := remote.Run(ctx, "uname -s")
	if err != nil {
		return Result{}, stageErr("detect_os", err)
	}
	machine, err := remote.Run(ctx, "uname -m")
	if err != nil {
		return Result{}, stageErr("detect_arch", err)
	}
	platform, err := NormalizePlatform(trimOutput(osName), trimOutput(machine))
	if err != nil {
		return Result{}, stageErr("detect_platform", err)
	}
	localBinary, err := ResolveBinary(i.opts.BinaryDir, platform)
	if err != nil {
		return Result{}, stageErr("resolve_binary", err)
	}

	remoteTmp := "/tmp/" + platform.BinaryName()
	if err := remote.Upload(ctx, localBinary, remoteTmp); err != nil {
		return Result{}, stageErr("upload", err)
	}
	mode, err := installCommands(ctx, remote, platform, remoteTmp, port)
	if err != nil {
		return Result{}, err
	}
	message := "Agent installed and started"
	if mode == installModeUserLaunchAgent {
		message = "Agent installed and started in user LaunchAgent mode"
	}
	if err := verifyAgentReady(ctx, remote, port, i.verifyAttempts(), i.verifyDelay()); err != nil {
		return Result{}, stageErr("verify", err)
	}
	return Result{
		OK:       true,
		HostID:   host.ID,
		Platform: platform.String(),
		Message:  message,
	}, nil
}

// Uninstall removes the remote agent service and binary from host.
//
// 参数：
//   - ctx: 上下文，用于取消 SSH 命令
//   - host: 远端主机配置，包含 SSH 凭据
//   - removeData: 是否同时删除远端 agent 数据目录和日志
//
// 返回：
//   - 卸载成功结果，包含是否删除数据
//   - 分阶段错误，便于上层定位失败原因
func (i *Installer) Uninstall(ctx context.Context, host model.Host, removeData bool) (UninstallResult, error) {
	remote, err := i.factory(host)
	if err != nil {
		return UninstallResult{}, stageErr("connect", err)
	}
	defer remote.Close() //nolint:errcheck

	osName, err := remote.Run(ctx, "uname -s")
	if err != nil {
		return UninstallResult{}, stageErr("detect_os", err)
	}
	if err := uninstallCommands(ctx, remote, strings.ToLower(trimOutput(osName)), removeData); err != nil {
		return UninstallResult{}, err
	}
	return UninstallResult{
		OK:          true,
		HostID:      host.ID,
		RemovedData: removeData,
		Message:     "Agent uninstalled",
	}, nil
}

func installCommands(ctx context.Context, remote Remote, platform Platform, remoteTmp string, port int) (installMode, error) {
	if _, err := remote.Run(ctx, "sudo -n install -m 0755 "+remoteTmp+" /usr/local/bin/superdev-agent"); err != nil {
		if platform.OS == "darwin" && shouldUseMacOSUserLaunchAgent(err) {
			if err := installMacOSUserLaunchAgent(ctx, remote, remoteTmp, port); err != nil {
				return "", err
			}
			return installModeUserLaunchAgent, nil
		}
		return "", stageErr("install_binary", err)
	}
	switch platform.OS {
	case "linux":
		commands := []string{
			"sudo -n mkdir -p /var/lib/superdev-agent",
			"cat > /tmp/superdev-agent.service <<'EOF'\n" + LinuxSystemdUnit(port) + "EOF",
			"sudo -n install -m 0644 /tmp/superdev-agent.service /etc/systemd/system/superdev-agent.service",
			"sudo -n systemctl daemon-reload",
			"sudo -n systemctl enable superdev-agent.service",
			"sudo -n systemctl restart superdev-agent.service",
		}
		for _, cmd := range commands {
			if _, err := remote.Run(ctx, cmd); err != nil {
				return "", stageErr("install_systemd", err)
			}
		}
	case "darwin":
		commands := []string{
			"sudo -n mkdir -p '/Library/Application Support/SuperDev/Agent'",
			"cat > /tmp/dev.superdev.agent.plist <<'EOF'\n" + MacOSLaunchDaemonPlist(port) + "EOF",
			"sudo -n install -m 0644 /tmp/dev.superdev.agent.plist /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n chown root:wheel /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true",
			"sudo -n launchctl bootstrap system /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n launchctl kickstart -k system/dev.superdev.agent",
		}
		for _, cmd := range commands {
			if _, err := remote.Run(ctx, cmd); err != nil {
				return "", stageErr("install_launchd", err)
			}
		}
	default:
		return "", stageErr("install_service", fmt.Errorf("unsupported os %q", platform.OS))
	}
	return installModeSystem, nil
}

func uninstallCommands(ctx context.Context, remote Remote, osName string, removeData bool) error {
	switch osName {
	case "linux":
		commands := []string{
			"sudo -n systemctl stop superdev-agent.service || true",
			"sudo -n systemctl disable superdev-agent.service || true",
			"sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent",
			"sudo -n systemctl daemon-reload",
		}
		if removeData {
			commands = append(commands, "sudo -n rm -rf /var/lib/superdev-agent")
		}
		for _, cmd := range commands {
			if _, err := remote.Run(ctx, cmd); err != nil {
				return stageErr("uninstall_systemd", err)
			}
		}
	case "darwin":
		commands := []string{
			"sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true",
			"sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent",
		}
		if removeData {
			commands = append(commands, "sudo -n rm -rf '/Library/Application Support/SuperDev/Agent'")
		}
		for _, cmd := range commands {
			if _, err := remote.Run(ctx, cmd); err != nil {
				if shouldUseMacOSUserLaunchAgent(err) {
					return uninstallMacOSUserLaunchAgent(ctx, remote, removeData)
				}
				return stageErr("uninstall_launchd", err)
			}
		}
	default:
		return stageErr("uninstall_service", fmt.Errorf("unsupported os %q", osName))
	}
	return nil
}

func installMacOSUserLaunchAgent(ctx context.Context, remote Remote, remoteTmp string, port int) error {
	home, uid, err := macOSUserContext(ctx, remote)
	if err != nil {
		return stageErr("install_user_launchd", err)
	}
	paths := macOSUserAgentPaths(home)
	plist := MacOSUserLaunchAgentPlist(port, paths.binary, paths.dataDir, paths.stdoutLog, paths.stderrLog)
	commands := []string{
		"mkdir -p " + shellQuote(paths.binDir) + " " + shellQuote(paths.dataDir) + " " + shellQuote(paths.launchAgentsDir) + " " + shellQuote(paths.logsDir),
		"install -m 0755 " + remoteTmp + " " + shellQuote(paths.binary),
		"cat > " + shellQuote(paths.plist) + " <<'EOF'\n" + plist + "EOF",
		"chmod 0644 " + shellQuote(paths.plist),
		"launchctl bootout user/" + uid + " " + shellQuote(paths.plist) + " || true",
		"launchctl bootstrap user/" + uid + " " + shellQuote(paths.plist),
		"launchctl kickstart -k user/" + uid + "/dev.superdev.agent",
	}
	for _, cmd := range commands {
		if _, err := remote.Run(ctx, cmd); err != nil {
			return stageErr("install_user_launchd", err)
		}
	}
	return nil
}

func uninstallMacOSUserLaunchAgent(ctx context.Context, remote Remote, removeData bool) error {
	home, uid, err := macOSUserContext(ctx, remote)
	if err != nil {
		return stageErr("uninstall_user_launchd", err)
	}
	paths := macOSUserAgentPaths(home)
	commands := []string{
		"launchctl bootout user/" + uid + " " + shellQuote(paths.plist) + " || true",
		"rm -f " + shellQuote(paths.plist) + " " + shellQuote(paths.binary),
	}
	if removeData {
		commands = append(commands, "rm -rf "+shellQuote(paths.rootDir))
	}
	for _, cmd := range commands {
		if _, err := remote.Run(ctx, cmd); err != nil {
			return stageErr("uninstall_user_launchd", err)
		}
	}
	return nil
}

func macOSUserContext(ctx context.Context, remote Remote) (string, string, error) {
	home, err := remote.Run(ctx, "printf %s \"$HOME\"")
	if err != nil {
		return "", "", err
	}
	uid, err := remote.Run(ctx, "id -u")
	if err != nil {
		return "", "", err
	}
	home = trimOutput(home)
	uid = trimOutput(uid)
	if home == "" {
		return "", "", fmt.Errorf("empty remote home")
	}
	if uid == "" {
		return "", "", fmt.Errorf("empty remote uid")
	}
	return home, uid, nil
}

type macOSUserAgentPathSet struct {
	rootDir         string
	binDir          string
	dataDir         string
	launchAgentsDir string
	logsDir         string
	binary          string
	plist           string
	stdoutLog       string
	stderrLog       string
}

func macOSUserAgentPaths(home string) macOSUserAgentPathSet {
	rootDir := path.Join(home, "Library", "Application Support", "SuperDev", "Agent")
	binDir := path.Join(rootDir, "bin")
	dataDir := path.Join(rootDir, "data")
	launchAgentsDir := path.Join(home, "Library", "LaunchAgents")
	logsDir := path.Join(home, "Library", "Logs")
	return macOSUserAgentPathSet{
		rootDir:         rootDir,
		binDir:          binDir,
		dataDir:         dataDir,
		launchAgentsDir: launchAgentsDir,
		logsDir:         logsDir,
		binary:          path.Join(binDir, "superdev-agent"),
		plist:           path.Join(launchAgentsDir, "dev.superdev.agent.plist"),
		stdoutLog:       path.Join(logsDir, "superdev-agent.log"),
		stderrLog:       path.Join(logsDir, "superdev-agent.err.log"),
	}
}

func shouldUseMacOSUserLaunchAgent(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sudo") &&
		(strings.Contains(msg, "password is required") ||
			strings.Contains(msg, "not in the sudoers") ||
			strings.Contains(msg, "not allowed to execute"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (i *Installer) verifyAttempts() int {
	if i.opts.VerifyAttempts > 0 {
		return i.opts.VerifyAttempts
	}
	return 12
}

func (i *Installer) verifyDelay() time.Duration {
	if i.opts.VerifyDelay > 0 {
		return i.opts.VerifyDelay
	}
	return 500 * time.Millisecond
}

func verifyAgentReady(ctx context.Context, remote Remote, port int, attempts int, delay time.Duration) error {
	cmd := fmt.Sprintf("curl -fsS http://127.0.0.1:%d/api/hosts >/dev/null", port)
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if _, err := remote.Run(ctx, cmd); err != nil {
			lastErr = err
		} else {
			return nil
		}
		if attempt == attempts {
			break
		}
		// systemd/launchd 只保证进程被拉起，不保证 HTTP 监听点已经 ready。
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return lastErr
}

func stageErr(stage string, err error) error {
	if err == nil {
		return nil
	}
	return &InstallError{Stage: stage, Err: err}
}

func trimOutput(out string) string {
	return strings.TrimSpace(out)
}
