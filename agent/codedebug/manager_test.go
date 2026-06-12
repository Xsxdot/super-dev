// manager_test.go 验证代码调试 session manager 的生命周期。
//
// 职责：
//   - 使用 fake launcher 和 fake DAP client 覆盖打开、关闭和复合采集流程
//   - 验证 manager 对 session 元数据和 adapter 清理的管理
//
// 边界：
//   - 不启动真实调试器或目标进程
package codedebug

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestManagerOpenCreatesSessionWithFakeLauncher(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			return &fakeDAP{}, nil
		},
		ReservePort: func() (int, error) { return 39001, nil },
		Now:         func() time.Time { return time.Unix(100, 0) },
	})
	project, service, dep := managerTestTarget(root)

	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Contains(t, session.ID, "cds_")
	assert.Equal(t, "dep-api-dev", session.DeploymentID)
	assert.Equal(t, 1234, session.ProcessID)
	assert.Equal(t, 39001, session.AdapterPort)
}

func TestManagerOpenSendsConfigurationDoneAfterLaunch(t *testing.T) {
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	_, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, 1, dap.configurationDoneCalls)
}

func TestManagerOpenConsumesInitialStopWhenStopOnEntry(t *testing.T) {
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	dep.CodeDebug.StopOnEntry = true

	_, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, 1, dap.waitForStoppedCalls)
}

func TestStartRuntimeOriginLaunched(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	rt, err := mgr.StartRuntime(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, "launched", rt.Origin)
}

func TestManagerCloseStopsAdapter(t *testing.T) {
	closed := false
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { closed = true; return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			return &fakeDAP{}, nil
		},
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	require.NoError(t, mgr.Close(session.ID, CloseRequest{StopRuntime: boolPtr(true)}))

	assert.True(t, closed)
}

func TestManagerCloseLeaseCanKeepDebugRuntime(t *testing.T) {
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 42, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	require.NoError(t, mgr.Close(session.ID, CloseRequest{StopRuntime: boolPtr(false)}))

	assert.Equal(t, 0, dap.disconnectCalls)
	runtime, ok := mgr.RuntimeStatus(dep.ID)
	require.True(t, ok)
	assert.True(t, runtime.Alive)
	assert.Equal(t, dep.ID, runtime.DeploymentID)
}

func TestManagerOpenReusesExistingDebugRuntime(t *testing.T) {
	launchCount := 0
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			launchCount++
			return AdapterProcess{PID: 40 + launchCount, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 41002 + launchCount, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	first, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)
	require.NoError(t, mgr.Close(first.ID, CloseRequest{StopRuntime: boolPtr(false)}))
	second, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	assert.Equal(t, 1, launchCount)
	assert.NotEqual(t, first.ID, second.ID)
}

func TestManagerSetBreakpointsRejectsOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(root)
	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	_, err = mgr.SetBreakpoints(context.Background(), session.ID, "../outside.go", []int{7})
	require.ErrorIs(t, err, ErrPathOutsideProject)

	_, err = mgr.SetBreakpoints(context.Background(), session.ID, "main.go", []int{9})
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "main.go"), dap.breakpointsSource)
}

func TestManagerCaptureAtRejectsBreakpointOutsideProjectRoot(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(root)
	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	_, err = mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "../outside.go",
		Line:      7,
		ThreadID:  1,
	})
	require.ErrorIs(t, err, ErrPathOutsideProject)
}

func TestManagerVariablesSanitizesSecretsAndLimitsLongValues(t *testing.T) {
	longValue := strings.Repeat("x", 2048)
	dap := &fakeDAP{
		variablesResult: map[string]any{
			"variables": []map[string]any{
				{"name": "API_TOKEN", "value": "super-secret-token-value"},
				{"name": "message", "value": longValue},
			},
		},
	}
	mgr, session := openManagerTestSession(t, t.TempDir(), dap)

	got, err := mgr.Variables(context.Background(), session.ID, 1)
	require.NoError(t, err)
	vars := asMapSlice(got["variables"])
	require.Len(t, vars, 2)
	assert.Equal(t, "[redacted]", vars[0]["value"])
	rendered := vars[1]["value"].(string)
	assert.NotEqual(t, longValue, rendered)
	assert.NotContains(t, rendered, longValue)
	assert.LessOrEqual(t, len(rendered), 1032)
}

