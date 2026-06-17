//go:build !windows

// pid_store_unix_test.go 验证 pid store 的 Unix 进程终止语义。
//
// 职责：
//   - 覆盖 KillAll 能终止持久化记录中的 Unix 进程
//   - 覆盖 KillAll 以进程组为边界终止后台子进程
//
// 边界：
//   - 不测试 JSON 持久化读写，跨平台存储行为由 pid_store_test.go 覆盖
//   - 不覆盖 Windows Job Object，对应语义由 jobobject_windows_test.go 覆盖
package process_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/process"
)

func TestPIDStoreKillAll(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))

	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())
	pid := cmd.Process.Pid

	ps.Set("dep-sleep", pid)
	require.NoError(t, ps.Flush())

	ps.KillAll()

	_ = cmd.Wait()
	proc, err := os.FindProcess(pid)
	require.NoError(t, err)
	err = proc.Signal(syscall.Signal(0))
	assert.Error(t, err, "进程应已死亡")
}

func TestPIDStore_KillAllTargetsGroup(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))

	cmd := exec.Command("sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	pgid := cmd.Process.Pid

	ps.Set("dep-group", pgid)
	require.NoError(t, ps.Flush())

	ps.KillAll()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process did not exit after KillAll")
	}
	require.Eventually(t, func() bool {
		return syscall.Kill(-pgid, 0) != nil
	}, 5*time.Second, 20*time.Millisecond, "process group should be dead after KillAll")
}
