// installer 服务模板生成。
//
// 职责：
//   - 生成 Linux systemd service 内容
//   - 生成 macOS LaunchDaemon plist 内容
//
// 边界：
//   - 不写入远端服务文件
//   - 不执行 systemctl 或 launchctl
package installer

import (
	"fmt"
	"strings"
)

// ServiceOptions 描述 agent 服务进程的监听和安全启动参数。
type ServiceOptions struct {
	BindAddress    string
	Port           int
	RequireAuth    bool
	BootstrapToken string
	TLSCertFile    string
	TLSKeyFile     string
	// User 是安装时的目标用户（通常为 Host.SSHUser），用于推导服务 HOME。
	User string
	// HomeDir 是写入服务环境的 HOME。非空时优先于按 User 推导的惯例路径，
	// 避免把 /root 写死进模板。
	HomeDir string
}

func (o ServiceOptions) listenAddr() string {
	bind := strings.TrimSpace(o.BindAddress)
	if bind == "" {
		bind = "127.0.0.1"
	}
	port := o.Port
	if port <= 0 {
		port = 57017
	}
	return fmt.Sprintf("%s:%d", bind, port)
}

func (o ServiceOptions) commandArgs() []string {
	args := []string{"--addr", o.listenAddr()}
	if o.RequireAuth {
		args = append(args, "--require-auth")
	}
	if strings.TrimSpace(o.BootstrapToken) != "" {
		args = append(args, "--bootstrap-token", o.BootstrapToken)
	}
	if strings.TrimSpace(o.TLSCertFile) != "" && strings.TrimSpace(o.TLSKeyFile) != "" {
		args = append(args, "--tls-cert-file", o.TLSCertFile, "--tls-key-file", o.TLSKeyFile)
	}
	return args
}

// resolvedHomeDir 按平台从目标用户推导服务进程 HOME。
//
// HomeDir 非空时原样使用；否则 root/空用户在 Linux 为 /root、在 Darwin 为
// /var/root，其它用户分别为 /home/<user> 与 /Users/<user>。
func (o ServiceOptions) resolvedHomeDir(platformOS string) string {
	if home := strings.TrimSpace(o.HomeDir); home != "" {
		return home
	}
	user := strings.TrimSpace(o.User)
	switch platformOS {
	case "darwin":
		if user == "" || user == "root" {
			return "/var/root"
		}
		return "/Users/" + user
	default:
		if user == "" || user == "root" {
			return "/root"
		}
		return "/home/" + user
	}
}

// LinuxSystemdUnit 生成 Linux systemd unit 内容。
//
// 参数：
//   - opts: 服务监听与安全启动参数
//
// 返回：
//   - 可写入 /etc/systemd/system/superdev-agent.service 的 unit 文本
func LinuxSystemdUnit(opts ServiceOptions) string {
	return fmt.Sprintf(`[Unit]
Description=SuperDev Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
# systemd 默认不给服务进程设 HOME；agent 的 home 解析依赖 HOME，
# SuperDev 安装远端 agent 的唯一方式就是这份 unit，必须显式注入。
Environment=HOME=%s
ExecStart=/usr/local/bin/superdev-agent %s --data /var/lib/superdev-agent
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, opts.resolvedHomeDir("linux"), strings.Join(opts.commandArgs(), " "))
}

// MacOSLaunchDaemonPlist 生成 macOS LaunchDaemon plist 内容。
//
// 参数：
//   - opts: 服务监听与安全启动参数
//
// 返回：
//   - 可写入 /Library/LaunchDaemons/dev.superdev.agent.plist 的 plist 文本
func MacOSLaunchDaemonPlist(opts ServiceOptions) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.superdev.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/local/bin/superdev-agent</string>
%s
    <string>--data</string>
    <string>/Library/Application Support/SuperDev/Agent</string>
  </array>
  <!-- launchd daemon 默认不注入 HOME；agent 的 home 解析依赖 HOME，
       SuperDev 安装远端 agent 在 macOS 上走 LaunchDaemon，必须显式写入。 -->
  <key>EnvironmentVariables</key>
  <dict>
    <key>HOME</key>
    <string>%s</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>/var/log/superdev-agent.log</string>
  <key>StandardErrorPath</key>
  <string>/var/log/superdev-agent.err.log</string>
</dict>
</plist>
`, plistArgumentLines(opts.commandArgs()), plistEscape(opts.resolvedHomeDir("darwin")))
}

// MacOSUserLaunchAgentPlist 生成 macOS 用户级 LaunchAgent plist 内容。
//
// 参数：
//   - opts: 服务监听与安全启动参数
//   - binaryPath: 用户目录下的 agent 二进制绝对路径
//   - dataDir: 用户目录下的 agent 数据目录绝对路径
//   - stdoutPath: 标准输出日志绝对路径
//   - stderrPath: 标准错误日志绝对路径
//
// 返回：
//   - 可写入 ~/Library/LaunchAgents/dev.superdev.agent.plist 的 plist 文本
func MacOSUserLaunchAgentPlist(opts ServiceOptions, binaryPath string, dataDir string, stdoutPath string, stderrPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>dev.superdev.agent</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
%s
    <string>--data</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>%s</string>
  <key>StandardErrorPath</key>
  <string>%s</string>
</dict>
</plist>
`, plistEscape(binaryPath), plistArgumentLines(opts.commandArgs()), plistEscape(dataDir), plistEscape(stdoutPath), plistEscape(stderrPath))
}

func plistArgumentLines(args []string) string {
	lines := make([]string, 0, len(args))
	for _, arg := range args {
		lines = append(lines, "    <string>"+plistEscape(arg)+"</string>")
	}
	return strings.Join(lines, "\n")
}

func plistEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
