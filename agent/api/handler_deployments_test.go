// Package api 验证 deployment 运行态端点接入 operation 安全门禁。
//
// 职责：
//   - 验证 dev local deployment 仍可直接启停
//   - 验证 non-dev local deployment 需要审批
//
// 边界：
//   - 不通过 MCP 工具调用
//   - 只使用 harmless command
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/agenthealth"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
	"github.com/xsxdot/super-dev/agent/process"
	"github.com/xsxdot/super-dev/agent/remoteexec"
)

func TestDeploymentRuntimeEndpoint_AllowsDevLocalWithoutApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(true, false))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/start", map[string]any{}, http.StatusOK)

	assert.Equal(t, "starting", resp["status"])
}

func TestDeploymentRuntimeEndpoint_RequiresApprovalForNonDevLocal(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(operationAPIProject(false, false))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/restart", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "approval_required", resp["code"])
	assert.NotNil(t, resp["plan"])
	assert.NotNil(t, resp["approval"])
}

func TestDeploymentRuntimeEndpoint_RequiresApprovalForRemoteManaged(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := remoteOperationAPIProject(true)
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/stop", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "approval_required", resp["code"])
}

func TestDeploymentRuntimeEndpoint_RunsRemoteStopAfterApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	_, err = app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	project := remoteOperationAPIProject(true)
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	runner := &recordingPipelineAgentRunner{}
	app.pipelineAgentRunner = runner
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true},
	})
	app.agentHealth.ProbeOnce(context.Background(), "h1")
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/stop", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "remote stop",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/deployments/api-prod/stop", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.Equal(t, "stopped", ok["status"])
	require.Len(t, runner.requests, 1)
	assert.Equal(t, remoteexec.CommandRequest{Command: "systemctl stop api", WorkDir: ""}, runner.requests[0])
}

func TestDeploymentRuntimeEndpoint_RunsRemoteStopOnSingleHostAfterApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	_, err = app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	_, err = app.remoteStore.AddHost(model.Host{ID: "h2", Name: "prod-b"})
	require.NoError(t, err)
	project := remoteOperationAPIProject(true)
	project.Services[0].Deployments[0].HostIDs = []string{"h1", "h2"}
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	runner := &recordingPipelineAgentRunner{}
	app.pipelineAgentRunner = runner
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true},
	})
	app.agentHealth.ProbeOnce(context.Background(), "h1")
	app.agentHealth.ProbeOnce(context.Background(), "h2")
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/deployments/api-prod/hosts/h2/stop", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	plan := required["plan"].(map[string]any)
	target := plan["target"].(map[string]any)
	assert.Equal(t, "h2", target["host_id"])
	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "remote stop h2",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/deployments/api-prod/hosts/h2/stop", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.Equal(t, "stopped", ok["status"])
	require.Len(t, runner.requests, 1)
	assert.Equal(t, remoteexec.CommandRequest{Command: "systemctl stop api", WorkDir: ""}, runner.requests[0])
	require.Len(t, runner.targets, 1)
	assert.Equal(t, "h2", runner.targets[0].HostID)
	assert.Equal(t, "prod-b", runner.targets[0].HostName)
}

func TestStartEnvSelectedRequiresApprovalForNonDevLocal(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := operationAPIProject(false, false)
	project.Services[0].Required = true
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/projects/proj-op/envs/prod/start-selected", map[string]any{}, http.StatusForbidden)

	assert.Equal(t, "approval_required", required["code"])
	plan := required["plan"].(map[string]any)
	assert.Equal(t, operation.OperationRuntimeStartSelected, plan["kind"])
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "start selected",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/projects/proj-op/envs/prod/start-selected", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.Equal(t, "starting", ok["status"])
}

func TestStartEnvSelectedRunsRemoteStartAfterApproval(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	_, err = app.remoteStore.AddHost(model.Host{ID: "h1", Name: "prod-a"})
	require.NoError(t, err)
	project := remoteOperationAPIProject(true)
	project.Services[0].Required = true
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	runner := &recordingPipelineAgentRunner{}
	app.pipelineAgentRunner = runner
	app.agentHealth = agenthealth.NewMonitor(staticAgentHealthProber{
		result: agenthealth.ProbeResult{AllEndpointsOK: true},
	})
	app.agentHealth.ProbeOnce(context.Background(), "h1")
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	required := postJSONForRawTest(t, srv.URL+"/api/projects/proj-op/envs/prod/start-selected", map[string]any{}, http.StatusForbidden)
	approvalID := required["approval"].(map[string]any)["id"].(string)
	_ = postJSONForTest[operation.Approval](t, srv.URL+"/api/operation-approvals/"+approvalID+"/approve", map[string]any{
		"decided_by": "user",
		"note":       "remote start selected",
	}, http.StatusOK)
	detail := getJSONForTest[operationApprovalDetailResponse](t, srv.URL+"/api/operation-approvals/"+approvalID, http.StatusOK)

	ok := postJSONWithHeadersForTest[map[string]string](t, srv.URL+"/api/projects/proj-op/envs/prod/start-selected", map[string]any{}, map[string]string{
		"X-SuperDev-Approval-Token": detail.ApprovalToken,
	}, http.StatusOK)

	assert.Equal(t, "starting", ok["status"])
	require.Len(t, runner.requests, 1)
	assert.Equal(t, remoteexec.CommandRequest{Command: "systemctl start api", WorkDir: ""}, runner.requests[0])
}

