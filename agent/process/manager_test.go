package process_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/process"
)

func TestManagerStartStopDeployment(t *testing.T) {
	var entriesMu sync.Mutex
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		entriesMu.Lock()
		entries = append(entries, e)
		entriesMu.Unlock()
	})

	dep1 := model.Deployment{
		ID:       "dep-1",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  `echo "started"`,
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep1))
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.DeploymentStatus("dep-1"))

	dep2 := model.Deployment{
		ID:       "dep-2",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep2))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-2"))

	mgr.StopDeployment("dep-2")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.DeploymentStatus("dep-2"))

	// 所有日志条目的 DeploymentID 应正确归属
	entriesMu.Lock()
	entriesSnapshot := append([]model.LogEntry(nil), entries...)
	entriesMu.Unlock()
	for _, e := range entriesSnapshot {
		assert.NotEmpty(t, e.DeploymentID)
	}
}

func TestManagerStartDeploymentSpecUsesArgv(t *testing.T) {
	var entriesMu sync.Mutex
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		entriesMu.Lock()
		entries = append(entries, e)
		entriesMu.Unlock()
	})
	dep := model.Deployment{
		ID:       "dep-argv",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Runtime:  &model.RuntimeConfig{Type: model.RuntimeTypeLanguage},
	}

	require.NoError(t, mgr.StartDeploymentSpec(dep, process.ProcessSpec{Argv: []string{"echo", "manager argv"}}))

	require.Eventually(t, func() bool {
		entriesMu.Lock()
		defer entriesMu.Unlock()
		for _, entry := range entries {
			if entry.DeploymentID == "dep-argv" && entry.Message == "manager argv" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
}

// TestCommandRuntimeStructuredArgvHelper 是结构化 command runtime 测试启动的子进程入口。
//
// 边界：仅当测试显式注入 SUPERDEV_PROCESS_ARGV_HELPER 时输出探针文本，正常测试进程中为空操作。
func TestCommandRuntimeStructuredArgvHelper(t *testing.T) {
	if os.Getenv("SUPERDEV_PROCESS_ARGV_HELPER") != "1" {
		return
	}
	fmt.Println("structured command runtime argv")
}

func TestManagerStartDeploymentUsesStructuredCommandRuntimeArgv(t *testing.T) {
	var entriesMu sync.Mutex
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		entriesMu.Lock()
		entries = append(entries, e)
		entriesMu.Unlock()
	})
	dep := model.Deployment{
		ID:       "dep-command-runtime-argv",
		EnvName:  "dev",
		Location: model.LocationLocal,
		// shell 命令故意无效；只有 Runtime 的结构化 argv 被采用时，探针子进程才会输出成功标记。
		Command: "superdev-command-that-must-not-run",
		Env: map[string]string{
			"SUPERDEV_PROCESS_ARGV_HELPER": "1",
		},
		Runtime: &model.RuntimeConfig{
			Type:       model.RuntimeTypeCommand,
			Executable: os.Args[0],
			Args:       []string{"-test.run=^TestCommandRuntimeStructuredArgvHelper$"},
		},
	}

	require.NoError(t, mgr.StartDeployment(dep))
	require.Eventually(t, func() bool {
		entriesMu.Lock()
		defer entriesMu.Unlock()
		for _, entry := range entries {
			if entry.DeploymentID == dep.ID && entry.Message == "structured command runtime argv" {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond)
}

func TestManagerRestartDeploymentKeepsRunningStatus(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	dep := model.Deployment{
		ID:       "dep-restart",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-restart"))

	require.NoError(t, mgr.RestartDeployment(dep))
	// 旧监控 goroutine 轮询间隔 200ms，无 sleep 也不应被覆盖为 stopped
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-restart"))

	mgr.StopDeployment("dep-restart")
}

func TestManagerStartDeploymentSkipsWhenAlreadyRunning(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	dep := model.Deployment{
		ID:       "dep-dup",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-dup"))
	firstPID := mgr.DeploymentPID("dep-dup")
	require.NotZero(t, firstPID)

	// 重复启动应为空操作，PID 不变
	require.NoError(t, mgr.StartDeployment(dep))
	assert.Equal(t, firstPID, mgr.DeploymentPID("dep-dup"))

	mgr.StopDeployment("dep-dup")
}

func TestManagerStartDeploymentSkipsAfterBackgroundedCommand(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})
	dir := t.TempDir()
	marker := filepath.Join(dir, "started.log")

	dep := model.Deployment{
		ID:       "dep-bg",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "printf 'started\\n' >> started.log; sleep 60 &",
		WorkDir:  dir,
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(300 * time.Millisecond)

	require.NoError(t, mgr.StartDeployment(dep))
	// 仅应执行一次启动命令（第二次 StartDeployment 被跳过）。
	time.Sleep(100 * time.Millisecond)
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if lines[0] == "" {
		assert.Empty(t, lines)
	} else {
		assert.Len(t, lines, 1)
	}

	mgr.StopDeployment("dep-bg")
}

func TestDeploymentPGIDReturnsGroup(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})
	dep := model.Deployment{
		ID:       "dep-pgid",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	t.Cleanup(func() { mgr.StopDeployment(dep.ID) })
	time.Sleep(100 * time.Millisecond)

	pid := mgr.DeploymentPID(dep.ID)
	pgid := mgr.DeploymentPGID(dep.ID)
	require.Greater(t, pid, 0)
	require.Greater(t, pgid, 0)
	assert.Equal(t, pid, pgid, "Runner starts deployments as process-group leaders")
}

func TestManager_FailureLogIncludesStderrTail(t *testing.T) {
	var logsMu sync.Mutex
	var logs []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		logsMu.Lock()
		logs = append(logs, e)
		logsMu.Unlock()
	})

	require.NoError(t, mgr.StartProcess("dep-x", process.ProcessSpec{
		Command: "echo faildetail 1>&2; exit 7",
		WorkDir: t.TempDir(),
	}))

	require.Eventually(t, func() bool {
		return mgr.Status("dep-x") == model.StatusFailed
	}, 5*time.Second, 20*time.Millisecond)

	logsMu.Lock()
	defer logsMu.Unlock()
	var joined strings.Builder
	for _, e := range logs {
		joined.WriteString(e.Message)
		joined.WriteByte('\n')
	}
	output := joined.String()
	assert.Contains(t, output, "退出码 7")
	assert.Contains(t, output, "  | faildetail")
}

func TestManagerStartProcess(t *testing.T) {
	var entriesMu sync.Mutex
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		entriesMu.Lock()
		entries = append(entries, e)
		entriesMu.Unlock()
	})

	require.NoError(t, mgr.StartProcess("proc-1", process.ProcessSpec{Command: `echo "hello"`, WorkDir: t.TempDir()}))
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.Status("proc-1"))

	// 通过 StartProcess 启动的进程，其日志应以传入的 id 作为 DeploymentID 归属
	entriesMu.Lock()
	entriesSnapshot := append([]model.LogEntry(nil), entries...)
	entriesMu.Unlock()
	require.NotEmpty(t, entriesSnapshot)
	for _, e := range entriesSnapshot {
		assert.Equal(t, "proc-1", e.DeploymentID, "StartProcess 的日志应归属于传入 id")
	}
}

