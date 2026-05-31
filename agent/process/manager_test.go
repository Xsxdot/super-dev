package process_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

func TestManagerStartStopDeployment(t *testing.T) {
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) { entries = append(entries, e) })

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
	for _, e := range entries {
		assert.NotEmpty(t, e.DeploymentID)
	}
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

func TestManagerStartProcess(t *testing.T) {
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) { entries = append(entries, e) })

	require.NoError(t, mgr.StartProcess("proc-1", process.ProcessSpec{Command: `echo "hello"`, WorkDir: t.TempDir()}))
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.Status("proc-1"))

	// 通过 StartProcess 启动的进程，其日志应以传入的 id 作为 DeploymentID 归属
	require.NotEmpty(t, entries)
	for _, e := range entries {
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
	var entries []model.LogEntry
	mgr := process.NewManager(func(e model.LogEntry) { entries = append(entries, e) })

	dep := model.Deployment{
		ID:       "dep-log",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  `echo "hello"`,
		WorkDir:  t.TempDir(),
	}
	require.NoError(t, mgr.StartDeployment(dep))
	time.Sleep(300 * time.Millisecond)

	require.NotEmpty(t, entries)
	for _, e := range entries {
		assert.Equal(t, "dep-log", e.DeploymentID, "所有日志条目应归属于 dep.ID")
	}
}
