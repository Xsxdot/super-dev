//go:build windows

// processalive_windows.go 提供 Windows 下进程存活探测的默认实现。
//
// 职责：
//   - 用 OpenProcess（经 os.FindProcess）探测 pid 对应进程是否存在
//
// 边界：
//   - 不判断进程是谁（pid 复用误判由调用方的身份比较兜底）
//   - 不处理 Unix
package codedebug

import "os"

// processAliveOS 探测 Windows 进程是否存活。
//
// Windows 的 os.FindProcess 内部 OpenProcess，句柄打不开即视为进程不存在。
func processAliveOS(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = proc.Release()
	return true
}
