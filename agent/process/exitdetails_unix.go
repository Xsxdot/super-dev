//go:build !windows

// exitdetails_unix.go 从 Unix ProcessState 中提取信号终止细节。
//
// 职责：
//   - 识别 SIGKILL/SIGTERM 等信号导致的退出
//   - 保留 Unix wait status 的真实退出码语义
//
// 边界：
//   - 不构造 ExitInfo 完整结构，仅返回平台相关的信号字段
//   - 不处理 Windows 退出码，Windows 逻辑见 exitdetails_windows.go
package process

import (
	"os"
	"syscall"
)

// signaledInfo 从 Unix ProcessState 提取信号终止信息。
func signaledInfo(state *os.ProcessState) (signaled bool, signal string, exitCode int) {
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return false, "", state.ExitCode()
	}
	if ws.Signaled() {
		return true, ws.Signal().String(), state.ExitCode()
	}
	return false, "", ws.ExitStatus()
}
