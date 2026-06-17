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

func TestResolveGoDebuggeePIDGoRunWithInlineEnvChild(t *testing.T) {
	// 带内联环境变量的 go run 仍应进入进程组解析；主 PID 通常是 sh，不是真实 debuggee。
	pid, err := resolveGoDebuggeePID(goDebuggeeHints{
		command: "ENABLE=true go run ./cmd/api", mainPID: 100, pgid: 100,
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{
				{pid: 100, comm: "sh"},
				{pid: 101, comm: "go"},
				{pid: 105, comm: "api"}, // go run 编译出的真实进程
			}
		},
	})
	if err != nil || pid != 105 {
		t.Fatalf("go run with inline env should resolve to child debuggee: pid=%d err=%v", pid, err)
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

func TestResolveNodeDebuggeePIDFindsChildUnderLauncher(t *testing.T) {
	// pnpm(父,pid=100) -> node(子,pid=101)，同一进程组。
	procs := []procInfo{
		{pid: 100, comm: "pnpm"},
		{pid: 101, comm: "node"},
	}
	pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID: 100, pgid: 100,
		listProcessGroup: func(int) []procInfo {
			return procs
		},
	})
	require.NoError(t, err)
	require.Equal(t, 101, pid)
}

func TestResolveNodeDebuggeePIDFindsWindowsNodeExe(t *testing.T) {
	// Windows 进程枚举返回 node.exe；解析逻辑应与 Unix 的 node 名称等价。
	pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID: 100, pgid: 100,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{
				{pid: 100, comm: "pnpm.exe"},
				{pid: 101, comm: "node.exe"},
			}
		},
	})
	require.NoError(t, err)
	require.Equal(t, 101, pid)
}

func TestResolveNodeDebuggeePIDMainIsNode(t *testing.T) {
	// 高层启动：主进程直接是 node。
	pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID:    200,
		pgid:       200,
		mainIsNode: true,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{{pid: 200, comm: "node"}}
		},
	})
	require.NoError(t, err)
	require.Equal(t, 200, pid)
}

func TestResolveNodeDebuggeePIDFallsBackToMainWhenProcessListUnavailable(t *testing.T) {
	// 高层 Node runtime 的主进程就是 node；沙箱里 ps 不可用时仍可安全 signal 主 PID。
	pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID:    210,
		pgid:       210,
		mainIsNode: true,
		listProcessGroup: func(int) []procInfo {
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 210, pid)
}

func TestResolveNodeDebuggeePIDSkipsLauncherNodeWhenMainIsNotNode(t *testing.T) {
	// pnpm/npm 逃生口的 launcher 自身也可能是 node；真正 debuggee 是同进程组里的子 node。
	pid, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID:    220,
		pgid:       220,
		mainIsNode: false,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{
				{pid: 220, comm: "node"},
				{pid: 221, comm: "node"},
			}
		},
	})
	require.NoError(t, err)
	require.Equal(t, 221, pid)
}

func TestResolveNodeDebuggeePIDNoNodeChild(t *testing.T) {
	_, err := resolveNodeDebuggeePID(nodeDebuggeeHints{
		mainPID: 300, pgid: 300,
		listProcessGroup: func(int) []procInfo {
			return []procInfo{{pid: 300, comm: "pnpm"}}
		},
	})
	require.Error(t, err)
}

func TestIsGoRunCommandSkipsOnlyValidInlineEnvFields(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "valid inline env", command: "ENABLE=true PORT=8080 /usr/local/bin/go run ./cmd/api", want: true},
		{name: "dash is not env key", command: "--enable=true go run ./cmd/api", want: false},
		{name: "slash is not env key", command: "config/path=true go run ./cmd/api", want: false},
		{name: "dot is not env key", command: "config.path=true go run ./cmd/api", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGoRunCommand(tt.command); got != tt.want {
				t.Fatalf("isGoRunCommand(%q)=%v, want %v", tt.command, got, tt.want)
			}
		})
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
