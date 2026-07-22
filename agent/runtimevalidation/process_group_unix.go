//go:build !windows

// process_group_unix.go 使用独立 process group 回收 Darwin/Linux 的 runner-owned 子进程树。
//
// 职责：设置 Setpgid，并向整个 group 发送 TERM/KILL。
// 边界：不承诺 runner 自身被 SIGKILL 后 group 自动退出。
package runtimevalidation

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

type unixProcessGroup struct {
	pgid int
}

func newProcessTreeController() (processTreeController, error) {
	return &unixProcessGroup{}, nil
}

func (g *unixProcessGroup) Configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func (g *unixProcessGroup) Attach(process *os.Process) error {
	g.pgid = process.Pid
	return nil
}

func (g *unixProcessGroup) Terminate() error {
	if g.pgid <= 0 {
		return nil
	}
	return syscall.Kill(-g.pgid, syscall.SIGTERM)
}

func (g *unixProcessGroup) Kill() error {
	if g.pgid <= 0 {
		return nil
	}
	return syscall.Kill(-g.pgid, syscall.SIGKILL)
}

func (g *unixProcessGroup) Close() error { return nil }

func (g *unixProcessGroup) ID() string {
	if g.pgid <= 0 {
		return "pgid:unassigned"
	}
	return fmt.Sprintf("pgid:%s", strconv.Itoa(g.pgid))
}
