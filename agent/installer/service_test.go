// installer 服务模板测试。
//
// 职责：
//   - 验证 Linux systemd unit 中的启动命令与自启配置
//   - 验证 macOS LaunchDaemon plist 中的启动命令与保活配置
//
// 边界：
//   - 不执行 systemctl 或 launchctl
//   - 不验证远端文件权限，由安装编排测试覆盖命令顺序
package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLinuxSystemdUnit(t *testing.T) {
	unit := LinuxSystemdUnit(57019)
	assert.Contains(t, unit, "Description=SuperDev Agent")
	assert.Contains(t, unit, "ExecStart=/usr/local/bin/superdev-agent --addr 127.0.0.1:57019 --data /var/lib/superdev-agent")
	assert.Contains(t, unit, "Restart=always")
	assert.Contains(t, unit, "WantedBy=multi-user.target")
}

func TestMacOSLaunchDaemonPlist(t *testing.T) {
	plist := MacOSLaunchDaemonPlist(57020)
	assert.Contains(t, plist, "<string>dev.superdev.agent</string>")
	assert.Contains(t, plist, "<string>/usr/local/bin/superdev-agent</string>")
	assert.Contains(t, plist, "<string>127.0.0.1:57020</string>")
	assert.Contains(t, plist, "<string>/Library/Application Support/SuperDev/Agent</string>")
	assert.Contains(t, plist, "<key>KeepAlive</key>")
}
