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

func TestManagerDebuggerSnapshotDefaultsAttached(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	runtime, err := mgr.StartRuntime(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	snap, ok := mgr.DebuggerSnapshot(runtime.DeploymentID)
	require.True(t, ok)
	assert.Equal(t, "attached", snap.State)
}

func TestManagerDebuggerSnapshotMissing(t *testing.T) {
	mgr := NewManager(ManagerOptions{})

	_, ok := mgr.DebuggerSnapshot("nonexistent")

	assert.False(t, ok)
}

func TestContinueRuntimeContinuesDeploymentDAP(t *testing.T) {
	dap := &fakeDAP{}
	mgr, session := openManagerTestSession(t, t.TempDir(), dap)

	err := mgr.ContinueRuntime(context.Background(), session.DeploymentID, 7)

	require.NoError(t, err)
	calls, threadID := dap.continueSnapshot()
	assert.Equal(t, 1, calls)
	assert.Equal(t, 7, threadID)
}

func TestContinueRuntimeMissingDeployment(t *testing.T) {
	mgr := NewManager(ManagerOptions{})

	err := mgr.ContinueRuntime(context.Background(), "missing", 1)

	require.ErrorIs(t, err, ErrSessionNotFound)
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

func TestResolveLeaseReusesActive(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 41003, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	first, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	got, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, got.ID)
}

func TestResolveLeaseCreatesWhenRuntimeRunningNoLease(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort: func() (int, error) { return 41004, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	_, err := mgr.StartRuntime(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	got, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, dep.ID, got.DeploymentID)
}

func TestResolveLeaseRuntimeNotRunning(t *testing.T) {
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(t.TempDir())

	_, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.ErrorIs(t, err, ErrRuntimeNotRunning)
}

func TestAttachRuntimeSendsAttachConfigurationDoneAndDetach(t *testing.T) {
	dap := &fakeDAP{}
	adapterClosed := false
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9001, Close: func() error {
				adapterClosed = true
				return nil
			}}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41005, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	dep.Command = "./server"

	rt, err := mgr.AttachRuntime(context.Background(), project, service, dep, attachTarget{processID: 4321})
	if err != nil {
		t.Fatal(err)
	}
	if rt.Origin != "attached" {
		t.Fatalf("attach runtime origin = %q, want attached", rt.Origin)
	}
	if rt.State != RuntimeStateDebugRunning {
		t.Fatalf("state = %q", rt.State)
	}
	assert.Equal(t, 1, dap.attachCalls)
	assert.Equal(t, 4321, dap.attachProcessID)
	assert.Equal(t, 1, dap.configurationDoneCalls)

	require.NoError(t, mgr.StopRuntime(dep.ID))
	assert.Equal(t, 1, dap.detachCalls)
	assert.Equal(t, 0, dap.disconnectCalls)
	assert.True(t, adapterClosed)
}

func TestResolveLeaseAttachesRunningService(t *testing.T) {
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9001, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41006, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "go"}, {pid: 4321, comm: "api"}}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	if err != nil {
		t.Fatalf("should attach running service: %v", err)
	}
	if !created {
		t.Fatal("attach should create a new lease")
	}
	// 此后该 deployment 应有 attached origin 的 runtime
	rt, ok := mgr.RuntimeStatus(dep.ID)
	if !ok || rt.Origin != "attached" {
		t.Fatalf("expected attached runtime after ResolveLease, got ok=%v origin=%q", ok, rt.Origin)
	}
	assert.Equal(t, 4321, rt.ProcessID)
}

func TestResolveLeaseAttachUnsupportedReportsError(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 4321, 4321, deploymentID == "dep-api-dev"
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguagePython

	_, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	if !errors.Is(err, ErrAttachUnsupported) {
		t.Fatalf("python running service should not silently launch; got %v", err)
	}
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
	// 断点路径会做 symlink 规范化以匹配 DWARF 真实路径，断言用规范化后的 root。
	wantRoot, _ := filepath.EvalSymlinks(root)
	assert.Equal(t, filepath.Join(wantRoot, "main.go"), dap.breakpointsSource)
}

