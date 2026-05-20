package process_test

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

func TestManagerStartStopService(t *testing.T) {
	var lines []string
	mgr := process.NewManager(func(e model.LogEntry) { lines = append(lines, e.Message) })

	svc := model.Service{
		ID:      "svc-1",
		Name:    "test",
		Command: `echo "started"`,
		WorkDir: t.TempDir(),
		Order:   0,
	}
	require.NoError(t, mgr.Start(svc))
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.Status("svc-1"))

	svc2 := model.Service{ID: "svc-2", Name: "long", Command: "sleep 60", WorkDir: t.TempDir()}
	require.NoError(t, mgr.Start(svc2))
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.Status("svc-2"))

	mgr.Stop("svc-2")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.Status("svc-2"))
}

func TestManagerRestartKeepsRunningStatus(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	svc := model.Service{
		ID:      "svc-restart",
		Name:    "long",
		Command: "sleep 60",
		WorkDir: t.TempDir(),
	}
	require.NoError(t, mgr.Start(svc))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.Status("svc-restart"))

	require.NoError(t, mgr.Restart(svc))
	// 旧监控 goroutine 轮询间隔 200ms，无 sleep 也不应被覆盖为 stopped
	time.Sleep(400 * time.Millisecond)
	assert.Equal(t, model.StatusRunning, mgr.Status("svc-restart"))

	mgr.Stop("svc-restart")
}

func TestManagerStartSkipsWhenAlreadyRunning(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	svc := model.Service{
		ID:      "svc-dup",
		Name:    "long",
		Command: "sleep 60",
		WorkDir: t.TempDir(),
	}
	require.NoError(t, mgr.Start(svc))
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, model.StatusRunning, mgr.Status("svc-dup"))
	firstPID := mgr.PID("svc-dup")
	require.NotZero(t, firstPID)

	require.NoError(t, mgr.Start(svc))
	assert.Equal(t, firstPID, mgr.PID("svc-dup"))

	mgr.Stop("svc-dup")
}

func TestManagerStartSkipsAfterBackgroundedCommand(t *testing.T) {
	mgr := process.NewManager(func(e model.LogEntry) {})

	svc := model.Service{
		ID:      "svc-bg",
		Name:    "bg",
		Command: "sleep 60 &",
		WorkDir: t.TempDir(),
	}
	require.NoError(t, mgr.Start(svc))
	time.Sleep(300 * time.Millisecond)

	require.NoError(t, mgr.Start(svc))
	// 仅应有一个 sleep 子进程（第二次 Start 被跳过）
	time.Sleep(100 * time.Millisecond)
	out, err := exec.Command("pgrep", "-f", "sleep 60").Output()
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if lines[0] == "" {
		assert.Empty(t, lines)
	} else {
		assert.Len(t, lines, 1)
	}

	mgr.Stop("svc-bg")
}

func TestManagerStartGroup(t *testing.T) {
	var mu sync.Mutex
	_ = mu
	mgr := process.NewManager(func(e model.LogEntry) {})

	services := []model.Service{
		{ID: "a", Name: "a", Command: `sleep 0.1 && echo a`, WorkDir: t.TempDir(), Order: 1},
		{ID: "b", Name: "b", Command: `sleep 0.1 && echo b`, WorkDir: t.TempDir(), Order: 1},
		{ID: "c", Name: "c", Command: `echo c`, WorkDir: t.TempDir(), Order: 2},
	}
	require.NoError(t, mgr.StartGroup(services))
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, model.StatusStopped, mgr.Status("a"))
	assert.Equal(t, model.StatusStopped, mgr.Status("b"))
	assert.Equal(t, model.StatusStopped, mgr.Status("c"))
}
