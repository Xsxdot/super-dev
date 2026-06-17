//go:build windows

// exitdetails_windows.go 提供 Windows ProcessState 的退出细节适配。
//
// 职责：
//   - 返回 Windows 进程退出码
//   - 明确 Windows 无 POSIX 信号终止语义
//
// 边界：
//   - 不模拟 Unix signal 字段
//   - 不改变 Runner 的通用 ExitInfo 组装逻辑
package process

import "os"

// signaledInfo 在 Windows 下不标记 signaled，仅返回进程退出码。
func signaledInfo(state *os.ProcessState) (signaled bool, signal string, exitCode int) {
	return false, "", state.ExitCode()
}
