// handler_code_debug_test.go 验证本机代码调试 HTTP API。
//
// 职责：
//   - 验证代码调试 target/session API 接入 agent
//   - 验证打开 session 进入 operation 审批链路
//   - 验证 evaluate 只审计表达式 hash 和元数据
//
// 边界：
//   - 不启动真实 DAP adapter
//   - 不执行真实目标进程
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/codedebug"
	"github.com/xsxdot/super-dev/agent/metrics"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/operation"
)

func TestListCodeDebugTargets(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: codedebug.NewManager(codedebug.ManagerOptions{})})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(codeDebugAPIProject(t.TempDir()))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	targets := getJSONForTest[[]codedebug.Target](t, srv.URL+"/api/code-debug-targets", http.StatusOK)

	require.Len(t, targets, 1)
	assert.Equal(t, "dep-api-dev", targets[0].DeploymentID)
}

func TestOpenCodeDebugSessionRequiresApproval(t *testing.T) {
	mgr := codeDebugManagerForAPITest()
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: mgr})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	app.mu.Lock()
	app.appendProjectLocked(codeDebugAPIProject(t.TempDir()))
	app.mu.Unlock()
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/code-debug-sessions", map[string]any{"deployment_id": "dep-api-dev"}, http.StatusForbidden)

	assert.Equal(t, "approval_required", resp["code"])
}

func TestCodeDebugEvaluateAuditsExpressionHashOnDenial(t *testing.T) {
	mgr := codeDebugManagerForAPITest()
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: mgr})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := codeDebugAPIProject(t.TempDir())
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	session, err := mgr.Open(context.Background(), project, project.Services[0], project.Services[0].Deployments[0], codedebug.OpenRequest{DeploymentID: "dep-api-dev"})
	require.NoError(t, err)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	_ = postJSONForRawTest(t, srv.URL+"/api/code-debug-sessions/"+session.ID+"/evaluate", map[string]any{
		"expression": "os.Getenv(\"SECRET_TOKEN\")",
		"frame_id":   1,
	}, http.StatusForbidden)
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.OperationCodeDebugEvaluate})
	require.NoError(t, err)
	require.NotEmpty(t, events)
	rawBytes, err := json.Marshal(events)
	require.NoError(t, err)
	raw := string(rawBytes)

	assert.Contains(t, raw, "expression_hash")
	assert.Contains(t, raw, "expression_length")
	assert.NotContains(t, raw, "SECRET_TOKEN")
}

func TestCodeDebugEvaluateAuditsExpressionHashOnSuccess(t *testing.T) {
	mgr := codeDebugManagerForAPITest()
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: mgr})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	settings, err := app.settings.Load()
	require.NoError(t, err)
	settings.Approval.CodeDebugEvaluate = false
	require.NoError(t, app.settings.Save(settings))
	project := codeDebugAPIProject(t.TempDir())
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	session, err := mgr.Open(context.Background(), project, project.Services[0], project.Services[0].Deployments[0], codedebug.OpenRequest{DeploymentID: "dep-api-dev"})
	require.NoError(t, err)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	_ = postJSONForTest[map[string]any](t, srv.URL+"/api/code-debug-sessions/"+session.ID+"/evaluate", map[string]any{
		"expression": "user.password",
		"frame_id":   1,
	}, http.StatusOK)
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.OperationCodeDebugEvaluate})
	require.NoError(t, err)
	require.NotEmpty(t, events)
	rawBytes, err := json.Marshal(events)
	require.NoError(t, err)
	raw := string(rawBytes)

	assert.Contains(t, raw, "expression_hash")
	assert.Contains(t, raw, "result_type")
	assert.NotContains(t, raw, "user.password")
}

func TestCodeDebugEvaluateAuditsCompositeSource(t *testing.T) {
	mgr := codeDebugManagerForAPITest()
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: mgr})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	settings, err := app.settings.Load()
	require.NoError(t, err)
	settings.Approval.CodeDebugEvaluate = false
	require.NoError(t, app.settings.Save(settings))
	project := codeDebugAPIProject(t.TempDir())
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	session, err := mgr.Open(context.Background(), project, project.Services[0], project.Services[0].Deployments[0], codedebug.OpenRequest{DeploymentID: "dep-api-dev"})
	require.NoError(t, err)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	_ = postJSONForTest[map[string]any](t, srv.URL+"/api/code-debug-sessions/"+session.ID+"/evaluate", map[string]any{
		"expression": "secret.Value()",
		"frame_id":   1,
		"source":     "debug_capture_at",
	}, http.StatusOK)
	events, err := app.operationAudit.List(context.Background(), operation.AuditFilter{Kind: operation.OperationCodeDebugEvaluate})
	require.NoError(t, err)
	require.NotEmpty(t, events)
	rawBytes, err := json.Marshal(events)
	require.NoError(t, err)
	raw := string(rawBytes)

	assert.Contains(t, raw, "debug_capture_at")
	assert.Contains(t, raw, "expression_hash")
	assert.NotContains(t, raw, "secret.Value")
}

