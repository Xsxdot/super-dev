// handler_managed_deployments_test.go 验证远端 managed deployment 下发入口。
//
// 职责：
//   - 覆盖 PUT /api/managed-deployments 的落盘和内存应用
//   - 覆盖远端 agent 启动后从 managed-deployments.json 恢复本机运行视图
//
// 边界：
//   - 不测试桌面端 host 投影和 tunnel 推送
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
)

type managedRuntimeSampler struct{}

func (managedRuntimeSampler) Sample(ctx context.Context, target metrics.SampleTarget) (model.InstanceMetrics, error) {
	cpu := 2.5
	mem := int64(1024)
	restarts := 1
	return model.InstanceMetrics{
		CPUPercent: &cpu,
		MemBytes:   &mem,
		Restarts:   &restarts,
		Health:     model.HealthRunning,
		Base:       target.Base,
	}, nil
}

func newManagedDeploymentTestApp(t *testing.T, dataDir string) *App {
	t.Helper()
	app, err := NewApp(AppConfig{
		DataDir: dataDir,
		ProbeOverride: collector.ProbeFunc(func(model.LogSourceType, string) error {
			return nil
		}),
		RuntimeMetricsSampler: managedRuntimeSampler{},
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
}

func managedDeploymentFixture() []model.ManagedDeployment {
	return []model.ManagedDeployment{{
		DeploymentID: "dep-api-prod",
		ServiceID:    "svc-api",
		ServiceName:  "api",
		ProjectID:    "proj-prod",
		EnvName:      "prod",
		Location:     model.LocationRemote,
		Runtime:      &model.RuntimeConfig{Type: model.RuntimeTypeSystemd, ServiceName: "api.service"},
		Logs:         &model.LogConfig{Type: model.LogKindCommand, Command: "printf managed-log"},
	}}
}

func TestPutManagedDeploymentsPersistsRegistersProjectAndRuntimeStatus(t *testing.T) {
	dataDir := t.TempDir()
	app := newManagedDeploymentTestApp(t, dataDir)
	body, err := json.Marshal(managedDeploymentFixture())
	require.NoError(t, err)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/managed-deployments", bytes.NewReader(body))
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var result model.ManagedDeploymentReconcileResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	assert.True(t, result.Persisted)
	assert.Equal(t, 1, result.DeploymentCount)
	assert.Equal(t, 1, result.CollectorCount)

	raw, err := os.ReadFile(filepath.Join(dataDir, "managed-deployments.json"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "dep-api-prod")

	status := httptest.NewRecorder()
	statusReq := httptest.NewRequest(http.MethodGet, "/api/projects/proj-prod/runtime-status", nil)
	app.Handler().ServeHTTP(status, statusReq)
	require.Equal(t, http.StatusOK, status.Code)
	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(status.Body).Decode(&got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	inst := got.Environments[0].Instances[0]
	assert.Equal(t, "dep-api-prod", inst.DeploymentID)
	assert.True(t, inst.IsLocal)
	assert.Equal(t, model.HealthRunning, inst.Metrics.Health)
}

func TestLoadManagedDeploymentsRestoresRemoteAgentState(t *testing.T) {
	dataDir := t.TempDir()
	store := NewManagedStore(dataDir)
	require.NoError(t, store.Save(managedDeploymentFixture()))
	app := newManagedDeploymentTestApp(t, dataDir)

	app.loadManagedDeployments()

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/proj-prod/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}
