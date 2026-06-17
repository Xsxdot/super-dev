//go:build !windows

// platform_unix.go 提供 darwin/linux 共用的进程组原语：基于 Setpgid 与信号。
//
// 职责：
//   - 配置子进程进入独立进程组
//   - 按进程组终止和探活
//   - 提供 Unix shell 命令包装形式
//
// 边界：
//   - 仅封装本地进程组语义，不感知服务、deployment 或远端执行
//   - 不捕获 stdout/stderr，输出处理仍由 Runner 负责
package process

import (
	"os/exec"
	"syscall"
)

// groupRef 是 Unix 下对进程组的引用。
type groupRef struct {
	pgid int
}

// configureSysProcAttr 让子进程成为独立进程组组长。
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// trackProcessGroup 在进程启动后返回其进程组引用。
func trackProcessGroup(cmd *exec.Cmd) (*groupRef, error) {
	return &groupRef{pgid: cmd.Process.Pid}, nil
}

// shellCommand 返回 Unix 下包裹任意命令字符串的 sh -c 形式。
func shellCommand(command string) (string, []string) {
	return "sh", []string{"-c", command}
}

// kill 终止整个 Unix 进程组。
func (g *groupRef) kill() error {
	return syscall.Kill(-g.pgid, syscall.SIGKILL)
}

// alive 判断 Unix 进程组是否仍有可见进程。
func (g *groupRef) alive() bool {
	err := syscall.Kill(-g.pgid, 0)
	// EPERM 说明进程存在但当前用户无权发送信号，应视为仍存活。
	return err == nil || err == syscall.EPERM
}

// id 返回 Unix 进程组 ID。
func (g *groupRef) id() int {
	return g.pgid
}
