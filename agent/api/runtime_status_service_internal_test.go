// runtime_status_service_internal_test.go 验证 runtime-status 的内部采样目标映射。
//
// 职责：锁定 language runtime 作为 process manager 托管进程时的 PGID 采样路径。
// 边界：不走 HTTP handler，不采样真实指标。
package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/process"
)

func TestRuntimeStatusSampleTargetTreatsLanguageRuntimeAsProcessManaged(t *testing.T) {
	mgr := process.NewManager(func(model.LogEntry) {})
	require.NoError(t, mgr.StartProcess("dep-api-dev", process.ProcessSpec{Command: "sleep 60"}))
	t.Cleanup(func() { mgr.Stop("dep-api-dev") })
	app := &App{
		managers:  map[string]*process.Manager{"proj-lang": mgr},
		codeDebug: codedebug.NewManager(codedebug.ManagerOptions{}),
	}
	svc := runtimeStatusService{app: app}
	dep := model.Deployment{
		ID: "dep-api-dev",
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Config: map[string]any{"program": "./cmd/api"},
		},
	}

	target := svc.sampleTarget("proj-lang", dep)

	assert.Equal(t, string(model.RuntimeTypeLanguage), target.Base)
	assert.NotZero(t, target.PGID)
	assert.Zero(t, target.PID)
}
