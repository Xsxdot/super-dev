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
	unit := LinuxSystemdUnit(ServiceOptions{
		BindAddress:    "0.0.0.0",
		Port:           57019,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
		User:           "alice",
	})
	assert.Contains(t, unit, "Description=SuperDev Agent")
	assert.Contains(t, unit, "ExecStart=/usr/local/bin/superdev-agent --addr 0.0.0.0:57019 --require-auth --bootstrap-token bootstrap --data /var/lib/superdev-agent")
	assert.Contains(t, unit, "Restart=always")
	assert.Contains(t, unit, "WantedBy=multi-user.target")
	assert.Contains(t, unit, "Environment=HOME=/home/alice")
}

func TestLinuxSystemdUnitHomeFromRootUser(t *testing.T) {
	unit := LinuxSystemdUnit(ServiceOptions{User: "root"})
	assert.Contains(t, unit, "Environment=HOME=/root")
}

func TestLinuxSystemdUnitPrefersExplicitHomeDir(t *testing.T) {
	unit := LinuxSystemdUnit(ServiceOptions{User: "alice", HomeDir: "/srv/alice"})
	assert.Contains(t, unit, "Environment=HOME=/srv/alice")
	assert.NotContains(t, unit, "Environment=HOME=/home/alice")
}

func TestMacOSLaunchDaemonPlist(t *testing.T) {
	plist := MacOSLaunchDaemonPlist(ServiceOptions{
		BindAddress:    "100.64.0.8",
		Port:           57020,
		RequireAuth:    true,
		BootstrapToken: "bootstrap",
		User:           "alice",
	})
	assert.Contains(t, plist, "<string>dev.superdev.agent</string>")
	assert.Contains(t, plist, "<string>/usr/local/bin/superdev-agent</string>")
	assert.Contains(t, plist, "<string>100.64.0.8:57020</string>")
	assert.Contains(t, plist, "<string>--require-auth</string>")
	assert.Contains(t, plist, "<string>--bootstrap-token</string>")
	assert.Contains(t, plist, "<string>bootstrap</string>")
	assert.Contains(t, plist, "<string>/Library/Application Support/SuperDev/Agent</string>")
	assert.Contains(t, plist, "<key>KeepAlive</key>")
	assert.Contains(t, plist, "<key>EnvironmentVariables</key>")
	assert.Contains(t, plist, "<key>HOME</key>")
	assert.Contains(t, plist, "<string>/Users/alice</string>")
}

func TestMacOSLaunchDaemonPlistHomeFromRootUser(t *testing.T) {
	plist := MacOSLaunchDaemonPlist(ServiceOptions{User: "root"})
	assert.Contains(t, plist, "<key>HOME</key>")
	assert.Contains(t, plist, "<string>/var/root</string>")
}

func TestMacOSUserLaunchAgentPlist(t *testing.T) {
	plist := MacOSUserLaunchAgentPlist(
		ServiceOptions{Port: 57020},
		"/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent",
		"/Users/sycm/Library/Application Support/SuperDev/Agent/data",
		"/Users/sycm/Library/Logs/superdev-agent.log",
		"/Users/sycm/Library/Logs/superdev-agent.err.log",
	)

	assert.Contains(t, plist, "<string>dev.superdev.agent</string>")
	assert.Contains(t, plist, "<string>/Users/sycm/Library/Application Support/SuperDev/Agent/bin/superdev-agent</string>")
	assert.Contains(t, plist, "<string>127.0.0.1:57020</string>")
	assert.Contains(t, plist, "<string>/Users/sycm/Library/Application Support/SuperDev/Agent/data</string>")
	assert.Contains(t, plist, "<string>/Users/sycm/Library/Logs/superdev-agent.log</string>")
	assert.NotContains(t, plist, "~")
	assert.Contains(t, plist, "<key>KeepAlive</key>")
}