func TestManagerVariablesRedactsSecretLookingVariableNames(t *testing.T) {
	dap := &fakeDAP{
		variablesResult: map[string]any{
			"variables": []map[string]any{
				{"name": "password", "value": "plain-text-secret"},
			},
		},
	}
	mgr, session := openManagerTestSession(t, t.TempDir(), dap)

	got, err := mgr.Variables(context.Background(), session.ID, 1)
	require.NoError(t, err)
	vars := asMapSlice(got["variables"])
	require.Len(t, vars, 1)
	assert.Equal(t, "[redacted]", vars[0]["name"])
	assert.Equal(t, "[redacted]", vars[0]["value"])
}

func TestManagerEvaluateSanitizesSecretLookingValues(t *testing.T) {
	dap := &fakeDAP{evaluateResult: map[string]any{"result": "sk-test-abcdefghijklmnopqrstuvwxyz123456"}}
	mgr, session := openManagerTestSession(t, t.TempDir(), dap)

	got, err := mgr.Evaluate(context.Background(), session.ID, "token", 1)
	require.NoError(t, err)

	assert.Equal(t, "[redacted]", got["result"])
}

func TestManagerInspectReturnsSanitizedVariables(t *testing.T) {
	dap := &fakeDAP{
		stackResult: map[string]any{
			"stackFrames": []map[string]any{{"id": 11}},
		},
		scopesResult: map[string]any{
			"scopes": []map[string]any{{"variablesReference": 7}},
		},
		variablesResult: map[string]any{
			"variables": []map[string]any{{"name": "password", "value": "plain-text-secret"}},
		},
	}
	mgr, session := openManagerTestSession(t, t.TempDir(), dap)

	got, err := mgr.Inspect(context.Background(), InspectRequest{SessionID: session.ID, ThreadID: 1})
	require.NoError(t, err)

	vars := asMapSlice(got["variables"])
	require.Len(t, vars, 1)
	assert.Equal(t, "[redacted]", vars[0]["value"])
}

func TestDefaultAdapterLaunchMissingCommandReturnsStableUnavailableError(t *testing.T) {
	_, err := defaultAdapterLaunch(context.Background(), AdapterCommand{
		Provider: model.CodeDebugProviderGo,
		Name:     "superdev-debug-adapter-definitely-missing",
	})

	require.ErrorIs(t, err, ErrAdapterUnavailable)
	info, ok := AdapterErrorDetails(err)
	require.True(t, ok)
	assert.Equal(t, CodeAdapterUnavailable, info.Code)
	assert.Equal(t, model.CodeDebugProviderGo, info.Provider)
	assert.Contains(t, info.Command, "superdev-debug-adapter-definitely-missing")
	assert.Contains(t, strings.ToLower(info.Hint), "install")
}

func TestDefaultAdapterLaunchKeepsProcessAfterCallerContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	process, err := defaultAdapterLaunch(ctx, AdapterCommand{
		Provider: model.CodeDebugProviderGo,
		Name:     "sleep",
		Args:     []string{"5"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = process.Close()
	})

	cancel()
	time.Sleep(100 * time.Millisecond)

	assert.NoError(t, signalProcessZero(process.PID))
}

func TestManagerOpenWrapsDialFailureAsStableConnectionError(t *testing.T) {
	root := t.TempDir()
	closed := false
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { closed = true; return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			return nil, errors.New("connection refused")
		},
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(root)

	_, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.Error(t, err)
	info, ok := AdapterErrorDetails(err)
	require.True(t, ok)
	assert.Equal(t, CodeDAPConnectionFailed, info.Code)
	assert.Equal(t, model.CodeDebugProviderGo, info.Provider)
	assert.Contains(t, info.Command, "dlv dap")
	assert.NotEmpty(t, info.Hint)
	assert.True(t, closed)
}

func TestManagerLaunchConfigInfersPythonProgramFromSimpleCommand(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.Command = "python ./app.py --port 8000"
	service.Language = model.LanguagePython
	dep.CodeDebug = &model.CodeDebugConfig{}

	cfg, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "app.py"), cfg.Program)
}

func TestManagerLaunchConfigInfersPythonProgramRelativeToWorkingDir(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.Command = "python app.py --port 8000"
	dep.WorkDir = filepath.Join(root, "server")
	service.Language = model.LanguagePython
	dep.CodeDebug = &model.CodeDebugConfig{}

	cfg, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "server", "app.py"), cfg.Program)
}

func TestManagerLaunchConfigInfersNodeProgramFromSimpleCommand(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.Command = "node server.js --watch"
	service.Language = model.LanguageNode
	dep.CodeDebug = &model.CodeDebugConfig{
		AdapterCommand: "node-debug-adapter",
	}

	cfg, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "server.js"), cfg.Program)
}

