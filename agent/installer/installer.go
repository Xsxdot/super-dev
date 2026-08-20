// Package installer installs the SuperDev remote agent on SSH hosts.
//
// 职责：
//   - 检测远端 macOS/Linux/Windows 平台与 CPU 架构
//   - 解析随桌面包携带的 agent 二进制
//   - 编排上传、系统级安装、自启配置和启动验证
//
// 边界：
//   - 不持久化 Host 或安装历史
//   - 不管理 SSH 隧道生命周期
//   - Windows 通过 OpenSSH + schtasks 管理 agent，不走 WinRM
package installer

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

// Options 配置远端 agent 安装器。
type Options struct {
	BinaryDir      string
	VerifyAttempts int
	VerifyDelay    time.Duration
	BootstrapToken string
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

// RestartResult 描述一次远端 agent 重启结果。
type RestartResult struct {
	OK       bool   `json:"ok"`
	HostID   string `json:"host_id"`
	Platform string `json:"platform"`
	Message  string `json:"message"`
}

// UpdateResult 描述一次远端 agent 二进制更新结果。
type UpdateResult struct {
	OK       bool   `json:"ok"`
	HostID   string `json:"host_id"`
	Platform string `json:"platform"`
	Message  string `json:"message"`
}

type installMode string

const (
	installModeSystem          installMode = "system"
	installModeUserLaunchAgent installMode = "user_launch_agent"
	defaultVerifyAttempts                  = 90
	defaultVerifyDelay                     = time.Second
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
	return i.InstallWithOptions(ctx, host, serviceOptionsForHost(host, i.opts.BootstrapToken))
}

// InstallWithOptions 使用显式服务参数安装或重装远端 agent。
//
// 参数：
//   - ctx: 上下文，用于取消 SSH 命令和上传前置检查
//   - host: 远端主机配置，包含 SSH 凭据
//   - serviceOpts: 远端 agent 服务启动时使用的监听、安全参数
//
// 返回：
//   - 安装成功结果，包含 host ID 和平台
//   - 分阶段错误，便于上层定位失败原因
//
// 注意：
//   - Agent API 已将监听/TLS 配置从 Host 拆出，因此直推安装需通过该入口显式传参
func (i *Installer) InstallWithOptions(ctx context.Context, host model.Host, serviceOpts ServiceOptions) (Result, error) {
	if strings.TrimSpace(serviceOpts.User) == "" {
		serviceOpts.User = host.SSHUser
	}
	remote, err := i.factory(host)
	if err != nil {
		return Result{}, stageErr("connect", err)
	}
	defer remote.Close() //nolint:errcheck

	platform, err := detectPlatform(ctx, remote)
	if err != nil {
		return Result{}, stageErr("detect_platform", err)
	}
	localBinary, err := ResolveBinary(i.opts.BinaryDir, platform)
	if err != nil {
		return Result{}, stageErr("resolve_binary", err)
	}

	remoteTmp, err := remoteTempPath(ctx, remote, platform)
	if err != nil {
		return Result{}, stageErr("detect_temp", err)
	}
	if err := remote.Upload(ctx, localBinary, remoteTmp); err != nil {
		return Result{}, stageErr("upload", err)
	}
	mode, err := installCommands(ctx, remote, platform, remoteTmp, serviceOpts)
	if err != nil {
		return Result{}, err
	}
	message := "Agent installed and started"
	if mode == installModeUserLaunchAgent {
		message = "Agent installed and started in user LaunchAgent mode"
	}
	if err := verifyAgentReady(ctx, remote, serviceOpts.Port, i.verifyAttempts(), i.verifyDelay()); err != nil {
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
	baseLog := logger.GetLogger().WithEntryName("AgentInstaller").
		WithField("host_id", host.ID).
		WithField("purge", removeData)
	baseLog.WithField("stage", "connect").Info("开始连接远端 Host 执行 Agent 卸载")
	remote, err := i.factory(host)
	if err != nil {
		baseLog.WithField("stage", "connect").WithErr(err).Error("连接远端 Host 失败")
		return UninstallResult{}, stageErr("connect", err)
	}
	defer func() {
		if closeErr := remote.Close(); closeErr != nil {
			baseLog.WithField("stage", "close").WithErr(closeErr).Error("关闭 Agent 卸载 SSH 连接失败")
		}
	}()
	baseLog.WithField("stage", "connect").Info("远端 Host 连接已建立")

	baseLog.WithField("stage", "detect_platform").Info("开始检测 Agent 卸载平台")
	normalizedOS, err := detectOS(ctx, remote)
	if err != nil {
		baseLog.WithField("stage", "detect_platform").WithErr(err).Error("检测 Agent 卸载平台失败")
		return UninstallResult{}, stageErr("detect_platform", err)
	}
	platformLog := baseLog.WithField("platform", normalizedOS)
	platformLog.WithField("stage", "detect_platform").Info("Agent 卸载平台检测完成")
	platformLog.WithField("stage", "platform_cleanup").Info("开始清理 Agent 自有资源")
	if err := uninstallCommands(ctx, remote, platformLog, normalizedOS, removeData); err != nil {
		platformLog.WithField("stage", uninstallErrorStage(err, "platform_cleanup")).WithErr(err).Error("清理 Agent 自有资源失败")
		return UninstallResult{}, err
	}
	platformLog.WithField("stage", "platform_cleanup").Info("Agent 自有资源清理完成")
	return UninstallResult{
		OK:          true,
		HostID:      host.ID,
		RemovedData: removeData,
		Message:     "Agent uninstalled",
	}, nil
}

func uninstallErrorStage(err error, fallback string) string {
	var installErr *InstallError
	if errors.As(err, &installErr) && installErr.Stage != "" {
		return installErr.Stage
	}
	return fallback
}

// Restart restarts the remote agent service on host.
//
// 参数：
//   - ctx: 上下文，用于取消 SSH 命令
//   - host: 远端主机配置，包含 SSH 凭据
//
// 返回：
//   - 重启成功结果，包含 host ID 和平台
//   - 分阶段错误，便于上层定位失败原因
//
// 注意：
//   - TLS auto provision 后监听会从 HTTP 切到 HTTPS，因此这里不做 HTTP 探活
func (i *Installer) Restart(ctx context.Context, host model.Host) (RestartResult, error) {
	remote, err := i.factory(host)
	if err != nil {
		return RestartResult{}, stageErr("connect", err)
	}
	defer remote.Close() //nolint:errcheck

	normalizedOS, err := detectOS(ctx, remote)
	if err != nil {
		return RestartResult{}, stageErr("detect_platform", err)
	}
	mode, err := restartCommands(ctx, remote, normalizedOS)
	if err != nil {
		return RestartResult{}, err
	}
	message := "Agent restarted"
	if mode == installModeUserLaunchAgent {
		message = "Agent restarted in user LaunchAgent mode"
	}
	return RestartResult{
		OK:       true,
		HostID:   host.ID,
		Platform: normalizedOS,
		Message:  message,
	}, nil
}

// UpdateBinary replaces the remote agent binary and restarts the existing service.
//
// 参数：
//   - ctx: 上下文，用于取消 SSH 命令和上传
//   - host: 远端主机配置，包含 SSH 凭据
//
// 返回：
//   - 更新成功结果，包含 host ID 和平台
//   - 分阶段错误，便于 API 和 UI 展示
//
// 注意：
//   - 该方法不重写 systemd/launchd 配置
//   - 该方法不删除 security.json，不触发 bootstrap/provision
func (i *Installer) UpdateBinary(ctx context.Context, host model.Host) (UpdateResult, error) {
	remote, err := i.factory(host)
	if err != nil {
		return UpdateResult{}, stageErr("connect", err)
	}
	defer remote.Close() //nolint:errcheck

	platform, err := detectPlatform(ctx, remote)
	if err != nil {
		return UpdateResult{}, stageErr("detect_platform", err)
	}
	localBinary, err := ResolveBinary(i.opts.BinaryDir, platform)
	if err != nil {
		return UpdateResult{}, stageErr("resolve_binary", err)
	}

	remoteTmp, err := remoteTempPath(ctx, remote, platform)
	if err != nil {
		return UpdateResult{}, stageErr("detect_temp", err)
	}
	if err := remote.Upload(ctx, localBinary, remoteTmp); err != nil {
		return UpdateResult{}, stageErr("upload", err)
	}
	mode, err := updateBinaryCommands(ctx, remote, platform, remoteTmp)
	if err != nil {
		return UpdateResult{}, err
	}
	message := "Agent binary updated and service restarted"
	if mode == installModeUserLaunchAgent {
		message = "Agent binary updated and user LaunchAgent restarted"
	}
	return UpdateResult{OK: true, HostID: host.ID, Platform: platform.String(), Message: message}, nil
}

func serviceOptionsForHost(host model.Host, bootstrapToken string) ServiceOptions {
	opts := ServiceOptions{
		BindAddress:    "127.0.0.1",
		Port:           model.DefaultAgentListenPort,
		RequireAuth:    strings.TrimSpace(bootstrapToken) != "",
		BootstrapToken: bootstrapToken,
		User:           host.SSHUser,
	}
	return opts
}

func detectPlatform(ctx context.Context, remote Remote) (Platform, error) {
	osName, err := remote.Run(ctx, "uname -s")
	if err == nil {
		machine, archErr := remote.Run(ctx, "uname -m")
		if archErr != nil {
			return Platform{}, archErr
		}
		platform, normErr := NormalizePlatform(trimOutput(osName), trimOutput(machine))
		if normErr == nil {
			log.Printf("[installer] detected platform=%s", platform.String())
			return platform, nil
		}
		log.Printf("[installer] uname platform unsupported os=%q: %v", trimOutput(osName), normErr)
	} else {
		log.Printf("[installer] uname os detection failed, trying windows fallback: %v", err)
	}
	return detectWindowsPlatform(ctx, remote, err)
}

func detectOS(ctx context.Context, remote Remote) (string, error) {
	osName, err := remote.Run(ctx, "uname -s")
	if err == nil {
		osValue, normErr := normalizeOSName(trimOutput(osName))
		if normErr == nil {
			log.Printf("[installer] detected os=%s", osValue)
			return osValue, nil
		}
		log.Printf("[installer] uname os unsupported os=%q: %v", trimOutput(osName), normErr)
	} else {
		log.Printf("[installer] uname os detection failed, trying windows fallback: %v", err)
	}
	platform, winErr := detectWindowsPlatform(ctx, remote, err)
	if winErr != nil {
		return "", winErr
	}
	return platform.OS, nil
}

func detectWindowsPlatform(ctx context.Context, remote Remote, previous error) (Platform, error) {
	out, err := remote.Run(ctx, "cmd /c ver")
	if err != nil {
		if previous != nil {
			return Platform{}, fmt.Errorf("unix detection failed: %v; windows detection failed: %w", previous, err)
		}
		return Platform{}, err
	}
	if !strings.Contains(strings.ToLower(out), "windows") {
		return Platform{}, fmt.Errorf("unsupported os %q", trimOutput(out))
	}
	arch, err := remote.Run(ctx, "cmd /c echo %PROCESSOR_ARCHITECTURE%")
	if err != nil {
		return Platform{}, err
	}
	platform, err := NormalizePlatform("windows_nt", trimOutput(arch))
	if err != nil {
		return Platform{}, err
	}
	log.Printf("[installer] detected platform=%s", platform.String())
	return platform, nil
}

func normalizeOSName(osName string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(osName)) {
	case "darwin":
		return "darwin", nil
	case "linux":
		return "linux", nil
	case "windows", "windows_nt":
		return "windows", nil
	default:
		if strings.Contains(strings.ToLower(osName), "windows") {
			return "windows", nil
		}
		return "", fmt.Errorf("unsupported os %q", strings.TrimSpace(osName))
	}
}

func remoteTempPath(ctx context.Context, remote Remote, platform Platform) (string, error) {
	if platform.OS != "windows" {
		return "/tmp/" + platform.BinaryName(), nil
	}
	out, err := remote.Run(ctx, "cmd /c echo %TEMP%")
	if err != nil {
		return "", err
	}
	tempDir := strings.TrimSpace(out)
	if tempDir == "" || strings.Contains(tempDir, "%TEMP%") {
		tempDir = `C:\Windows\Temp`
	}
	// scp over Windows OpenSSH accepts slash-separated absolute paths more consistently.
	return strings.TrimRight(strings.ReplaceAll(tempDir, `\`, "/"), "/") + "/" + platform.BinaryName(), nil
}

func bindHostFromDirectAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		if u, err := url.Parse(address); err == nil {
			return u.Hostname()
		}
	}
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		return host
	}
	return address
}

func installCommands(ctx context.Context, remote Remote, platform Platform, remoteTmp string, opts ServiceOptions) (installMode, error) {
	if platform.OS == "windows" {
		if err := installWindowsScheduledTask(ctx, remote, remoteTmp, opts); err != nil {
			return "", err
		}
		return installModeSystem, nil
	}
	fillServiceHomeDir(ctx, remote, platform.OS, &opts)
	if _, err := remote.Run(ctx, "sudo -n install -m 0755 "+remoteTmp+" /usr/local/bin/superdev-agent"); err != nil {
		if platform.OS == "darwin" && shouldUseMacOSUserLaunchAgent(err) {
			if err := installMacOSUserLaunchAgent(ctx, remote, remoteTmp, opts); err != nil {
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
			// 必须先停服务再删 security.json：旧 agent 仍在运行时删除，
			// 它随后任何一次状态落盘都会把 provisioned 状态写回来，
			// 重装下发的新 bootstrap token 就永远无法生效（provision 一直 401）。
			stopServiceBeforeResetCommand("sudo -n systemctl stop superdev-agent.service || true", opts),
			resetSecurityStateCommand("sudo -n ", "/var/lib/superdev-agent/security.json", opts),
			"cat > /tmp/superdev-agent.service <<'EOF'\n" + LinuxSystemdUnit(opts) + "EOF",
			"sudo -n install -m 0644 /tmp/superdev-agent.service /etc/systemd/system/superdev-agent.service",
			"sudo -n systemctl daemon-reload",
			"sudo -n systemctl enable superdev-agent.service",
			"sudo -n systemctl restart superdev-agent.service",
		}
		for _, cmd := range commands {
			if strings.TrimSpace(cmd) == "" {
				continue
			}
			if _, err := remote.Run(ctx, cmd); err != nil {
				return "", stageErr("install_systemd", err)
			}
		}
	case "darwin":
		commands := []string{
			"sudo -n mkdir -p '/Library/Application Support/SuperDev/Agent'",
			// 同 linux：先 bootout 停掉旧 agent，再删状态，避免它把 provisioned 写回来。
			stopServiceBeforeResetCommand("sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true", opts),
			resetSecurityStateCommand("sudo -n ", "/Library/Application Support/SuperDev/Agent/security.json", opts),
			"cat > /tmp/dev.superdev.agent.plist <<'EOF'\n" + MacOSLaunchDaemonPlist(opts) + "EOF",
			"sudo -n install -m 0644 /tmp/dev.superdev.agent.plist /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n chown root:wheel /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist || true",
			"sudo -n launchctl bootstrap system /Library/LaunchDaemons/dev.superdev.agent.plist",
			"sudo -n launchctl kickstart -k system/dev.superdev.agent",
		}
		for _, cmd := range commands {
			if strings.TrimSpace(cmd) == "" {
				continue
			}
			if _, err := remote.Run(ctx, cmd); err != nil {
				return "", stageErr("install_launchd", err)
			}
		}
	default:
		return "", stageErr("install_service", fmt.Errorf("unsupported os %q", platform.OS))
	}
	return installModeSystem, nil
}

func updateBinaryCommands(ctx context.Context, remote Remote, platform Platform, remoteTmp string) (installMode, error) {
	switch platform.OS {
	case "linux":
		if _, err := remote.Run(ctx, "sudo -n install -m 0755 "+remoteTmp+" /usr/local/bin/superdev-agent"); err != nil {
			return "", stageErr("replace_binary", err)
		}
		return restartCommands(ctx, remote, "linux")
	case "darwin":
		if _, err := remote.Run(ctx, "sudo -n install -m 0755 "+remoteTmp+" /usr/local/bin/superdev-agent"); err != nil {
			if shouldUseMacOSUserLaunchAgent(err) {
				if err := updateMacOSUserLaunchAgentBinary(ctx, remote, remoteTmp); err != nil {
					return "", err
				}
				if err := restartMacOSUserLaunchAgent(ctx, remote); err != nil {
					return "", err
				}
				return installModeUserLaunchAgent, nil
			}
			return "", stageErr("replace_binary", err)
		}
		return restartCommands(ctx, remote, "darwin")
	case "windows":
		if err := replaceWindowsBinary(ctx, remote, remoteTmp); err != nil {
			return "", err
		}
		return restartCommands(ctx, remote, "windows")
	default:
		return "", stageErr("replace_binary", fmt.Errorf("unsupported os %q", platform.OS))
	}
}

func installWindowsScheduledTask(ctx context.Context, remote Remote, remoteTmp string, opts ServiceOptions) error {
	if err := replaceWindowsBinary(ctx, remote, remoteTmp); err != nil {
		return err
	}
	commands := []string{
		// 先停任务再删状态：顺序反了的话，仍在运行的旧 agent 会把 provisioned
		// 状态重新落盘，重装下发的新 bootstrap token 就永远不会生效。
		`cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`,
		windowsResetSecurityStateCommand(opts),
		`cmd /c schtasks /Create /TN SuperDevAgent /SC ONSTART /RU SYSTEM /TR ` + windowsQuote(windowsAgentCommand(opts)) + ` /F`,
		`cmd /c schtasks /Run /TN SuperDevAgent`,
	}
	for _, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if _, err := remote.Run(ctx, cmd); err != nil {
			return stageErr("install_windows_task", err)
		}
	}
	return nil
}

func replaceWindowsBinary(ctx context.Context, remote Remote, remoteTmp string) error {
	commands := []string{
		`cmd /c if not exist "C:\ProgramData\SuperDev\Agent" mkdir "C:\ProgramData\SuperDev\Agent"`,
		`cmd /c if not exist "C:\ProgramData\SuperDev\Agent\data" mkdir "C:\ProgramData\SuperDev\Agent\data"`,
		`cmd /c copy /Y ` + windowsQuote(windowsCommandPath(remoteTmp)) + ` "C:\ProgramData\SuperDev\Agent\superdev-agent.exe"`,
	}
	for _, cmd := range commands {
		if _, err := remote.Run(ctx, cmd); err != nil {
			return stageErr("replace_binary", err)
		}
	}
	return nil
}

func updateMacOSUserLaunchAgentBinary(ctx context.Context, remote Remote, remoteTmp string) error {
	operationLog := logger.GetLogger().WithEntryName("AgentInstaller").WithField("stage", "replace_user_binary")
	home, _, err := macOSUserContext(ctx, remote, operationLog)
	if err != nil {
		return stageErr("replace_binary", err)
	}
	paths := macOSUserAgentPaths(home)
	if _, err := remote.Run(ctx, "install -m 0755 "+remoteTmp+" "+shellQuote(paths.binary)); err != nil {
		return stageErr("replace_binary", err)
	}
	return nil
}

// stopServiceBeforeResetCommand 在需要重置安全状态时先停掉正在运行的旧 agent。
//
// 参数：
//   - stopCommand: 平台对应的停服务命令（自带 `|| true`，服务不存在时不算失败）
//   - opts: 本次安装参数，仅在下发了 bootstrap token 时才需要重置
//
// 返回：
//   - 需要执行的停服务命令；无需重置时返回空串，由调用方跳过
//
// 注意：
//   - 必须与 resetSecurityStateCommand 使用同一个触发条件，二者要么都执行要么都不执行
//   - 不停服务就删 security.json 会被仍在运行的旧 agent 重新写回，
//     导致重装下发的新 bootstrap token 永久失效
func stopServiceBeforeResetCommand(stopCommand string, opts ServiceOptions) string {
	if strings.TrimSpace(opts.BootstrapToken) == "" {
		return ""
	}
	return stopCommand
}

func resetSecurityStateCommand(prefix string, securityPath string, opts ServiceOptions) string {
	if strings.TrimSpace(opts.BootstrapToken) == "" {
		return ""
	}
	return prefix + "rm -f " + shellQuote(securityPath)
}

func windowsResetSecurityStateCommand(opts ServiceOptions) string {
	if strings.TrimSpace(opts.BootstrapToken) == "" {
		return ""
	}
	return `cmd /c del /F /Q "C:\ProgramData\SuperDev\Agent\data\security.json" 2>NUL`
}

func windowsAgentCommand(opts ServiceOptions) string {
	args := []string{windowsQuote(`C:\ProgramData\SuperDev\Agent\superdev-agent.exe`)}
	for _, arg := range opts.commandArgs() {
		args = append(args, windowsQuote(arg))
	}
	args = append(args, windowsQuote("--data"), windowsQuote(`C:\ProgramData\SuperDev\Agent\data`))
	return strings.Join(args, " ")
}

func windowsCommandPath(value string) string {
	return strings.ReplaceAll(value, "/", `\`)
}

func windowsQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

type remoteOperation struct {
	Action   string
	Resource string
	Command  string
}

const (
	linuxSystemdLoadStateCommand = "systemctl show superdev-agent.service --property=LoadState --property=ActiveState --property=FragmentPath --property=ExecStart --no-pager"
	linuxSystemdUnitPath         = "/etc/systemd/system/superdev-agent.service"
	linuxAgentBinaryPath         = "/usr/local/bin/superdev-agent"
	macOSOwnershipAbsentMarker   = "__SUPERDEV_RESOURCE_ABSENT__"
	macOSLayoutAmbiguousMarker   = "ambiguous"
	macOSLayoutAbsentMarker      = "absent"
	windowsTaskAbsentMarker      = "__SUPERDEV_TASK_ABSENT__"
	windowsTaskQueryCommand      = `cmd /c if not exist "%SystemRoot%\System32\Tasks\SuperDevAgent" (echo __SUPERDEV_TASK_ABSENT__) else schtasks /Query /TN SuperDevAgent /XML`
	windowsAgentBinaryPath       = `C:\ProgramData\SuperDev\Agent\superdev-agent.exe`
)

func runObservedRemoteOperation(ctx context.Context, remote Remote, operationLog *logger.Log, stage string, operation remoteOperation) (string, error) {
	// 完整命令可能包含主机路径等环境信息，日志只使用稳定的 action/resource 标签。
	entry := operationLog.WithField("stage", stage).
		WithField("action", operation.Action).
		WithField("resource", operation.Resource)
	entry.Info("开始执行 Agent 远端清理动作")
	output, err := remote.Run(ctx, operation.Command)
	if err != nil {
		entry.WithErr(err).Error("Agent 远端清理动作失败")
		return "", err
	}
	entry.Info("Agent 远端清理动作完成")
	return output, nil
}

func runObservedRemoteOperations(ctx context.Context, remote Remote, operationLog *logger.Log, stage string, operations []remoteOperation) error {
	for _, operation := range operations {
		if _, err := runObservedRemoteOperation(ctx, remote, operationLog, stage, operation); err != nil {
			return err
		}
	}
	return nil
}

func stopLinuxSystemdUnit(ctx context.Context, remote Remote, operationLog *logger.Log) (bool, error) {
	output, err := runObservedRemoteOperation(ctx, remote, operationLog, "uninstall_systemd", remoteOperation{
		Action:   "inspect",
		Resource: "systemd_service",
		Command:  linuxSystemdLoadStateCommand,
	})
	if err != nil {
		return false, err
	}

	state := make(map[string]string, 2)
	for _, line := range strings.Split(output, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			state[key] = value
		}
	}
	loadState, hasLoadState := state["LoadState"]
	activeState, hasActiveState := state["ActiveState"]
	fragmentPath, hasFragmentPath := state["FragmentPath"]
	execStart, hasExecStart := state["ExecStart"]
	if !hasLoadState || !hasActiveState || !hasFragmentPath || !hasExecStart {
		return false, fmt.Errorf("systemctl 未返回完整的 Agent unit 状态")
	}

	stateLog := operationLog.WithField("stage", "uninstall_systemd").
		WithField("resource", "systemd_service").
		WithField("load_state", loadState).
		WithField("active_state", activeState)
	// 只有 systemd 同时明确 unit 未加载且未活动时才视为幂等 no-op；活动进程优先于文件状态。
	if loadState == "not-found" && activeState == "inactive" {
		if fragmentPath != "" || execStart != "" {
			return false, fmt.Errorf("不存在的 Agent unit 返回了未知所有权信息")
		}
		stateLog.Info("Agent systemd unit 不存在，无需停止")
		return false, nil
	}
	if fragmentPath != linuxSystemdUnitPath {
		return false, fmt.Errorf("同名 systemd unit 不属于 SuperDev Agent: FragmentPath=%q", fragmentPath)
	}
	execStartPaths := systemdExecStartPaths(execStart)
	if len(execStartPaths) != 1 || execStartPaths[0] != linuxAgentBinaryPath {
		return false, fmt.Errorf("同名 systemd unit 不属于 SuperDev Agent: ExecStart 非 canonical binary")
	}
	stateLog.Info("检测到 Agent systemd unit，准备停止")
	_, err = runObservedRemoteOperation(ctx, remote, operationLog, "uninstall_systemd", remoteOperation{
		Action:   "stop",
		Resource: "systemd_service",
		Command:  "sudo -n systemctl stop superdev-agent.service",
	})
	return true, err
}

func systemdExecStartPaths(execStart string) []string {
	const marker = "path="
	paths := make([]string, 0, 1)
	for remaining := execStart; ; {
		start := strings.Index(remaining, marker)
		if start < 0 {
			break
		}
		value := remaining[start+len(marker):]
		if end := strings.IndexAny(value, " ;}\t\r\n"); end >= 0 {
			paths = append(paths, strings.TrimSpace(value[:end]))
			remaining = value[end:]
			continue
		}
		paths = append(paths, strings.TrimSpace(value))
		break
	}
	return paths
}

func uninstallCommands(ctx context.Context, remote Remote, operationLog *logger.Log, osName string, removeData bool) error {
	switch osName {
	case "linux":
		unitExists, err := stopLinuxSystemdUnit(ctx, remote, operationLog)
		if err != nil {
			return stageErr("uninstall_systemd", err)
		}
		// 命令仅指向 SuperDev Agent 自有 unit 和安装路径；缺失表示已经是目标状态。
		operations := make([]remoteOperation, 0, 4)
		if unitExists {
			// unit 文件可能已被部分卸载，因此 disable 仍以文件存在为边界；stop 已依据 systemd 运行状态完成。
			operations = append(operations, remoteOperation{Action: "disable", Resource: "systemd_service", Command: "if [ -e /etc/systemd/system/superdev-agent.service ]; then sudo -n systemctl disable superdev-agent.service; fi"})
		}
		operations = append(operations,
			remoteOperation{Action: "delete", Resource: "service_and_binary", Command: "sudo -n rm -f /etc/systemd/system/superdev-agent.service /usr/local/bin/superdev-agent"},
			remoteOperation{Action: "reload", Resource: "systemd_manager", Command: "sudo -n systemctl daemon-reload"},
		)
		if removeData {
			operations = append(operations, remoteOperation{Action: "purge", Resource: "data", Command: "sudo -n rm -rf /var/lib/superdev-agent"})
		}
		if err := runObservedRemoteOperations(ctx, remote, operationLog, "uninstall_systemd", operations); err != nil {
			return stageErr("uninstall_systemd", err)
		}
	case "darwin":
		mode, err := detectMacOSUninstallMode(ctx, remote, operationLog, removeData)
		if err != nil {
			return stageErr("uninstall_launchd", err)
		}
		if mode == installModeUserLaunchAgent {
			return uninstallMacOSUserLaunchAgent(ctx, remote, operationLog, removeData)
		}

		if err := stopMacOSLaunchdJob(
			ctx,
			remote,
			operationLog,
			"uninstall_launchd",
			"launch_daemon",
			"system/dev.superdev.agent",
			"/Library/LaunchDaemons/dev.superdev.agent.plist",
			"/usr/local/bin/superdev-agent",
			true,
			"sudo -n launchctl bootout system /Library/LaunchDaemons/dev.superdev.agent.plist",
		); err != nil {
			return stageErr("uninstall_launchd", err)
		}

		// LaunchDaemon 的数据与日志位置由我们生成的 plist 固定，purge 只删除这些 Agent 自有路径。
		operations := []remoteOperation{
			{Action: "delete", Resource: "launch_daemon_and_binary", Command: "sudo -n rm -f /Library/LaunchDaemons/dev.superdev.agent.plist /usr/local/bin/superdev-agent"},
		}
		if removeData {
			operations = append(operations,
				remoteOperation{Action: "purge", Resource: "data", Command: "sudo -n rm -rf '/Library/Application Support/SuperDev/Agent'"},
				remoteOperation{Action: "purge", Resource: "logs", Command: "sudo -n rm -f /var/log/superdev-agent.log /var/log/superdev-agent.err.log"},
			)
		}
		if err := runObservedRemoteOperations(ctx, remote, operationLog, "uninstall_launchd", operations); err != nil {
			return stageErr("uninstall_launchd", err)
		}
	case "windows":
		taskExists, err := verifyWindowsScheduledTaskOwnership(ctx, remote, operationLog)
		if err != nil {
			return stageErr("uninstall_windows_task", err)
		}
		// 先检查 Agent 自有路径，使缺失资源成为成功 no-op，同时保留真实权限错误的非零退出码。
		operations := make([]remoteOperation, 0, 2)
		if taskExists {
			operations = append(operations,
				// 仅在 Action 已验证指向 canonical Agent binary 后，才允许变更同名任务。
				remoteOperation{Action: "stop", Resource: "scheduled_task", Command: `cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`},
				remoteOperation{Action: "delete", Resource: "scheduled_task", Command: `cmd /c schtasks /Delete /TN SuperDevAgent /F`},
			)
		}
		if err := runObservedRemoteOperations(ctx, remote, operationLog, "uninstall_windows_task", operations); err != nil {
			return stageErr("uninstall_windows_task", err)
		}
		if err := verifyWindowsAgentStopped(ctx, remote, operationLog); err != nil {
			return stageErr("uninstall_windows_task", err)
		}
		operations = []remoteOperation{
			{Action: "delete", Resource: "binary", Command: `cmd /c if exist "C:\ProgramData\SuperDev\Agent\superdev-agent.exe" del /F /Q "C:\ProgramData\SuperDev\Agent\superdev-agent.exe"`},
		}
		if removeData {
			operations = append(operations, remoteOperation{Action: "purge", Resource: "data_and_logs", Command: `cmd /c if exist "C:\ProgramData\SuperDev\Agent" rmdir /S /Q "C:\ProgramData\SuperDev\Agent"`})
		}
		if err := runObservedRemoteOperations(ctx, remote, operationLog, "uninstall_windows_task", operations); err != nil {
			return stageErr("uninstall_windows_task", err)
		}
	default:
		return stageErr("uninstall_service", fmt.Errorf("unsupported os %q", osName))
	}
	return nil
}

func restartCommands(ctx context.Context, remote Remote, osName string) (installMode, error) {
	switch osName {
	case "linux":
		if _, err := remote.Run(ctx, "sudo -n systemctl restart superdev-agent.service"); err != nil {
			return "", stageErr("restart_systemd", err)
		}
	case "darwin":
		if _, err := remote.Run(ctx, "sudo -n launchctl kickstart -k system/dev.superdev.agent"); err != nil {
			if shouldUseMacOSUserLaunchAgent(err) {
				if err := restartMacOSUserLaunchAgent(ctx, remote); err != nil {
					return "", err
				}
				return installModeUserLaunchAgent, nil
			}
			return "", stageErr("restart_launchd", err)
		}
	case "windows":
		commands := []string{
			`cmd /c schtasks /End /TN SuperDevAgent 2>NUL || exit /b 0`,
			`cmd /c schtasks /Run /TN SuperDevAgent`,
		}
		for _, cmd := range commands {
			if _, err := remote.Run(ctx, cmd); err != nil {
				return "", stageErr("restart_windows_task", err)
			}
		}
	default:
		return "", stageErr("restart_service", fmt.Errorf("unsupported os %q", osName))
	}
	return installModeSystem, nil
}

func macOSUninstallModeProbe(removeData bool) string {
	_ = removeData // 保留参数以维持调用契约；保留数据和日志始终参与重试布局识别。
	systemPaths := []string{
		"[ -e /usr/local/bin/superdev-agent ]",
		"[ -d '/Library/Application Support/SuperDev/Agent' ]",
		"[ -e /var/log/superdev-agent.log ]",
		"[ -e /var/log/superdev-agent.err.log ]",
	}
	userPaths := []string{
		"[ -e \"$HOME/Library/Application Support/SuperDev/Agent/bin/superdev-agent\" ]",
		"[ -d \"$HOME/Library/Application Support/SuperDev/Agent/data\" ]",
		"[ -e \"$HOME/Library/Logs/superdev-agent.log\" ]",
		"[ -e \"$HOME/Library/Logs/superdev-agent.err.log\" ]",
	}
	systemPresent := strings.Join(systemPaths, " || ")
	userPresent := strings.Join(userPaths, " || ")
	// 路径证据不做优先级猜测；两种布局同时留下 Agent 自有路径时必须交由上层拒绝自动卸载。
	return "if { " + systemPresent + "; } && { " + userPresent + "; }; then printf " + macOSLayoutAmbiguousMarker +
		"; elif " + systemPresent + "; then printf system; elif " + userPresent +
		"; then printf user_launch_agent; else printf " + macOSLayoutAbsentMarker + "; fi"
}

func verifyWindowsScheduledTaskOwnership(ctx context.Context, remote Remote, operationLog *logger.Log) (bool, error) {
	output, err := runObservedRemoteOperation(ctx, remote, operationLog, "uninstall_windows_task", remoteOperation{
		Action: "inspect", Resource: "scheduled_task", Command: windowsTaskQueryCommand,
	})
	if err != nil {
		return false, err
	}
	if trimOutput(output) == windowsTaskAbsentMarker {
		operationLog.WithField("stage", "uninstall_windows_task").
			WithField("resource", "scheduled_task").Info("Windows Agent Scheduled Task 不存在，无需停止")
		return false, nil
	}

	var task struct {
		Actions struct {
			Action []struct {
				XMLName xml.Name
				Command string `xml:"Command"`
			} `xml:",any"`
		} `xml:"Actions"`
	}
	// schtasks 声明 UTF-16，但 SSH 通道可能已转为单字节文本；去除 NUL 并统一声明后再解析结构。
	normalizedXML := strings.ReplaceAll(output, "\x00", "")
	normalizedXML = strings.ReplaceAll(normalizedXML, `encoding="UTF-16"`, `encoding="UTF-8"`)
	normalizedXML = strings.ReplaceAll(normalizedXML, `encoding="utf-16"`, `encoding="UTF-8"`)
	if err := xml.Unmarshal([]byte(normalizedXML), &task); err != nil {
		return false, fmt.Errorf("解析 SuperDevAgent Scheduled Task XML 失败: %w", err)
	}
	if len(task.Actions.Action) != 1 || task.Actions.Action[0].XMLName.Local != "Exec" {
		return false, fmt.Errorf("同名 Scheduled Task 必须仅包含一个 Exec Action")
	}
	command := strings.Trim(strings.TrimSpace(task.Actions.Action[0].Command), `"`)
	if !strings.EqualFold(command, windowsAgentBinaryPath) {
		return false, fmt.Errorf("同名 Scheduled Task 不属于 SuperDev Agent: Action 非 canonical binary")
	}
	return true, nil
}

func verifyWindowsAgentStopped(ctx context.Context, remote Remote, operationLog *logger.Log) error {
	entry := operationLog.WithField("stage", "uninstall_windows_task").
		WithField("action", "verify").
		WithField("resource", "agent_process")
	entry.Info("开始验证 Windows Agent 进程已停止")
	output, err := remote.Run(ctx, `cmd /c tasklist /FI "IMAGENAME eq superdev-agent.exe" /NH`)
	if err != nil {
		entry.WithErr(err).Error("验证 Windows Agent 进程失败")
		return err
	}
	if strings.Contains(strings.ToLower(output), "superdev-agent.exe") {
		err := fmt.Errorf("SuperDev Agent process is still running")
		entry.WithErr(err).Error("Windows Agent 进程未停止")
		return err
	}
	entry.Info("Windows Agent 进程已停止")
	return nil
}

func stopMacOSLaunchdJob(
	ctx context.Context,
	remote Remote,
	operationLog *logger.Log,
	stage string,
	resource string,
	target string,
	plistPath string,
	expectedBinary string,
	requiresSudo bool,
	bootoutCommand string,
) error {
	loaded, err := macOSLaunchdJobLoaded(ctx, remote, operationLog, stage, resource, target)
	if err != nil {
		return err
	}
	if _, err := inspectMacOSPlistOwnership(ctx, remote, operationLog, stage, resource, plistPath, expectedBinary, requiresSudo, loaded); err != nil {
		return err
	}
	if !loaded {
		return nil
	}
	_, err = runObservedRemoteOperation(ctx, remote, operationLog, stage, remoteOperation{
		Action:   "stop",
		Resource: resource,
		Command:  bootoutCommand,
	})
	return err
}

func inspectMacOSPlistOwnership(
	ctx context.Context,
	remote Remote,
	operationLog *logger.Log,
	stage string,
	resource string,
	plistPath string,
	expectedBinary string,
	requiresSudo bool,
	jobLoaded bool,
) (bool, error) {
	program, err := runObservedRemoteOperation(ctx, remote, operationLog, stage, remoteOperation{
		Action:   "inspect",
		Resource: resource + "_plist",
		Command:  macOSPlistProgramProbe(plistPath, requiresSudo),
	})
	if err != nil {
		return false, err
	}
	program = trimOutput(program)
	if program == macOSOwnershipAbsentMarker {
		if jobLoaded {
			return false, fmt.Errorf("同名 launchd job 已加载但 canonical plist 不存在，无法验证所有权")
		}
		return false, nil
	}
	if program != expectedBinary {
		return false, fmt.Errorf("同名 launchd job 不属于 SuperDev Agent: ProgramArguments[0]=%q", program)
	}
	return true, nil
}

func macOSPlistProgramProbe(plistPath string, requiresSudo bool) string {
	prefix := ""
	if requiresSudo {
		prefix = "sudo -n "
	}
	return "if [ -e " + shellQuote(plistPath) + " ]; then " + prefix +
		"/usr/libexec/PlistBuddy -c 'Print :ProgramArguments:0' " + shellQuote(plistPath) +
		"; else printf " + shellQuote(macOSOwnershipAbsentMarker) + "; fi"
}

func macOSLaunchdJobLoaded(
	ctx context.Context,
	remote Remote,
	operationLog *logger.Log,
	stage string,
	resource string,
	target string,
) (bool, error) {
	entry := operationLog.WithField("stage", stage).
		WithField("action", "detect").
		WithField("resource", resource)
	entry.Info("开始检测 macOS Agent launchd job")
	if _, err := remote.Run(ctx, "launchctl print "+target); err != nil {
		// launchd 明确报告 job 不存在时已经达到停止目标；其他探测错误不得伪装成幂等成功。
		if isMacOSLaunchdJobNotFound(target, err) {
			entry.Info("macOS Agent launchd job 已不存在")
			return false, nil
		}
		entry.WithErr(err).Error("检测 macOS Agent launchd job 失败")
		return false, err
	}
	entry.Info("macOS Agent launchd job 已加载")
	return true, nil
}

func isMacOSLaunchdJobNotFound(target string, err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "could not find service") || strings.Contains(message, "service not found") {
		return true
	}
	// system daemon Host 可能没有 GUI domain；仅用户域探测可把该精确错误视为 job 不存在。
	return strings.HasPrefix(target, "gui/") && strings.Contains(message, "could not find domain for user gui")
}

func detectMacOSUninstallMode(ctx context.Context, remote Remote, operationLog *logger.Log, removeData bool) (installMode, error) {
	home, uid, err := macOSUserContext(ctx, remote, operationLog)
	if err != nil {
		return "", err
	}
	userPaths := macOSUserAgentPaths(home)
	systemLoaded, err := macOSLaunchdJobLoaded(ctx, remote, operationLog, "detect_install_mode", "launch_daemon", "system/dev.superdev.agent")
	if err != nil {
		return "", err
	}
	userLoaded, err := macOSLaunchdJobLoaded(ctx, remote, operationLog, "detect_install_mode", "user_launch_agent", "gui/"+uid+"/dev.superdev.agent")
	if err != nil {
		return "", err
	}
	systemPlist, err := inspectMacOSPlistOwnership(
		ctx, remote, operationLog, "detect_install_mode", "launch_daemon",
		"/Library/LaunchDaemons/dev.superdev.agent.plist", "/usr/local/bin/superdev-agent", true, systemLoaded,
	)
	if err != nil {
		return "", err
	}
	userPlist, err := inspectMacOSPlistOwnership(
		ctx, remote, operationLog, "detect_install_mode", "user_launch_agent",
		userPaths.plist, userPaths.binary, false, userLoaded,
	)
	if err != nil {
		return "", err
	}
	output, err := runObservedRemoteOperation(ctx, remote, operationLog, "detect_install_mode", remoteOperation{
		Action:   "detect",
		Resource: "install_layout",
		Command:  macOSUninstallModeProbe(removeData),
	})
	if err != nil {
		return "", err
	}
	systemPaths := false
	userPathsPresent := false
	switch trimOutput(output) {
	case string(installModeSystem):
		systemPaths = true
	case string(installModeUserLaunchAgent):
		userPathsPresent = true
	case macOSLayoutAmbiguousMarker:
		systemPaths = true
		userPathsPresent = true
	case macOSLayoutAbsentMarker:
	default:
		return "", fmt.Errorf("unsupported macOS agent install evidence %q", trimOutput(output))
	}
	systemPresent := systemLoaded || systemPlist || systemPaths
	userPresent := userLoaded || userPlist || userPathsPresent
	// 两个布局都含 Agent 证据时不能安全推断 Controller 配置指向哪一个，必须在任何 mutation 前拒绝。
	if systemPresent && userPresent {
		return "", fmt.Errorf("ambiguous macOS Agent install: system LaunchDaemon and user LaunchAgent both exist")
	}
	if systemPresent {
		return installModeSystem, nil
	}
	if userPresent {
		return installModeUserLaunchAgent, nil
	}
	// 两侧均无资源时选择用户布局只为完成幂等路径清理；所有命令仍受 canonical 路径约束。
	return installModeUserLaunchAgent, nil
}

func installMacOSUserLaunchAgent(ctx context.Context, remote Remote, remoteTmp string, opts ServiceOptions) error {
	operationLog := logger.GetLogger().WithEntryName("AgentInstaller").WithField("stage", "install_user_launchd")
	home, uid, err := macOSUserContext(ctx, remote, operationLog)
	if err != nil {
		return stageErr("install_user_launchd", err)
	}
	paths := macOSUserAgentPaths(home)
	plist := MacOSUserLaunchAgentPlist(opts, paths.binary, paths.dataDir, paths.stdoutLog, paths.stderrLog)
	commands := []string{
		"mkdir -p " + shellQuote(paths.binDir) + " " + shellQuote(paths.dataDir) + " " + shellQuote(paths.launchAgentsDir) + " " + shellQuote(paths.logsDir),
		// 同 system 布局：先停旧 agent 再删状态，否则它会把 provisioned 写回来。
		stopServiceBeforeResetCommand("launchctl bootout gui/"+uid+" "+shellQuote(paths.plist)+" || true", opts),
		resetSecurityStateCommand("", path.Join(paths.dataDir, "security.json"), opts),
		"install -m 0755 " + remoteTmp + " " + shellQuote(paths.binary),
		"cat > " + shellQuote(paths.plist) + " <<'EOF'\n" + plist + "EOF",
		"chmod 0644 " + shellQuote(paths.plist),
		"launchctl bootout gui/" + uid + " " + shellQuote(paths.plist) + " || true",
		"launchctl bootstrap gui/" + uid + " " + shellQuote(paths.plist),
		"launchctl kickstart -k gui/" + uid + "/dev.superdev.agent",
	}
	for _, cmd := range commands {
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if _, err := remote.Run(ctx, cmd); err != nil {
			return stageErr("install_user_launchd", err)
		}
	}
	return nil
}

func restartMacOSUserLaunchAgent(ctx context.Context, remote Remote) error {
	operationLog := logger.GetLogger().WithEntryName("AgentInstaller").WithField("stage", "restart_user_launchd")
	_, uid, err := macOSUserContext(ctx, remote, operationLog)
	if err != nil {
		return stageErr("restart_user_launchd", err)
	}
	if _, err := remote.Run(ctx, "launchctl kickstart -k gui/"+uid+"/dev.superdev.agent"); err != nil {
		return stageErr("restart_user_launchd", err)
	}
	return nil
}

func uninstallMacOSUserLaunchAgent(ctx context.Context, remote Remote, operationLog *logger.Log, removeData bool) error {
	home, uid, err := macOSUserContext(ctx, remote, operationLog)
	if err != nil {
		return stageErr("uninstall_user_launchd", err)
	}
	paths := macOSUserAgentPaths(home)
	if err := stopMacOSLaunchdJob(
		ctx,
		remote,
		operationLog,
		"uninstall_user_launchd",
		"user_launch_agent",
		"gui/"+uid+"/dev.superdev.agent",
		paths.plist,
		paths.binary,
		false,
		"launchctl bootout gui/"+uid+" "+shellQuote(paths.plist),
	); err != nil {
		return stageErr("uninstall_user_launchd", err)
	}
	operations := []remoteOperation{
		{Action: "delete", Resource: "user_launch_agent_and_binary", Command: "rm -f " + shellQuote(paths.plist) + " " + shellQuote(paths.binary)},
	}
	if removeData {
		// 用户模式日志位于 ~/Library/Logs，不在主数据根目录内，purge 需同时清理两者。
		operations = append(operations,
			remoteOperation{Action: "purge", Resource: "user_data", Command: "rm -rf " + shellQuote(paths.rootDir)},
			remoteOperation{Action: "purge", Resource: "user_logs", Command: "rm -f " + shellQuote(paths.stdoutLog) + " " + shellQuote(paths.stderrLog)},
		)
	}
	if err := runObservedRemoteOperations(ctx, remote, operationLog, "uninstall_user_launchd", operations); err != nil {
		return stageErr("uninstall_user_launchd", err)
	}
	return nil
}

func macOSUserContext(ctx context.Context, remote Remote, operationLog *logger.Log) (string, string, error) {
	home, err := runObservedRemoteOperation(ctx, remote, operationLog, "detect_user_context", remoteOperation{
		Action: "detect", Resource: "user_home", Command: "printf %s \"$HOME\"",
	})
	if err != nil {
		return "", "", err
	}
	uid, err := runObservedRemoteOperation(ctx, remote, operationLog, "detect_user_context", remoteOperation{
		Action: "detect", Resource: "user_id", Command: "id -u",
	})
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
	return defaultVerifyAttempts
}

func (i *Installer) verifyDelay() time.Duration {
	if i.opts.VerifyDelay > 0 {
		return i.opts.VerifyDelay
	}
	return defaultVerifyDelay
}

func verifyAgentReady(ctx context.Context, remote Remote, port int, attempts int, delay time.Duration) error {
	cmd := fmt.Sprintf("curl -fsS http://127.0.0.1:%d/api/security/health >/dev/null", port)
	var lastErr error
	log.Printf("[installer] verifying agent readiness port=%d attempts=%d delay=%s", port, attempts, delay)
	for attempt := 1; attempt <= attempts; attempt++ {
		if _, err := remote.Run(ctx, cmd); err != nil {
			lastErr = err
		} else {
			log.Printf("[installer] agent readiness verified port=%d attempt=%d", port, attempt)
			return nil
		}
		if attempt == 1 || attempt%10 == 0 {
			log.Printf("[installer] agent readiness still pending port=%d attempt=%d/%d err=%v", port, attempt, attempts, lastErr)
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
	log.Printf("[installer] agent readiness verification failed port=%d attempts=%d err=%v", port, attempts, lastErr)
	return fmt.Errorf("agent not ready after %d attempts with %s delay: %w", attempts, delay, lastErr)
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
