//go:build windows

// procgroup_windows.go 提供 Windows 下 adapter 进程回收的实现。
//
// 职责：
//   - Close 时用 taskkill /T 终止 adapter 进程树
//
// 边界：
//   - Windows 无 POSIX 进程组语义，进程树终止依赖 taskkill；taskkill 不可用时
//     退回只杀主进程（子进程回收不保证，与旧行为一致）
package codedebug

import (
	"os"
	"os/exec"
	"strconv"
)

// setAdapterProcessGroup 在 Windows 下为 no-op（无 POSIX 进程组语义）。
func setAdapterProcessGroup(_ *exec.Cmd) {}

// killAdapterProcessTree 用 taskkill /T /F 终止 adapter 及其子进程树。
func killAdapterProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run(); err == nil {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	defer func() { _ = proc.Release() }()
	return proc.Kill()
}
