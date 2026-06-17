//go:build !windows

// signal_unix.go 提供 Unix 下给运行中进程发信号的默认实现。
//
// 职责：
//   - 将调试 attach 需要的信号名映射为 syscall.Signal
//   - 调用 syscall.Kill 唤醒或中断运行中进程
//
// 边界：
//   - 不决定何时发信号，调用策略仍在 Manager 中
//   - 不处理 Windows，Windows 无 POSIX signal 语义
package codedebug

import (
	"strings"
	"syscall"
)

// signalProcessOS 给 Unix 进程发送指定信号。
func signalProcessOS(pid int, signal string) error {
	return syscall.Kill(pid, signalToSyscall(signal))
}

// signalToSyscall 将外部信号名归一化为 Unix syscall.Signal。
func signalToSyscall(signal string) syscall.Signal {
	switch strings.TrimSpace(strings.ToUpper(signal)) {
	case "SIGUSR1", "USR1":
		return syscall.SIGUSR1
	case "SIGTERM", "TERM":
		return syscall.SIGTERM
	case "SIGINT", "INT":
		return syscall.SIGINT
	default:
		return syscall.SIGUSR1
	}
}
