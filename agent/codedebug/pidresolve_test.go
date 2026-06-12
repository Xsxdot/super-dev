package codedebug

import (
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveGoDebuggeePIDDirectExecutable(t *testing.T) {
	// 命令是直接可执行（非 go run），debuggee 就是 deployment 主 PID 本身。
	pid, err := resolveGoDebuggeePID(goDebuggeeHints{
		command: "./server", mainPID: 4321, pgid: 4321,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{{pid: 4321, comm: "server"}}
		},
	})
	if err != nil || pid != 4321 {
		t.Fatalf("direct executable should use main pid: pid=%d err=%v", pid, err)
	}
}

func TestResolveGoDebuggeePIDGoRunChild(t *testing.T) {
	// go run：主 PID 是 go/sh，真实 debuggee 是进程组内的临时可执行子进程。
	pid, err := resolveGoDebuggeePID(goDebuggeeHints{
		command: "go run ./cmd/api", mainPID: 100, pgid: 100,
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{
				{pid: 100, comm: "go"},
				{pid: 105, comm: "api"}, // go run 编译出的真实进程
			}
		},
	})
	if err != nil || pid != 105 {
		t.Fatalf("go run should resolve to child debuggee: pid=%d err=%v", pid, err)
	}
}

func TestResolveGoDebuggeePIDGoRunNoChild(t *testing.T) {
	_, err := resolveGoDebuggeePID(goDebuggeeHints{
		command: "go run ./cmd/api", mainPID: 100, pgid: 100,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{{pid: 100, comm: "go"}}
		},
	})
	if err == nil {
		t.Fatal("go run with no compiled child should error (not yet started)")
	}
}

func TestListProcessGroupOSIncludesStartedProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	if out, err := exec.Command("ps", "-axo", "pid=,pgid=,comm=").CombinedOutput(); err != nil {
		t.Skipf("ps process enumeration unavailable in this sandbox: %v: %s", err, string(out))
	} else if strings.TrimSpace(string(out)) == "" {
		t.Skip("ps process enumeration returned no process rows")
	}

	require.Eventually(t, func() bool {
		for _, p := range listProcessGroupOS(cmd.Process.Pid) {
			if p.pid == cmd.Process.Pid {
				return true
			}
		}
		return false
	}, time.Second, 20*time.Millisecond)
}
