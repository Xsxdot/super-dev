// Package api_test 验证项目 runtime-status HTTP 聚合接口。
//
// 职责：
//   - 验证本机 deployment 按环境聚合
//   - 验证远端 host 失败隔离
//   - 锁定 runtime sampler 的 SampleTarget 映射
//
// 边界：
//   - 不执行真实 systemd/docker/ps 命令
//   - 不建立真实 SSH 隧道
package api_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/xsxdot/super-dev/agent/api"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/noderegistry"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

type fakeRuntimeSampler struct {
	mu           sync.Mutex
	byDeployment map[string]model.InstanceMetrics
	targets      []metrics.SampleTarget
}

func (f *fakeRuntimeSampler) Sample(ctx context.Context, target metrics.SampleTarget) (model.InstanceMetrics, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets = append(f.targets, target)
	if got, ok := f.byDeployment[target.DeploymentID]; ok {
		return got, nil
	}
	return model.InstanceMetrics{Health: model.HealthUnknown, Base: target.Base}, errors.New("missing fake sample")
}

func (f *fakeRuntimeSampler) targetsByDeployment() map[string]metrics.SampleTarget {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]metrics.SampleTarget{}
	for _, target := range f.targets {
		out[target.DeploymentID] = target
	}
	return out
}

func TestRuntimeStatusAggregatesLocalDeploymentsByEnvironment(t *testing.T) {
	sampler := &fakeRuntimeSampler{byDeployment: map[string]model.InstanceMetrics{
		"dep-api-dev":  runningMetrics(12.5, 1000, "process"),
		"dep-web-prod": runningMetrics(1.5, 2000, "docker"),
	}}
	app := newRuntimeStatusApp(t, sampler)
	projectID := addRuntimeStatusProject(t, app, `
id: overview-local
name: overview-local
environments:
  - name: dev
  - name: prod
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-dev
        env: dev
        location: local
        runtime:
          type: command
          command: echo api
  - id: svc-web
    name: web
    deployments:
      - id: dep-web-prod
        env: prod
        location: local
        runtime:
          type: docker
          container: web-prod
`)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Environments, 2)
	assert.Equal(t, "dev", got.Environments[0].EnvName)
	assert.Equal(t, "prod", got.Environments[1].EnvName)
	assert.Equal(t, "api", got.Environments[0].Instances[0].ServiceName)
	assert.True(t, got.Environments[0].Instances[0].IsLocal)
	assert.Equal(t, model.HealthRunning, got.Environments[0].Instances[0].Metrics.Health)
	targets := sampler.targetsByDeployment()
	assert.Equal(t, "docker", targets["dep-web-prod"].Base)
	assert.Equal(t, "web-prod", targets["dep-web-prod"].Container)
}

func TestRuntimeStatusKeepsManagerActiveDeploymentRunningWhenSamplerReportsStopped(t *testing.T) {
	sampler := &fakeRuntimeSampler{byDeployment: map[string]model.InstanceMetrics{
		"dep-api-dev": {Health: model.HealthStopped, Base: "command"},
	}}
	app := newRuntimeStatusApp(t, sampler)
	projectID := addRuntimeStatusProject(t, app, `
id: overview-manager-active
name: overview-manager-active
environments:
  - name: dev
    is_dev: true
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-dev
        env: dev
        location: local
        runtime:
          type: command
          command: sleep 60
`)

	start := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/api/deployments/dep-api-dev/start", nil)
	app.Handler().ServeHTTP(start, startReq)
	require.Equal(t, http.StatusOK, start.Code)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	assert.Equal(t, model.HealthRunning, got.Environments[0].Instances[0].Metrics.Health)
	targets := sampler.targetsByDeployment()
	assert.NotZero(t, targets["dep-api-dev"].PGID)
	assert.Zero(t, targets["dep-api-dev"].PID)
}

func TestRuntimeStatusPassesLaunchdLabelToSampler(t *testing.T) {
	sampler := &fakeRuntimeSampler{byDeployment: map[string]model.InstanceMetrics{
		"dep-worker-prod": runningMetrics(1, 2048, "launchd"),
	}}
	app := newRuntimeStatusApp(t, sampler)
	projectID := addRuntimeStatusProject(t, app, `
id: overview-launchd
name: overview-launchd
environments:
  - name: prod
services:
  - id: svc-worker
    name: worker
    deployments:
      - id: dep-worker-prod
        env: prod
        location: local
        runtime:
          type: launchd
          label: com.example.worker
`)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	targets := sampler.targetsByDeployment()
	assert.Equal(t, "launchd", targets["dep-worker-prod"].Base)
	assert.Equal(t, "com.example.worker", targets["dep-worker-prod"].Label)
	assert.Zero(t, targets["dep-worker-prod"].PID)
}