func TestManagerStartDeployment(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	dep := model.Deployment{
		ID:       "dep-1",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-1"))
	assert.Greater(t, mgr.DeploymentPID("dep-1"), 0)

	mgr.StopDeployment("dep-1")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.DeploymentStatus("dep-1"))
}

func TestStartDeploymentStaysStartingUntilReady(t *testing.T) {
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-ready:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	mgr := process.NewManager(func(e model.LogEntry) {})
	dep := model.Deployment{
		ID:          "dep-ready",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
		Readiness:   &model.ReadinessProbe{Type: "http", Target: srv.URL, TimeoutSeconds: 5},
	}
	t.Cleanup(func() { mgr.StopDeployment(dep.ID) })

	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(700 * time.Millisecond)
	require.Equal(t, model.StatusStarting, mgr.DeploymentStatus(dep.ID))

	close(ready)
	require.Eventually(t, func() bool {
		return mgr.DeploymentStatus(dep.ID) == model.StatusRunning
	}, 3*time.Second, 100*time.Millisecond)
}

func TestStartDeploymentNoReadinessRunsImmediately(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})
	dep := model.Deployment{
		ID:          "dep-plain",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Command:     "sleep 5",
		WorkDir:     t.TempDir(),
	}
	t.Cleanup(func() { mgr.StopDeployment(dep.ID) })

	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.DeploymentStatus(dep.ID))
}

func TestManagerRestartDeployment(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	dep := model.Deployment{
		ID:       "dep-restart",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-restart"))

	require.NoError(t, mgr.RestartDeployment(dep))
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-restart"))

	mgr.StopDeployment("dep-restart")
}

func TestManagerDeploymentIsolation(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	dep1 := model.Deployment{ID: "dep-dev", EnvName: "dev", Location: model.LocationLocal, Command: "sleep 60", WorkDir: t.TempDir()}
	dep2 := model.Deployment{ID: "dep-test", EnvName: "test", Location: model.LocationLocal, Command: "sleep 60", WorkDir: t.TempDir()}

	require.NoError(t, mgr.StartDeployment(dep1))
	require.NoError(t, mgr.StartDeployment(dep2))
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-dev"))
	assert.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-test"))

	mgr.StopDeployment("dep-dev")
	mgr.StopDeployment("dep-test")
}

func TestManagerLogEntryDeploymentID(t *testing.T) {
	var entriesMu sync.Mutex
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) {
		entriesMu.Lock()
		entries = append(entries, e)
		entriesMu.Unlock()
	})

	dep := model.Deployment{
		ID:       "dep-log",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  `echo "hello"`,
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(300 * time.Millisecond)

	entriesMu.Lock()
	entriesSnapshot := append([]model.LogEntry(nil), entries...)
	entriesMu.Unlock()
	require.NotEmpty(t, entriesSnapshot)
	for _, e := range entriesSnapshot {
		assert.Equal(t, "dep-log", e.DeploymentID, "所有日志条目应归属于 dep.ID")
	}
}

