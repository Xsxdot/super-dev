//go:build !windows

// processalive_unix.go 提供 Unix 下进程存活探测的默认实现。
//
// 职责：
//   - 用 signal 0 探测 pid 对应进程是否存在（不发送任何真实信号）
//
// 边界：
//   - 不判断进程是谁（pid 复用误判由调用方的身份比较兜底）
//   - 不处理 Windows
package codedebug

import (
	"errors"
	"syscall"
)

// processAliveOS 探测 Unix 进程是否存活。
//
// kill(pid, 0) 不投递信号只做存在性与权限检查：EPERM 表示进程存在但无权限，
// 也算存活；ESRCH 表示不存在。
func processAliveOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
