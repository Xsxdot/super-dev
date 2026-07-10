//go:build !windows

// procgroup_unix_test.go 验证 adapter 进程组的建立与整组回收。
package codedebug

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDefaultAdapterLaunchKillsProcessGroup 验证 adapter 自成进程组，且 Close
// 会杀掉整组——dlv dap 会派生 debugserver 与（launch 模式下的）debuggee，
// 只杀 dlv 主进程会留下孤儿三件套（2026-07-07 实测残留 24 小时）。
func TestDefaultAdapterLaunchKillsProcessGroup(t *testing.T) {
	proc, err := defaultAdapterLaunch(context.Background(), AdapterCommand{
		Name: "sh",
		Args: []string{"-c", "sleep 60 | sleep 60"},
	})
	require.NoError(t, err)

	// adapter 必须自成进程组（组 id 即主进程 pid），否则无法整组回收。
	require.NoError(t, syscall.Kill(-proc.PID, 0), "adapter must run in its own process group")

	require.NoError(t, proc.Close())
	require.Eventually(t, func() bool {
		err := syscall.Kill(-proc.PID, 0)
		return errors.Is(err, syscall.ESRCH)
	}, 3*time.Second, 50*time.Millisecond, "whole adapter process group should be killed on Close")
}
