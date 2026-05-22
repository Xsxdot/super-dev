package collector_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/collector"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/process"
)

func newTestManager(t *testing.T) *collector.Manager {
	t.Helper()
	procMgr := process.NewManager(func(model.LogEntry) {})
	// 注入伪造的"探测器":对任意 name 都说存在,简化测试。
	probe := collector.ProbeFunc(func(t model.LogSourceType, name string) error { return nil })
	return collector.NewManager(procMgr, probe)
}

func TestManagerStartStop(t *testing.T) {
	mgr := newTestManager(t)

	id, err := mgr.StartForTest("svc-stub", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	time.Sleep(150 * time.Millisecond)
	col, ok := mgr.Get(id)
	require.True(t, ok)
	assert.Equal(t, model.StatusRunning, col.Status)

	require.NoError(t, mgr.Stop(id))
	time.Sleep(200 * time.Millisecond)
	_, ok = mgr.Get(id)
	assert.False(t, ok)
}

func TestManagerStartIsIdempotent(t *testing.T) {
	mgr := newTestManager(t)

	id1, err := mgr.StartForTest("nova-api", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Stop(id1) })

	// 第二次 Start 相同 (name, type) 应返回同一 ID,不重新启动。
	id2, err := mgr.StartForTest("nova-api", model.LogSourceTypeJournalctl, []string{"sleep", "60"})
	require.NoError(t, err)
	assert.Equal(t, id1, id2)

	list := mgr.List()
	assert.Len(t, list, 1)
}

func TestManagerProbeFailure(t *testing.T) {
	procMgr := process.NewManager(func(model.LogEntry) {})
	probe := collector.ProbeFunc(func(t model.LogSourceType, name string) error {
		return collector.ErrTargetNotFound
	})
	mgr := collector.NewManager(procMgr, probe)

	_, err := mgr.Start("nonexistent", model.LogSourceTypeJournalctl)
	require.ErrorIs(t, err, collector.ErrTargetNotFound)
}

func TestManagerStartRejectsInvalidName(t *testing.T) {
	mgr := newTestManager(t)
	_, err := mgr.Start("; rm -rf /", model.LogSourceTypeJournalctl)
	require.ErrorIs(t, err, collector.ErrInvalidName)
}