// TestManagerSetBreakpointsResolvesAgainstWorkingDir 验证断点源码路径基于
// 已解析的工作目录（cwd），而不是项目根。language runtime 的 cwd 是子目录
// （如 ./server），断点源码相对 cwd，若用项目根会少算一层导致 dlv 找不到文件。
func TestManagerSetBreakpointsResolvesAgainstWorkingDir(t *testing.T) {
	root := t.TempDir()
	serverDir := filepath.Join(root, "server")
	require.NoError(t, os.MkdirAll(filepath.Join(serverDir, "cmd", "server"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "go.mod"), []byte("module demo\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(serverDir, "cmd", "server", "main.go"), []byte("package main\nfunc main() {}\n"), 0o644))

	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39002, nil },
	})
	project, service, dep := managerTestTarget(root)
	dep.Command = ""
	dep.WorkDir = ""
	dep.CodeDebug = nil
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    "./server",
		Config: map[string]any{"program": "./cmd/server"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}

	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	_, err = mgr.SetBreakpoints(context.Background(), session.ID, "cmd/server/main.go", []int{2})
	require.NoError(t, err)
	// 断点路径应基于 cwd（root/server），并对 symlink 规范化（macOS /tmp -> /private/tmp）。
	wantDir, _ := filepath.EvalSymlinks(serverDir)
	assert.Equal(t, filepath.Join(wantDir, "cmd", "server", "main.go"), dap.breakpointsSource)
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

func TestCaptureAtCoexistsWithPump(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	dap := &fakeDAP{
		waitForStoppedViaSubscribe: true,
		stackResult: map[string]any{
			"stackFrames": []map[string]any{{
				"id":     11,
				"line":   42,
				"source": map[string]any{"path": source},
			}},
		},
		scopesResult: map[string]any{
			"scopes": []map[string]any{{"variablesReference": 7}},
		},
		variablesResult: map[string]any{
			"variables": []map[string]any{{"name": "answer", "value": "42"}},
		},
	}
	mgr, session := openManagerTestSession(t, root, dap)
	resultCh := make(chan captureAtResult, 1)

	go func() {
		result, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
			SessionID: session.ID,
			Source:    "main.go",
			Line:      42,
			ThreadID:  1,
			Timeout:   2 * time.Second,
		})
		resultCh <- captureAtResult{result: result, err: err}
	}()

	waitForPump(t, func() bool {
		return dap.subscriberCount() >= 2
	})
	dap.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": float64(1)}})
	waitForPump(t, func() bool {
		calls, _ := dap.continueSnapshot()
		return calls >= 1 && dap.subscriberCount() >= 2
	})
	dap.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": float64(1)}})

	select {
	case got := <-resultCh:
		require.NoError(t, got.err)
		assert.Equal(t, float64(1), got.result["stopped"].(map[string]any)["threadId"])
	case <-time.After(2 * time.Second):
		t.Fatal("CaptureAt did not return")
	}

	snap, ok := mgr.DebuggerSnapshot(session.DeploymentID)
	require.True(t, ok)
	assert.Equal(t, "paused", snap.State)
	assert.Equal(t, 1, snap.ThreadID)
	assert.Equal(t, source, snap.Source)
	assert.Equal(t, 42, snap.Line)
}

func TestCaptureAtIgnoresAlreadyRunningContinueError(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	dap := &fakeDAP{
		continueErr:           errors.New("debuggee is running"),
		autoStoppedOnPause:    true,
		autoStoppedOnContinue: true,
		stackResult: map[string]any{
			"stackFrames": []map[string]any{{
				"id":     11,
				"line":   20,
				"source": map[string]any{"path": source},
			}},
		},
		scopesResult: map[string]any{
			"scopes": []map[string]any{{"variablesReference": 7}},
		},
		variablesResult: map[string]any{
			"variables": []map[string]any{{"name": "answer", "value": "42"}},
		},
	}
	mgr, session := openManagerTestSession(t, root, dap)

	result, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "main.go",
		Line:      20,
		ThreadID:  1,
	})

	require.NoError(t, err)
	wantRoot, _ := filepath.EvalSymlinks(root)
	assert.Equal(t, filepath.Join(wantRoot, "main.go"), dap.breakpointsSource)
	assert.Equal(t, 1, dap.pauseCalls)
	assert.Equal(t, 1, dap.continueCalls)
	assert.Equal(t, map[string]any{"threadId": 1}, result["stopped"])
}

func TestCaptureAtReportsUnverifiedBreakpoint(t *testing.T) {
	root := t.TempDir()
	dap := &fakeDAP{
		autoStoppedOnPause: true,
		breakpointsResult: map[string]any{
			"breakpoints": []map[string]any{{
				"verified": false,
				"line":     20,
				"message":  "no code at line",
			}},
		},
	}
	mgr, session := openManagerTestSession(t, root, dap)

	_, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "main.go",
		Line:      20,
		ThreadID:  1,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "breakpoint line 20 unverified")
	assert.Equal(t, 1, dap.continueCalls)
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