func TestRestart_AfterExternalKill_Succeeds(t *testing.T) {
	app := newTestAppForPackage(t)
	dep := model.Deployment{
		ID:       "dep-restart-killed",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 30",
		WorkDir:  t.TempDir(),
	}

	require.NoError(t, app.startDeploymentRuntime(context.Background(), "proj-restart", dep))
	mgr := app.getOrCreateManager("proj-restart")
	oldPGID := mgr.DeploymentPID(dep.ID)
	require.NotZero(t, oldPGID)
	require.NoError(t, syscall.Kill(-oldPGID, syscall.SIGKILL))

	require.NoError(t, app.restartDeploymentRuntime(context.Background(), "proj-restart", dep))
	require.Eventually(t, func() bool {
		newPGID := mgr.DeploymentPID(dep.ID)
		return mgr.IsDeploymentActive(dep.ID) && newPGID != 0 && newPGID != oldPGID
	}, 5*time.Second, 20*time.Millisecond)
	mgr.StopDeployment(dep.ID)
}

func TestApplyProcessReconcileResults_RemovesPidStoreEntry(t *testing.T) {
	app := newTestAppForPackage(t)
	app.pidStore.Set("dep-pidstore-dead", 12345)
	require.NoError(t, app.pidStore.Flush())

	app.applyProcessReconcileResults([]process.ReconcileResult{{
		ID:        "dep-pidstore-dead",
		Corrected: true,
		Status:    model.StatusFailed,
	}})

	pgids := app.pidStore.LoadAll()
	assert.NotContains(t, pgids, "dep-pidstore-dead")
}

func TestControlEvents_RestartEmitsLifecycle(t *testing.T) {
	app := newTestAppForPackage(t)
	dep := model.Deployment{
		ID:       "dep-control-restart",
		EnvName:  "dev",
		Location: model.LocationLocal,
		Command:  "sleep 30",
		WorkDir:  t.TempDir(),
	}
	ch := app.buf.Subscribe("control-events-restart")
	defer app.buf.Unsubscribe("control-events-restart")

	require.NoError(t, app.restartDeploymentRuntime(context.Background(), "proj-control", dep))

	events := collectControlEvents(t, ch, dep.ID, 4)
	got := controlEventPhases(events)
	assert.Equal(t, []string{"command_received", "reconciling", "executing", "succeeded"}, got)
	app.getOrCreateManager("proj-control").StopDeployment(dep.ID)
}

func collectControlEvents(t *testing.T, ch <-chan model.LogEntry, depID string, want int) []model.LogEntry {
	t.Helper()
	var out []model.LogEntry
	deadline := time.After(5 * time.Second)
	for len(out) < want {
		select {
		case e, ok := <-ch:
			if !ok {
				t.Fatalf("control event channel closed after %d/%d events", len(out), want)
			}
			if e.DeploymentID == depID && e.Stream == "control" {
				out = append(out, e)
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %d control events, got %d: %+v", want, len(out), out)
		}
	}
	return out
}

func controlEventPhases(events []model.LogEntry) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		switch {
		case strings.Contains(e.Message, "command_received"):
			out = append(out, "command_received")
		case strings.Contains(e.Message, "reconciling"):
			out = append(out, "reconciling")
		case strings.Contains(e.Message, "executing"):
			out = append(out, "executing")
		case strings.Contains(e.Message, "succeeded"):
			out = append(out, "succeeded")
		case strings.Contains(e.Message, "failed"):
			out = append(out, "failed")
		}
	}
	return out
}

func remoteOperationAPIProject(isDev bool) model.Project {
	project := operationAPIProject(isDev, false)
	project.Services[0].Deployments[0].Location = model.LocationRemote
	project.Services[0].Deployments[0].HostIDs = []string{"h1"}
	project.Services[0].Deployments[0].StartCommand = "systemctl start api"
	project.Services[0].Deployments[0].StopCommand = "systemctl stop api"
	return project
}
