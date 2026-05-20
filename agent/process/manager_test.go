package process_test

import (
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
