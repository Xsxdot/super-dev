package api

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/xsxdot/super-dev/agent/logbuf"
	"github.com/xsxdot/super-dev/agent/model"
	agentprocess "github.com/xsxdot/super-dev/agent/process"
)

// TestCloseRunsOnce 验证 App.Close 多次调用时清理逻辑只执行一次。
func TestCloseRunsOnce(t *testing.T) {
	var count int
	a := &App{}
	a.closeFn = func() { count++ }

	a.Close()
	a.Close()

	if count != 1 {
		t.Fatalf("期望 Close 内部清理只执行 1 次，实际执行 %d 次", count)
	}
}

// TestCloseStopsProjectManagers 验证 App.Close 会停止项目 deployment manager 中的进程。
func TestCloseStopsProjectManagers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signal semantics differ on windows")
	}

	mgr := agentprocess.NewManager(func(model.LogEntry) {})
	t.Cleanup(mgr.StopAll)

	const depID = "dep-close-project-manager"
	if err := mgr.StartProcess(depID, agentprocess.ProcessSpec{Argv: []string{"sh", "-c", "sleep 30"}}); err != nil {
		t.Fatalf("启动测试进程失败: %v", err)
	}
	pid := mgr.PID(depID)
	if pid == 0 {
		t.Fatal("期望测试进程已启动并有 pid")
	}

	a := &App{
		buf:      logbuf.New(nil, 10, "", nil),
		managers: map[string]*agentprocess.Manager{"project-1": mgr},
	}
	a.Close()

	if processAlive(pid) {
		t.Fatalf("期望 Close 停止 project manager 中的进程 pid=%d", pid)
	}
}

func processAlive(pid int) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return false
		}
		if time.Now().After(deadline) {
			return err == nil || errors.Is(err, syscall.EPERM)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
