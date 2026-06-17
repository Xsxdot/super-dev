//go:build !windows

// Package process verifies manager reconciliation against OS process groups.
//
// 职责：
//   - 覆盖进程组已死但 Manager 内存仍 running 的漂移纠正
//   - 覆盖后台子进程仍 alive 时不能被误清理
//
// 边界：
//   - 不触碰 pidStore，持久化清理由 API 层测试覆盖
package process

import (
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestManager_ReconcileDetectsDeadProcess(t *testing.T) {
	var logsMu sync.Mutex
	var logs []model.LogEntry
	mgr := NewManager(func(e model.LogEntry) {
		logsMu.Lock()
		logs = append(logs, e)
		logsMu.Unlock()
	})

	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	pgid := cmd.Process.Pid
	mgr.runners["dep-y"] = &Runner{
		cmd:        cmd,
		running:    true,
		group:      &groupRef{pgid: pgid},
		stderrTail: newStderrRing(100),
	}
	mgr.status["dep-y"] = model.StatusRunning

	require.NoError(t, syscall.Kill(-pgid, syscall.SIGKILL))
	_ = cmd.Wait()

	var res ReconcileResult
	require.Eventually(t, func() bool {
		res = mgr.Reconcile("dep-y")
		return res.Corrected
	}, 5*time.Second, 20*time.Millisecond)

	assert.False(t, mgr.IsActive("dep-y"))
	assert.Equal(t, "dep-y", res.ID)
	assert.Equal(t, pgid, res.PGID)
	assert.Equal(t, model.StatusFailed, res.Status)

	logsMu.Lock()
	defer logsMu.Unlock()
	var joined strings.Builder
	for _, e := range logs {
		joined.WriteString(e.Message)
		joined.WriteByte('\n')
	}
	assert.Contains(t, joined.String(), "对账检测到进程组已退出")
}

func TestManager_ReconcileKeepsBackgroundChildRunning(t *testing.T) {
	mgr := NewManager(func(e model.LogEntry) {})

	require.NoError(t, mgr.StartProcess("dep-bg", ProcessSpec{
		Command: "sleep 30 >/dev/null 2>&1 &",
		WorkDir: t.TempDir(),
	}))
	require.Eventually(t, func() bool {
		res := mgr.Reconcile("dep-bg")
		return !res.Corrected && mgr.Status("dep-bg") == model.StatusRunning
	}, 5*time.Second, 20*time.Millisecond)

	assert.True(t, mgr.IsActive("dep-bg"))
	mgr.Stop("dep-bg")
}

func TestManager_ReconcileAllReturnsOnlyCorrectedResults(t *testing.T) {
	mgr := NewManager(func(e model.LogEntry) {})

	dead := exec.Command("sleep", "30")
	dead.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, dead.Start())
	deadPGID := dead.Process.Pid
	mgr.runners["dep-dead"] = &Runner{
		cmd:        dead,
		running:    true,
		group:      &groupRef{pgid: deadPGID},
		stderrTail: newStderrRing(100),
	}
	mgr.status["dep-dead"] = model.StatusRunning
	require.NoError(t, syscall.Kill(-deadPGID, syscall.SIGKILL))
	_ = dead.Wait()

	require.NoError(t, mgr.StartProcess("dep-live", ProcessSpec{
		Command: "sleep 30",
		WorkDir: t.TempDir(),
	}))
	t.Cleanup(func() { mgr.Stop("dep-live") })

	results := mgr.ReconcileAll()
	require.Len(t, results, 1)
	assert.Equal(t, "dep-dead", results[0].ID)
	assert.Equal(t, model.StatusFailed, results[0].Status)
}
