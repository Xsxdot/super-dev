// managed_deployments_e2e_test.go 覆盖远端 managed deployment 的端到端恢复链路。
//
// 职责：
//   - 串起桌面端 project 注册、host desired 推送和远端 managed apply
//   - 验证桌面端 remote runtime-status 能读取远端本机视图
//   - 验证桌面端 deployment log backend 能读取远端 collector 日志
//
// 边界：
//   - 不建立真实 SSH 隧道，使用测试 resolver 指向 httptest server
//   - 不依赖真实 systemd/docker 目标，collector probe 使用测试桩
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/collector"
	"github.com/xsxdot/super-dev/agent/logbackend"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
)

type e2eRuntimeSampler struct{}

func (e2eRuntimeSampler) Sample(ctx context.Context, target metrics.SampleTarget) (model.InstanceMetrics, error) {
	cpu := 9.5
	mem := int64(4096)
	return model.InstanceMetrics{CPUPercent: &cpu, MemBytes: &mem, Health: model.HealthRunning, Base: target.Base}, nil
}

func TestManagedDeploymentReconcileRestoresRemoteLogsAndRuntimeStatus(t *testing.T) {
	remoteApp, err := NewApp(AppConfig{
		DataDir: t.TempDir(),
		ProbeOverride: collector.ProbeFunc(func(model.LogSourceType, string) error {
			return nil
		}),
		RuntimeMetricsSampler: e2eRuntimeSampler{},
	})
	require.NoError(t, err)
	t.Cleanup(remoteApp.Close)
	remoteSrv := httptest.NewServer(remoteApp.Handler())
	t.Cleanup(remoteSrv.Close)

	desktopApp, err := NewApp(AppConfig{
		DataDir:               t.TempDir(),
		NodeTransportOverride: testNodeTransport{table: map[string]string{"h1": remoteSrv.URL}},
	})
	require.NoError(t, err)
	t.Cleanup(desktopApp.Close)
	desktopSrv := httptest.NewServer(desktopApp.Handler())
	t.Cleanup(desktopSrv.Close)

	_, err = desktopApp.remoteStore.AddHost(model.Host{ID: "h1", Name: "local-01"})
	require.NoError(t, err)
	projectDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, ".superdev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, ".superdev", "config.yaml"), []byte(`
id: proj-e2e
name: proj-e2e
environments:
  - name: prod
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-prod
        env: prod
        location: remote
        hosts: [h1]
        runtime:
          type: systemd
          service_name: api.service
        logs:
          type: command
          command: printf remote-log
`), 0o644))

	addReq := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	addResp := httptest.NewRecorder()
	desktopApp.Handler().ServeHTTP(addResp, addReq)
	require.Equal(t, http.StatusOK, addResp.Code)

	require.Eventually(t, func() bool {
		resp, err := http.Get(remoteSrv.URL + "/api/projects")
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var projects []model.Project
		if err := json.NewDecoder(resp.Body).Decode(&projects); err != nil {
			return false
		}
		return len(projects) == 1
	}, 2*time.Second, 20*time.Millisecond)

	statusResp, err := http.Get(desktopSrv.URL + "/api/projects/proj-e2e/runtime-status")
	require.NoError(t, err)
	defer statusResp.Body.Close()
	require.Equal(t, http.StatusOK, statusResp.StatusCode)
	var status model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(statusResp.Body).Decode(&status))
	require.Len(t, status.Environments, 1)
	require.Len(t, status.Environments[0].Instances, 1)
	inst := status.Environments[0].Instances[0]
	assert.Equal(t, "dep-api-prod", inst.DeploymentID)
	assert.Equal(t, "h1", inst.NodeID)
	// 远端 agent 自身返回 IsLocal=true；桌面端 remoteInstance 会按桌面视角改写为 false。
	assert.False(t, inst.IsLocal)
	assert.Equal(t, model.HealthRunning, inst.Metrics.Health)

	collectorID := collector.CollectorID("printf remote-log", model.LogSourceTypeCommand)
	remoteApp.WriteTestLog(model.LogEntry{
		DeploymentID: collectorID,
		Timestamp:    time.Now().UTC(),
		Level:        "INFO",
		Message:      "remote log recovered",
		Stream:       "stdout",
	})
	require.Eventually(t, func() bool {
		backend, ok := desktopApp.lookupBackend("dep-api-prod")
		if !ok {
			return false
		}
		entries, _, err := backend.Query(context.Background(), logbackend.QueryFilter{DeploymentID: "dep-api-prod", Limit: 10})
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if entry.Message == "remote log recovered" {
				return true
			}
		}
		return false
	}, 2*time.Second, 20*time.Millisecond)
}