func TestLaunchConfigUsesLanguageRuntimePlan(t *testing.T) {
	mgr := NewManager(ManagerOptions{})
	project := model.Project{ID: "proj-lang", Name: "demo", RootPath: "/repo"}
	service := model.Service{ID: "svc-api", Name: "api", Language: model.LanguageGo}
	dep := model.Deployment{
		ID:          "dep-api-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    "./server",
			Env:    map[string]string{"ENABLE": "true"},
			Config: map[string]any{"program": "./cmd/server", "program_args": []any{"--port", "8080"}},
		},
		// 旧 override 字段即使被手写进配置也必须被忽略（同源原则）
		CodeDebug: &model.CodeDebugConfig{
			Program:    "./wrong",
			WorkingDir: "./wrong-dir",
			EnvVars:    map[string]string{"WRONG": "1"},
		},
	}

	cfg, provider, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.Equal(t, model.CodeDebugProviderGo, cfg.Provider)
	assert.Equal(t, "/repo/server/cmd/server", cfg.Program)
	assert.Equal(t, []string{"--port", "8080"}, cfg.Args)
	assert.Equal(t, "/repo/server", cfg.WorkingDir)
	assert.Equal(t, map[string]string{"ENABLE": "true"}, cfg.Env)
}

func TestAttachCommandHintLanguageRuntimeIsDirect(t *testing.T) {
	dep := model.Deployment{Runtime: &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		Config: map[string]any{"program": "./cmd/server"},
	}}
	// language runtime 经 build+exec，主进程即 debuggee，hint 为空 → 走直接可执行分支。
	assert.Equal(t, "", attachCommandHint(dep))

	flat := model.Deployment{Command: "./server"}
	assert.Equal(t, "./server", attachCommandHint(flat))
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
	mu                         sync.Mutex
	breakpointsSource          string
	breakpointsResult          map[string]any
	stackResult                map[string]any
	scopesResult               map[string]any
	variablesResult            map[string]any
	evaluateResult             map[string]any
	subs                       []chan map[string]any
	waitForStoppedViaSubscribe bool
	pauseCalls                 int
	pauseThreadID              int
	pauseErr                   error
	autoStoppedOnPause         bool
	continueCalls              int
	continueThreadID           int
	continueErr                error
	autoStoppedOnContinue      bool
	disconnectCalls            int
	detachCalls                int
	attachCalls                int
	attachProcessID            int
	configurationDoneCalls     int
	waitForStoppedCalls        int
}

func (f *fakeDAP) Initialize(context.Context) (map[string]any, error) { return map[string]any{}, nil }
func (f *fakeDAP) Launch(context.Context, map[string]any) error       { return nil }
func (f *fakeDAP) Attach(_ context.Context, args map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attachCalls++
	if pid, ok := args["processId"].(int); ok {
		f.attachProcessID = pid
	}
	return nil
}
func (f *fakeDAP) Detach(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detachCalls++
	return nil
}
func (f *fakeDAP) ConfigurationDone(context.Context) error {
	f.configurationDoneCalls++
	return nil
}
func (f *fakeDAP) SetBreakpoints(_ context.Context, source string, _ []int) (map[string]any, error) {
	f.breakpointsSource = source
	if f.breakpointsResult != nil {
		return f.breakpointsResult, nil
	}
	return map[string]any{}, nil
}
func (f *fakeDAP) Continue(_ context.Context, threadID int) error {
	f.mu.Lock()
	f.continueCalls++
	f.continueThreadID = threadID
	err := f.continueErr
	autoStopped := f.autoStoppedOnContinue
	f.mu.Unlock()
	if autoStopped {
		f.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": threadID}})
	}
	return err
}
func (f *fakeDAP) Pause(_ context.Context, threadID int) error {
	f.mu.Lock()
	f.pauseCalls++
	f.pauseThreadID = threadID
	err := f.pauseErr
	autoStopped := f.autoStoppedOnPause
	f.mu.Unlock()
	if autoStopped {
		f.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": threadID}})
	}
	return err
}
func (f *fakeDAP) Next(context.Context, int) error    { return nil }
func (f *fakeDAP) StepIn(context.Context, int) error  { return nil }
func (f *fakeDAP) StepOut(context.Context, int) error { return nil }
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
func (f *fakeDAP) WaitForStopped(ctx context.Context) (map[string]any, error) {
	f.mu.Lock()
	f.waitForStoppedCalls++
	viaSubscribe := f.waitForStoppedViaSubscribe
	f.mu.Unlock()
	if viaSubscribe {
		sub, cancel := f.Subscribe()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case event, ok := <-sub:
				if !ok {
					return nil, ErrSessionClosed
				}
				if body, ok := stoppedBody(event); ok {
					return body, nil
				}
			}
		}
	}
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
func (f *fakeDAP) emit(event map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sub := range f.subs {
		sub <- event
	}
}
func (f *fakeDAP) subscriberCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}
func (f *fakeDAP) continueSnapshot() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.continueCalls, f.continueThreadID
}
func (f *fakeDAP) Close() error { return nil }

type captureAtResult struct {
	result map[string]any
	err    error
}

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
