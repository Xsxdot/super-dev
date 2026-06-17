//go:build !windows

// pidresolve_unix_test.go 验证 Unix ps 进程组枚举 smoke 行为。
//
// 职责：
//   - 启动独立进程组并确认 listProcessGroupOS 能枚举到它
//   - 保护 Unix ps 输出解析契约
//
// 边界：
//   - 不在 Windows 编译或运行
//   - 不验证 debuggee 选择策略，选择策略由 pidresolve_test.go 覆盖
package codedebug

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestListProcessGroupOSIncludesStartedProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	if out, err := exec.Command("ps", "-axo", "pid=,pgid=,comm=").CombinedOutput(); err != nil {
		t.Skipf("ps process enumeration unavailable in this sandbox: %v: %s", err, string(out))
	} else if strings.TrimSpace(string(out)) == "" {
		t.Skip("ps process enumeration returned no process rows")
	}

	require.Eventually(t, func() bool {
		for _, p := range listProcessGroupOS(cmd.Process.Pid) {
			if p.pid == cmd.Process.Pid {
				return true
			}
		}
		return false
	}, time.Second, 20*time.Millisecond)
}