func TestRuntimeStatusIsolatesRemoteHostFailure(t *testing.T) {
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	app, err := api.NewApp(api.AppConfig{
		DataDir:                     t.TempDir(),
		RuntimeMetricsSampler:       &fakeRuntimeSampler{byDeployment: map[string]model.InstanceMetrics{}},
		RuntimeStatusRequestTimeout: 200 * time.Millisecond,
		NodeRegistryOverride:        reg,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	projectID := addRuntimeStatusProject(t, app, `
id: overview-remote
name: overview-remote
environments:
  - name: prod
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api-prod
        env: prod
        location: remote
        hosts: [host-ok, host-bad]
        runtime:
          type: systemd
          service_name: api.service
`)

	createHost(t, app, "host-ok", "ok-node")
	createHost(t, app, "host-bad", "bad-node")
	reg.ApplyForTest([]nodetransport.NodeStatus{
		{
			HostID:    "host-ok",
			Name:      "ok-node",
			Reachable: true,
			Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
			Deployments: []model.InstanceStatus{{
				ServiceID: "svc-api", ServiceName: "api", DeploymentID: "dep-api-prod",
				NodeID: "host-ok", NodeName: "ok-node", IsLocal: false,
				Metrics: runningMetrics(7, 3000, "systemd"),
			}},
			UpdatedAt: time.Now().UTC(),
		},
		{
			HostID:    "host-bad",
			Name:      "bad-node",
			Reachable: false,
			Agent:     model.AgentRuntime{Health: model.AgentHealthUnreachable, Reachable: false},
			Error:     "tunnel timeout",
			UpdatedAt: time.Now().UTC(),
		},
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 2)
	byNode := map[string]model.InstanceStatus{}
	for _, inst := range got.Environments[0].Instances {
		byNode[inst.NodeID] = inst
	}
	assert.Equal(t, model.HealthRunning, byNode["host-ok"].Metrics.Health)
	assert.Equal(t, model.HealthUnknown, byNode["host-bad"].Metrics.Health)
	assert.Contains(t, byNode["host-bad"].Error, "tunnel timeout")
}

func TestRuntimeStatusUsesNodeRegistryForRemoteDeployments(t *testing.T) {
	reg := noderegistry.New([]nodetransport.NodeTransport{}, noderegistry.Options{StaleAfter: time.Hour})
	app, err := api.NewApp(api.AppConfig{
		DataDir:                     t.TempDir(),
		RuntimeMetricsSampler:       &fakeRuntimeSampler{byDeployment: map[string]model.InstanceMetrics{}},
		RuntimeStatusRequestTimeout: 200 * time.Millisecond,
		NodeRegistryOverride:        reg,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)

	projectID := addRuntimeStatusProject(t, app, `
id: overview-node-registry
name: overview-node-registry
environments:
  - name: prod
services:
  - id: svc-api
    name: api
    deployments:
      - id: dep-api
        env: prod
        location: remote
        hosts: [h1]
`)
	createHost(t, app, "h1", "ali-01")
	reg.ApplyForTest([]nodetransport.NodeStatus{{
		HostID:    "h1",
		Name:      "ali-01",
		Reachable: true,
		Agent:     model.AgentRuntime{Health: model.AgentHealthHealthy, Reachable: true},
		Deployments: []model.InstanceStatus{{
			DeploymentID: "dep-api",
			NodeID:       "h1",
			NodeName:     "ali-01",
			Metrics:      model.InstanceMetrics{Health: model.HealthRunning, Base: "systemd"},
		}},
		UpdatedAt: time.Now().UTC(),
	}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/projects/"+projectID+"/runtime-status", nil)
	app.Handler().ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var got model.RuntimeStatusResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	inst := got.Environments[0].Instances[0]
	assert.Equal(t, "dep-api", inst.DeploymentID)
	assert.Equal(t, model.HealthRunning, inst.Metrics.Health)
	assert.Empty(t, inst.Error)
}

func runningMetrics(cpu float64, mem int64, base string) model.InstanceMetrics {
	restarts := 0
	uptime := int64(60)
	return model.InstanceMetrics{
		CPUPercent: &cpu,
		MemBytes:   &mem,
		UptimeSec:  &uptime,
		Restarts:   &restarts,
		Health:     model.HealthRunning,
		Base:       base,
	}
}

func newRuntimeStatusApp(t *testing.T, sampler metrics.MetricsSampler) *api.App {
	t.Helper()
	app, err := api.NewApp(api.AppConfig{
		DataDir:                     t.TempDir(),
		RuntimeMetricsSampler:       sampler,
		RuntimeStatusRequestTimeout: 200 * time.Millisecond,
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	return app
}

func addRuntimeStatusProject(t *testing.T, app *api.App, yaml string) string {
	t.Helper()
	projectDir := t.TempDir()
	configDir := filepath.Join(projectDir, ".superdev")
	require.NoError(t, os.MkdirAll(configDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(yaml), 0o644))

	req := httptest.NewRequest(http.MethodPost, "/api/projects", strings.NewReader(`{"root_path":"`+projectDir+`"}`))
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var project model.Project
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&project))
	return project.ID
}

func createHost(t *testing.T, app *api.App, id, name string) {
	t.Helper()
	body := strings.NewReader(`{"id":"` + id + `","name":"` + name + `","private_ip":"127.0.0.1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/hosts", body)
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}