func TestManagerLaunchConfigUsesWorkingDirRelativeGoProgram(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.WorkDir = filepath.Join(root, "server")
	dep.Runtime = &model.RuntimeConfig{
		Type:       model.RuntimeTypeCommand,
		Command:    "go run ./cmd/server",
		WorkingDir: dep.WorkDir,
	}
	dep.CodeDebug = &model.CodeDebugConfig{
		Program: "server/cmd/server",
	}

	cfg, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, "./cmd/server", cfg.Program)
	assert.Equal(t, filepath.Join(root, "server"), cfg.WorkingDir)
}

func TestManagerLaunchConfigKeepsDefaultGoProgramRelative(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.CodeDebug.Program = ""

	cfg, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, ".", cfg.Program)
}

func openManagerTestSession(t *testing.T, root string, dap DAP) (*Manager, Session) {
	t.Helper()
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(root)
	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)
	return mgr, session
}

func managerTestTarget(root string) (model.Project, model.Service, model.Deployment) {
	dep := model.Deployment{
		ID:        "dep-api-dev",
		EnvName:   "dev",
		Location:  model.LocationLocal,
		Command:   "go run ./cmd/api",
		WorkDir:   root,
		CodeDebug: &model.CodeDebugConfig{Program: "."},
	}
	service := model.Service{ID: "svc-api", Name: "api", Language: model.LanguageGo, Deployments: []model.Deployment{dep}}
	project := model.Project{ID: "p1", Name: "demo", RootPath: root, Services: []model.Service{service}}
	return project, service, dep
}

type fakeDAP struct {
	mu                     sync.Mutex
	breakpointsSource      string
	stackResult            map[string]any
	scopesResult           map[string]any
	variablesResult        map[string]any
	evaluateResult         map[string]any
	subs                   []chan map[string]any
	disconnectCalls        int
	configurationDoneCalls int
	waitForStoppedCalls    int
}

func (f *fakeDAP) Initialize(context.Context) (map[string]any, error) { return map[string]any{}, nil }
func (f *fakeDAP) Launch(context.Context, map[string]any) error       { return nil }
func (f *fakeDAP) ConfigurationDone(context.Context) error {
	f.configurationDoneCalls++
	return nil
}
func (f *fakeDAP) SetBreakpoints(_ context.Context, source string, _ []int) (map[string]any, error) {
	f.breakpointsSource = source
	return map[string]any{}, nil
}
func (f *fakeDAP) Continue(context.Context, int) error { return nil }
func (f *fakeDAP) Pause(context.Context, int) error    { return nil }
func (f *fakeDAP) Next(context.Context, int) error     { return nil }
func (f *fakeDAP) StepIn(context.Context, int) error   { return nil }
func (f *fakeDAP) StepOut(context.Context, int) error  { return nil }
func (f *fakeDAP) StackTrace(context.Context, int) (map[string]any, error) {
	if f.stackResult != nil {
		return f.stackResult, nil
	}
	return map[string]any{}, nil
}
func (f *fakeDAP) Scopes(context.Context, int) (map[string]any, error) {
	if f.scopesResult != nil {
		return f.scopesResult, nil
	}
	return map[string]any{}, nil
}
func (f *fakeDAP) Variables(context.Context, int) (map[string]any, error) {
	if f.variablesResult != nil {
		return f.variablesResult, nil
	}
	return map[string]any{}, nil
}
func (f *fakeDAP) Evaluate(context.Context, string, int) (map[string]any, error) {
	if f.evaluateResult != nil {
		return f.evaluateResult, nil
	}
	return map[string]any{"result": "ok"}, nil
}
func (f *fakeDAP) Disconnect(context.Context) error {
	f.disconnectCalls++
	return nil
}
func (f *fakeDAP) WaitForStopped(context.Context) (map[string]any, error) {
	f.waitForStoppedCalls++
	return map[string]any{"threadId": 1}, nil
}
func (f *fakeDAP) Subscribe() (<-chan map[string]any, func()) {
	f.mu.Lock()
	ch := make(chan map[string]any, 16)
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			for i, sub := range f.subs {
				if sub == ch {
					f.subs = append(f.subs[:i], f.subs[i+1:]...)
					close(ch)
					break
				}
			}
			f.mu.Unlock()
		})
	}
	return ch, cancel
}
func (f *fakeDAP) Close() error { return nil }

func boolPtr(value bool) *bool {
	return &value
}

func signalProcessZero(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Signal(syscall.Signal(0))
}