// TestManagerNotifiesStatusChange 验证 onStatusChange 回调收到 deployment 的状态跃迁序列，
// 且回调本身不在 Manager 内部锁持有期间被调用——这是端口镜像事件帧机制的时延来源，
// 回调若在锁内触发，任何重入 Manager（如调用 m.Status）的调用方都会自死锁。
func TestManagerNotifiesStatusChange(t *testing.T) {
	type change struct {
		id string
		st model.ServiceStatus
	}
	var mu sync.Mutex
	var changes []change

	mgr := process.NewManager(func(model.LogEntry) {})
	mgr.SetOnStatusChange(func(id string, st model.ServiceStatus) {
		// 死锁探针：setStatus 若在持锁状态下调用本回调，这里对 Manager 的重入调用
		// （mgr.Status）会永久阻塞在 mu.Lock() 上。真正暴露该问题的不是"收集到了状态"，
		// 而是这一行重入调用是否能返回。
		_ = mgr.Status(id)
		mu.Lock()
		changes = append(changes, change{id: id, st: st})
		mu.Unlock()
	})

	dep := model.Deployment{
		ID:       "dep-status-change",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 5",
		WorkDir:  t.TempDir(),
	}

	// StartDeployment 在独立 goroutine 中调用并用超时兜底：若回调死锁，StartDeployment
	// 内部的 setStatus 调用会永久阻塞，直接调用会把整个测试拖到 go test 的全局超时
	// （分钟级、栈信息难读）。有界超时能在几秒内给出清晰的失败原因。
	started := make(chan error, 1)
	go func() { started <- mgr.StartDeployment(dep) }()
	select {
	case err := <-started:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("mgr.StartDeployment 未在 3s 内返回：onStatusChange 回调很可能在持锁状态下被调用，触发死锁")
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		var sawStarting, sawRunning bool
		for _, c := range changes {
			if c.id != dep.ID {
				continue
			}
			switch c.st {
			case model.StatusStarting:
				sawStarting = true
			case model.StatusRunning:
				sawRunning = true
			}
		}
		return sawStarting && sawRunning
	}, 3*time.Second, 20*time.Millisecond, "应依次收到 starting、running 两次状态变更通知")

	stopped := make(chan struct{})
	go func() {
		mgr.StopDeployment(dep.ID)
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("mgr.StopDeployment 未在 3s 内返回：onStatusChange 回调很可能在持锁状态下被调用，触发死锁")
	}

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range changes {
			if c.id == dep.ID && c.st == model.StatusStopped {
				return true
			}
		}
		return false
	}, 3*time.Second, 20*time.Millisecond, "应收到 stopped 状态变更通知")
}

// TestManagerRunningPIDsReflectsOnlyRunningDeployments 验证 RunningPIDs 只暴露仍在运行的
// deployment，且 pid 与 DeploymentPID 一致——它是端口镜像 Deps.Resolve（pid 反查 deploymentID）
// 反向扫描的数据来源，必须与正向查询（DeploymentPID）严格对应，否则反查会认错占用者归属。
func TestManagerRunningPIDsReflectsOnlyRunningDeployments(t *testing.T) {
	mgr := process.NewManager(func(model.LogEntry) {})

	running := model.Deployment{
		ID:       "dep-running",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 60",
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(running))
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.DeploymentStatus("dep-running"))

	stopped := model.Deployment{
		ID:       "dep-stopped",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  `echo "done"`,
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(stopped))
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, model.StatusStopped, mgr.DeploymentStatus("dep-stopped"))

	pids := mgr.RunningPIDs()
	pid, ok := pids["dep-running"]
	require.True(t, ok, "运行中的 deployment 应出现在 RunningPIDs 里")
	assert.Equal(t, mgr.DeploymentPID("dep-running"), pid)
	assert.Positive(t, pid)

	_, stoppedPresent := pids["dep-stopped"]
	assert.False(t, stoppedPresent, "已停止的 deployment 不应出现在 RunningPIDs 里")

	mgr.StopDeployment("dep-running")
}