func TestCodeDebugEvaluateRejectsEmptyExpression(t *testing.T) {
	mgr := codeDebugManagerForAPITest()
	app, err := NewApp(AppConfig{DataDir: t.TempDir(), CodeDebugManagerOverride: mgr})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := codeDebugAPIProject(t.TempDir())
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()
	session, err := mgr.Open(context.Background(), project, project.Services[0], project.Services[0].Deployments[0], codedebug.OpenRequest{DeploymentID: "dep-api-dev"})
	require.NoError(t, err)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	resp := postJSONForRawTest(t, srv.URL+"/api/code-debug-sessions/"+session.ID+"/evaluate", map[string]any{
		"expression": "   ",
		"frame_id":   1,
	}, http.StatusBadRequest)

	assert.Equal(t, "expression is required", resp["error"])
}

func TestWriteCodeDebugErrorIncludesAdapterRemediationData(t *testing.T) {
	rec := httptest.NewRecorder()
	err := codedebug.NewAdapterError(
		codedebug.CodeAdapterUnavailable,
		codedebug.AdapterCommand{Provider: model.CodeDebugProviderGo, Name: "dlv", Args: []string{"dap"}},
		errors.New("executable file not found"),
	)

	writeCodeDebugError(rec, err)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "adapter_unavailable", body["code"])
	assert.Equal(t, "go", body["provider"])
	assert.Equal(t, "dlv dap", body["command"])
	assert.NotEmpty(t, body["remediation_hint"])
}

func TestRuntimeStatusReportsDebugRunning(t *testing.T) {
	app, err := NewApp(AppConfig{
		DataDir:                  t.TempDir(),
		CodeDebugManagerOverride: codeDebugManagerForAPITest(),
		RuntimeMetricsSampler:    codeDebugRuntimeSampler{},
	})
	require.NoError(t, err)
	t.Cleanup(app.Close)
	project := codeDebugAPIProject(t.TempDir())
	project.Services[0].Deployments[0].CodeDebug.StartMode = model.CodeDebugStartModeDebug
	app.mu.Lock()
	app.appendProjectLocked(project)
	app.mu.Unlock()

	_, err = app.codeDebug.StartRuntime(context.Background(), project, project.Services[0], project.Services[0].Deployments[0], codedebug.OpenRequest{DeploymentID: "dep-api-dev"})
	require.NoError(t, err)

	got := app.runtimeStatusService().Snapshot(context.Background(), project)

	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	assert.Equal(t, model.HealthDebugRunning, got.Environments[0].Instances[0].Metrics.Health)
	assert.Equal(t, "debug", got.Environments[0].Instances[0].Metrics.Base)
}

func codeDebugManagerForAPITest() *codedebug.Manager {
	return codedebug.NewManager(codedebug.ManagerOptions{
		AdapterLaunch: func(context.Context, codedebug.AdapterCommand) (codedebug.AdapterProcess, error) {
			return codedebug.AdapterProcess{PID: 1, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (codedebug.DAP, error) { return &fakeCodeDebugDAP{}, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
}

func codeDebugAPIProject(root string) model.Project {
	return model.Project{
		ID: "p1", Name: "demo", RootPath: root,
		Environments: []model.Environment{{Name: "dev", IsDev: true}},
		Services: []model.Service{{
			ID: "svc-api", Name: "api",
			Deployments: []model.Deployment{{
				ID: "dep-api-dev", EnvName: "dev", Location: model.LocationLocal,
				Command: "go run ./cmd/api", WorkDir: root,
				CodeDebug: &model.CodeDebugConfig{Enabled: true, Provider: model.CodeDebugProviderGo, Program: "."},
			}},
		}},
	}
}

type codeDebugRuntimeSampler struct{}

func (codeDebugRuntimeSampler) Sample(context.Context, metrics.SampleTarget) (model.InstanceMetrics, error) {
	return model.InstanceMetrics{Health: model.HealthStopped, Base: "command"}, nil
}

type fakeCodeDebugDAP struct{}

func (f *fakeCodeDebugDAP) Initialize(context.Context) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *fakeCodeDebugDAP) Launch(context.Context, map[string]any) error { return nil }
func (f *fakeCodeDebugDAP) SetBreakpoints(context.Context, string, []int) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *fakeCodeDebugDAP) Continue(context.Context, int) error { return nil }
func (f *fakeCodeDebugDAP) Pause(context.Context, int) error    { return nil }
func (f *fakeCodeDebugDAP) Next(context.Context, int) error     { return nil }
func (f *fakeCodeDebugDAP) StepIn(context.Context, int) error   { return nil }
func (f *fakeCodeDebugDAP) StepOut(context.Context, int) error  { return nil }
func (f *fakeCodeDebugDAP) StackTrace(context.Context, int) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *fakeCodeDebugDAP) Scopes(context.Context, int) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *fakeCodeDebugDAP) Variables(context.Context, int) (map[string]any, error) {
	return map[string]any{}, nil
}
func (f *fakeCodeDebugDAP) Evaluate(context.Context, string, int) (map[string]any, error) {
	return map[string]any{"result": "ok"}, nil
}
func (f *fakeCodeDebugDAP) Disconnect(context.Context) error { return nil }
func (f *fakeCodeDebugDAP) WaitForStopped(context.Context) (map[string]any, error) {
	return map[string]any{"threadId": 1}, nil
}
func (f *fakeCodeDebugDAP) Close() error { return nil }
