//go:build windows

// signal_windows.go 明确 Windows 不支持 POSIX 信号发送。
//
// 职责：
//   - 为 Manager 的 signalProcessOS 默认 hook 提供 Windows 编译实现
//   - 返回带 pid/signal 上下文的 unsupported 错误
//
// 边界：
//   - 不模拟 SIGUSR1；Node 调试在 Windows 走 prearm --inspect
//   - Python/JVM 等 prearm-listen 语言本就不经 signal 路径
package codedebug

import "fmt"

// signalProcessOS 在 Windows 下返回不支持 POSIX signal 的错误。
func signalProcessOS(pid int, signal string) error {
	return fmt.Errorf("signal %q to pid %d: not supported on windows", signal, pid)
}
