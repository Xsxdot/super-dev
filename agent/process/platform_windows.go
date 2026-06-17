//go:build windows

// platform_windows.go 用 Job Object + CREATE_NEW_PROCESS_GROUP 对等 Unix 进程组。
//
// 职责：
//   - 配置 Windows 子进程创建标志
//   - 将已启动进程纳入 Job Object 并按 Job 终止/探活
//   - 提供 Windows shell 命令包装形式
//
// 边界：
//   - 仅封装本地进程组语义，Job Object 句柄生命周期由 groupRef 持有
//   - 不负责远端 Windows 安装或服务注册
package process

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// groupRef 是 Windows 下对进程组的引用，底层是一个 Job Object。
type groupRef struct {
	job *jobObject
	pid int
}

// configureSysProcAttr 让子进程成为新进程组组长，便于整组控制。
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}

// trackProcessGroup 在进程启动后创建 Job Object 并把主进程纳入。
func trackProcessGroup(cmd *exec.Cmd) (*groupRef, error) {
	job, err := newJobObject()
	if err != nil {
		return nil, err
	}
	if err := job.assign(cmd.Process.Pid); err != nil {
		_ = job.Close()
		return nil, err
	}
	return &groupRef{job: job, pid: cmd.Process.Pid}, nil
}

// shellCommand 返回 Windows 下包裹任意命令字符串的 cmd /c 形式。
func shellCommand(command string) (string, []string) {
	return "cmd", []string{"/c", command}
}

// kill 终止 Job Object 内的整个进程树。
func (g *groupRef) kill() error {
	return g.job.terminate()
}

// alive 判断 Job Object 内是否仍有活动进程。
func (g *groupRef) alive() bool {
	return g.job.isAlive()
}

// id 返回 Windows 主进程 pid，用于日志与历史 pid store 兼容。
func (g *groupRef) id() int {
	return g.pid
}
