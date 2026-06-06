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

// LinuxSystemdUnit 生成 Linux systemd unit 内容。
//
// 参数：
//   - port: 远端 agent 监听端口
//
// 返回：
//   - 可写入 /etc/systemd/system/superdev-agent.service 的 unit 文本
func LinuxSystemdUnit(port int) string {
	return fmt.Sprintf(`[Unit]
Description=SuperDev Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/bin/superdev-agent --addr 127.0.0.1:%d --data /var/lib/superdev-agent
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
`, port)
}

// MacOSLaunchDaemonPlist 生成 macOS LaunchDaemon plist 内容。
//
// 参数：
//   - port: 远端 agent 监听端口
//
// 返回：
//   - 可写入 /Library/LaunchDaemons/dev.superdev.agent.plist 的 plist 文本
func MacOSLaunchDaemonPlist(port int) string {
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
    <string>--addr</string>
    <string>127.0.0.1:%d</string>
    <string>--data</string>
    <string>/Library/Application Support/SuperDev/Agent</string>
  </array>
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
`, port)
}

// MacOSUserLaunchAgentPlist 生成 macOS 用户级 LaunchAgent plist 内容。
//
// 参数：
//   - port: 远端 agent 监听端口
//   - binaryPath: 用户目录下的 agent 二进制绝对路径
//   - dataDir: 用户目录下的 agent 数据目录绝对路径
//   - stdoutPath: 标准输出日志绝对路径
//   - stderrPath: 标准错误日志绝对路径
//
// 返回：
//   - 可写入 ~/Library/LaunchAgents/dev.superdev.agent.plist 的 plist 文本
func MacOSUserLaunchAgentPlist(port int, binaryPath string, dataDir string, stdoutPath string, stderrPath string) string {
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
    <string>--addr</string>
    <string>127.0.0.1:%d</string>
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
`, plistEscape(binaryPath), port, plistEscape(dataDir), plistEscape(stdoutPath), plistEscape(stderrPath))
}

func plistEscape(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
