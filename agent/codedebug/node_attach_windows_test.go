//go:build windows

// node_attach_windows_test.go 验证 Windows Node attach 的 prearm 行为。
//
// 职责：
//   - 确认 Windows Node attach 不发送 POSIX signal
//   - 确认 attach 端口来自启动 argv 中的 --inspect
//
// 边界：
//   - 不启动真实 Node 或 js-debug adapter
//   - 不覆盖 Unix SIGUSR1 路径
package codedebug

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
	"github.com/xsxdot/super-dev/agent/model"
)

// TestNodeAttachWindowsUsesPrearm 验证 Windows 下 Node attach 不调用 signalProcess。
func TestNodeAttachWindowsUsesPrearm(t *testing.T) {
	signalCalled := false
	m := NewManager(ManagerOptions{
		RunningProcessArgv: func(deploymentID string) []string {
			return []string{"node", "--inspect=12345", "server.js"}
		},
		SignalProcess: func(pid int, sig string) error {
			signalCalled = true
			return nil
		},
	})
	req := readinessRequest{readiness: langruntime.ReadinessAttachPID}

	err := m.fillNodeAttach(&req, model.Deployment{ID: "dep-node"}, 101)

	require.NoError(t, err)
	require.False(t, signalCalled, "windows node attach must not send signals")
	require.Equal(t, 101, req.pid)
	require.Equal(t, langruntime.ReadinessPrearmListen, req.readiness)
	require.Equal(t, 12345, req.port)
}
