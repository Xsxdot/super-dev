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

func TestPIDStoreSetGet(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))

	ps.Set("dep-1", 12345)
	ps.Set("dep-2", 99999)
	require.NoError(t, ps.Flush())

	ps2 := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	pgids := ps2.LoadAll()
	assert.Equal(t, 12345, pgids["dep-1"])
	assert.Equal(t, 99999, pgids["dep-2"])
}

func TestPIDStoreRemove(t *testing.T) {
	dir := t.TempDir()
	ps := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	ps.Set("dep-1", 12345)
	ps.Remove("dep-1")
	require.NoError(t, ps.Flush())

	ps2 := process.NewPIDStore(filepath.Join(dir, "pids.json"))
	pgids := ps2.LoadAll()
	assert.Empty(t, pgids)
}

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
	assert.Error(t, syscall.Kill(-pgid, 0), "process group should be dead after KillAll")
}
