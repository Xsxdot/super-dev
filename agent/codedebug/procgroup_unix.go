//go:build !windows

// procgroup_unix.go 提供 Unix 下 adapter 进程组的建立与整组回收。
//
// 职责：
//   - 让 adapter（如 dlv dap）自成进程组
//   - Close 时按进程组整组 SIGKILL，覆盖 adapter 派生的 debugserver/debuggee
//
// 边界：
//   - 不决定何时回收，生命周期仍由 Manager 掌控
//   - 不处理 Windows（无 POSIX 进程组语义）
package codedebug

import (
	"os/exec"
	"syscall"
)

// setAdapterProcessGroup 让 adapter 进程自成进程组（pgid = 主进程 pid）。
//
// dlv dap 在 launch 模式会派生 debugserver 与重新编译的 debuggee；attach 模式
// 会派生 debugserver。只杀 dlv 主进程会把这些子进程留成孤儿（2026-07-07 实测
// dlv+debugserver+__debug_bin 三件套残留 24 小时），必须能整组回收。
func setAdapterProcessGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killAdapterProcessTree 杀死 adapter 及其派生的全部子进程。
//
// 先按进程组整组 SIGKILL；组已不存在时退回按 pid 杀主进程（幂等，进程已死
// 返回的 ESRCH 视为成功）。
func killAdapterProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err == nil || err == syscall.ESRCH {
		return nil
	}
	// 组杀失败（如进程未成组的兜底路径）：按 pid 杀主进程
	if killErr := syscall.Kill(pid, syscall.SIGKILL); killErr == nil || killErr == syscall.ESRCH {
		return nil
	}
	return err
}
