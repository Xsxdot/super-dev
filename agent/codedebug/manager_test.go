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
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/langruntime"
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

func TestManagerOpenCompletesWhenLaunchWaitsForConfigurationDone(t *testing.T) {
	dap := &fakeDAP{
		launchWaitsForConfigurationDone: true,
		emitInitializedOnLaunch:         true,
	}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39001, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := mgr.Open(ctx, project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, 1, dap.configurationDoneCalls)
}

func TestManagerOpenUsesJSDebugChildSessionFromLaunchStartDebugging(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.js"), []byte("let n = 0\nsetInterval(() => { n += 1 }, 500)\n"), 0o644))
	rootDAP := &fakeDAP{
		emitInitializedOnInitialize:     true,
		launchWaitsForConfigurationDone: true,
		emitStartDebuggingAfterLaunch:   true,
	}
	childDAP := &fakeDAP{emitInitializedOnInitialize: true, attachWaitsForConfigurationDone: true}
	dialCount := 0
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9006, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41012, nil },
	})
	project, service, dep := managerTestTarget(root)
	service.Language = model.LanguageNode
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "server.js"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}

	_, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, 2, dialCount)
	assert.True(t, rootDAP.respondedStartDebugging)
	assert.Equal(t, "pending-node-target", childDAP.launchPendingTargetID)
	mgr.mu.Lock()
	record := mgr.runtimes[dep.ID]
	mgr.mu.Unlock()
	require.NotNil(t, record)
	assert.Same(t, childDAP, record.dap)
}

func TestManagerOpenHandlesStartDebuggingBeforeRootLaunchResponse(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.js"), []byte("setInterval(() => {}, 500)\n"), 0o644))
	rootDAP := &fakeDAP{
		launchWaitsForConfigurationDone:        true,
		emitInitializedOnLaunch:                true,
		emitStartDebuggingBeforeLaunchResponse: true,
	}
	childDAP := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	dialCount := 0
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9007, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41013, nil },
	})
	project, service, dep := managerTestTarget(root)
	service.Language = model.LanguageNode
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "server.js"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := mgr.Open(ctx, project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.NoError(t, err)
	assert.Equal(t, 2, dialCount)
	assert.True(t, rootDAP.respondedStartDebugging)
	assert.Equal(t, "pending-node-target", childDAP.launchPendingTargetID)
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

func TestThreadStepActionsWaitForStoppedEvent(t *testing.T) {
	for _, action := range []string{"step_over", "step_in", "step_out"} {
		t.Run(action, func(t *testing.T) {
			dap := &fakeDAP{}
			mgr, session := openManagerTestSession(t, t.TempDir(), dap)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			type result struct {
				value map[string]any
				err   error
			}
			resultCh := make(chan result, 1)

			go func() {
				value, err := mgr.ThreadAction(ctx, session.ID, action, 7)
				resultCh <- result{value: value, err: err}
			}()

			deadline := time.After(500 * time.Millisecond)
			for dap.subscriberCount() < 2 {
				select {
				case got := <-resultCh:
					t.Fatalf("%s returned before the asynchronous stopped event: result=%v err=%v", action, got.value, got.err)
				case <-deadline:
					t.Fatalf("%s did not subscribe for the asynchronous stopped event", action)
				case <-time.After(time.Millisecond):
				}
			}

			dap.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": 7}})
			select {
			case got := <-resultCh:
				require.NoError(t, got.err)
				assert.Equal(t, action, got.value["action"])
				assert.Equal(t, 7, got.value["thread_id"])
			case <-time.After(500 * time.Millisecond):
				t.Fatalf("%s did not return after the asynchronous stopped event", action)
			}
		})
	}
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
		Dial:         func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort:  func() (int, error) { return 41002 + launchCount, nil },
		ProcessAlive: func(int) bool { return true },
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
		Dial:         func(context.Context, string, time.Duration) (DAP, error) { return &fakeDAP{}, nil },
		ReservePort:  func() (int, error) { return 41004, nil },
		ProcessAlive: func(int) bool { return true },
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

func TestAttachRuntimeCompletesWhenAttachWaitsForConfigurationDone(t *testing.T) {
	dap := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9002, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41006, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	dep.Command = "./server"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	rt, err := mgr.AttachRuntime(ctx, project, service, dep, attachTarget{processID: 4321})

	require.NoError(t, err)
	assert.Equal(t, "attached", rt.Origin)
	assert.Equal(t, 1, dap.attachCalls)
	assert.Equal(t, 1, dap.configurationDoneCalls)
}

func TestAttachRuntimeCompletesWhenConfigurationDoneResponseStaysPending(t *testing.T) {
	dap := &fakeDAP{
		attachWaitsForConfigurationDone:       true,
		emitInitializedOnAttach:               true,
		configurationDoneResponseStaysPending: true,
	}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9002, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41006, nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	dep.Command = "./server"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	rt, err := mgr.AttachRuntime(ctx, project, service, dep, attachTarget{processID: 4321})

	require.NoError(t, err)
	assert.Equal(t, "attached", rt.Origin)
	assert.Equal(t, 1, dap.attachCalls)
	assert.Equal(t, 1, dap.configurationDoneCalls)
}

func TestAttachRuntimeUsesJSDebugChildSessionFromStartDebugging(t *testing.T) {
	rootDAP := &fakeDAP{
		attachWaitsForConfigurationDone: true,
		emitInitializedOnAttach:         true,
		emitStartDebuggingAfterAttach:   true,
	}
	childDAP := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	dialCount := 0
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9003, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41007, nil },
	})
	root := t.TempDir()
	cfg := LaunchConfig{
		Target:     Target{ProjectID: "p1", RootPath: root, DeploymentID: "dep-node"},
		Provider:   model.CodeDebugProviderNode,
		WorkingDir: root,
		TargetPort: 9229,
	}
	provider := NewNodeProvider("/tmp/dapDebugServer.js")

	_, err := mgr.attachRuntimeWithConfig(context.Background(), cfg, provider, 1234, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, 1234)
	})

	require.NoError(t, err)
	assert.Equal(t, 2, dialCount)
	assert.True(t, rootDAP.respondedStartDebugging)
	assert.Equal(t, "pending-node-target", childDAP.attachPendingTargetID)
	mgr.mu.Lock()
	record := mgr.runtimes["dep-node"]
	mgr.mu.Unlock()
	require.NotNil(t, record)
	assert.Same(t, childDAP, record.dap)
}

func TestAttachRuntimeHandlesStartDebuggingBeforeRootAttachResponse(t *testing.T) {
	rootDAP := &fakeDAP{
		attachWaitsForConfigurationDone:        true,
		emitInitializedOnAttach:                true,
		emitStartDebuggingBeforeAttachResponse: true,
	}
	childDAP := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	dialCount := 0
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9004, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41008, nil },
	})
	root := t.TempDir()
	cfg := LaunchConfig{
		Target:     Target{ProjectID: "p1", RootPath: root, DeploymentID: "dep-node-early-child"},
		Provider:   model.CodeDebugProviderNode,
		WorkingDir: root,
		TargetPort: 9229,
	}
	provider := NewNodeProvider("/tmp/dapDebugServer.js")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := mgr.attachRuntimeWithConfig(ctx, cfg, provider, 1234, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, 1234)
	})

	require.NoError(t, err)
	assert.Equal(t, 2, dialCount)
	assert.True(t, rootDAP.respondedStartDebugging)
	assert.Equal(t, "pending-node-target", childDAP.attachPendingTargetID)
}

func TestAttachRuntimePreservesInitializedEventEmittedDuringInitialize(t *testing.T) {
	rootDAP := &fakeDAP{
		emitInitializedOnInitialize:     true,
		attachWaitsForConfigurationDone: true,
		emitStartDebuggingAfterAttach:   true,
	}
	childDAP := &fakeDAP{
		emitInitializedOnInitialize:     true,
		attachWaitsForConfigurationDone: true,
	}
	dialCount := 0
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9005, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41009, nil },
	})
	root := t.TempDir()
	cfg := LaunchConfig{
		Target:     Target{ProjectID: "p1", RootPath: root, DeploymentID: "dep-node-initialize-event"},
		Provider:   model.CodeDebugProviderNode,
		WorkingDir: root,
		TargetPort: 9229,
	}
	provider := NewNodeProvider("/tmp/dapDebugServer.js")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := mgr.attachRuntimeWithConfig(ctx, cfg, provider, 1234, func(cfg LaunchConfig) map[string]any {
		return provider.AttachArguments(cfg, 1234)
	})

	require.NoError(t, err)
	assert.Equal(t, 2, dialCount)
	assert.Equal(t, 1, rootDAP.configurationDoneCalls)
	assert.Equal(t, 1, childDAP.configurationDoneCalls)
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
		ProcessAlive: func(int) bool { return true },
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
	assert.Equal(t, 100, rt.ProcessID)
}

func TestTerminatedEventRetiresRuntimeAndSessions(t *testing.T) {
	dap := &fakeDAP{}
	adapterClosed := make(chan struct{})
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9001, Close: func() error { close(adapterClosed); return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41100, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		ProcessAlive: func(int) bool { return true },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	session, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	require.NoError(t, err)

	// debuggee 退出（如服务被 restart）：adapter 发 terminated 事件。
	// runtime 必须被反向失效并摘除，否则死 runtime 会被后续调试请求永久复用。
	dap.emit(map[string]any{"event": "terminated"})

	require.Eventually(t, func() bool {
		_, ok := mgr.RuntimeStatus(dep.ID)
		return !ok
	}, 2*time.Second, 10*time.Millisecond, "runtime should be retired after terminated event")
	select {
	case <-adapterClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter process should be closed on retire")
	}
	got, ok := mgr.Status(session.ID)
	require.True(t, ok, "session should remain queryable after retire")
	assert.False(t, got.Alive, "session should be closed after runtime retired")
}

func TestResolveLeaseRetiresDeadRuntimeAndReattaches(t *testing.T) {
	dap := &fakeDAP{}
	debuggeeAlive := true
	adapterCloses := 0
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9001, Close: func() error { adapterCloses++; return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41101, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		ProcessAlive: func(pid int) bool { return debuggeeAlive },
	})
	project, service, dep := managerTestTarget(t.TempDir())

	session, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	require.NoError(t, err)
	require.Equal(t, 1, dap.attachCalls)
	// 释放 lease 但保留 runtime（模拟一次 capture 结束后的常态）
	require.NoError(t, mgr.Close(session.ID, CloseRequest{}))

	// debuggee 死亡但 terminated 事件丢失（如 agent 错过事件/断连）：
	// 复用前的 liveness 兜底必须发现进程已死，retire 后重新 attach。
	debuggeeAlive = false

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 2, dap.attachCalls, "dead runtime must be retired and a fresh attach performed")
	assert.GreaterOrEqual(t, adapterCloses, 1, "stale adapter should be closed on retire")
}

func TestResolveLeaseNativeAttachCarriesProgram(t *testing.T) {
	root := t.TempDir()
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9002, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41009, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 4321, 4321, deploymentID == "dep-rust-dev"
		},
	})
	dep := model.Deployment{
		ID:          "dep-rust-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "target/debug/app"},
		},
	}
	service := model.Service{ID: "rust", Name: "rust", Language: model.LanguageRust, Deployments: []model.Deployment{dep}}
	project := model.Project{ID: "p1", Name: "native", RootPath: root, Services: []model.Service{service}}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 4321, dap.attachProcessID)
	assert.Equal(t, filepath.Join(root, "target/debug/app"), dap.attachProgram)
}

func TestAwaitAttachResultBeforeConfigurationReturnsReadyError(t *testing.T) {
	ch := make(chan error, 1)
	ch <- errors.New("dap attach failed: no such process")

	err, done := awaitAttachResultBeforeConfiguration(context.Background(), ch)

	assert.True(t, done)
	assert.EqualError(t, err, "dap attach failed: no such process")
}

func TestResolveLeaseNodeSignalsResolvedChildBeforeAttach(t *testing.T) {
	dap := &fakeDAP{}
	var signaled struct {
		pid int
		sig string
	}
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9002, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41009, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "pnpm"}, {pid: 101, comm: "node"}}
		},
		SignalProcess: func(pid int, sig string) error {
			signaled.pid = pid
			signaled.sig = sig
			return nil
		},
		RunningProcessStderr: func(deploymentID string) []string {
			return []string{"Debugger listening on ws://127.0.0.1:9229/uuid"}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageNode
	dep.Command = ""
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"package_manager": "pnpm", "script": "worker"},
	}
	dep.CodeDebug = &model.CodeDebugConfig{AdapterCommand: "node-debug-adapter"}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 101, signaled.pid)
	assert.Equal(t, "SIGUSR1", signaled.sig)
	assert.Equal(t, 9229, dap.attachConnectPort)
	rt, ok := mgr.RuntimeStatus(dep.ID)
	require.True(t, ok)
	assert.Equal(t, 101, rt.ProcessID)
}

func TestResolveLeaseNodeScriptRuntimeAttachesWithoutProgram(t *testing.T) {
	rootDAP := &fakeDAP{
		attachWaitsForConfigurationDone: true,
		emitInitializedOnAttach:         true,
		emitStartDebuggingAfterAttach:   true,
	}
	childDAP := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	dialCount := 0
	var signaled struct {
		pid int
		sig string
	}
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9004, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41010, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "pnpm"}, {pid: 101, comm: "node"}}
		},
		SignalProcess: func(pid int, sig string) error {
			signaled.pid = pid
			signaled.sig = sig
			return nil
		},
		RunningProcessStderr: func(deploymentID string) []string {
			return []string{"Debugger listening on ws://127.0.0.1:9229/uuid"}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageNode
	dep.Command = ""
	dep.CodeDebug = nil
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"package_manager": "pnpm", "script": "dev"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 101, signaled.pid)
	assert.Equal(t, "SIGUSR1", signaled.sig)
	assert.Equal(t, 9229, rootDAP.attachConnectPort)
	assert.Equal(t, "pending-node-target", childDAP.attachPendingTargetID)
	rt, ok := mgr.RuntimeStatus(dep.ID)
	require.True(t, ok)
	assert.Equal(t, "attached", rt.Origin)
}

func TestResolveLeaseNodeScriptRuntimeSkipsNodePackageManagerWrapper(t *testing.T) {
	rootDAP := &fakeDAP{
		attachWaitsForConfigurationDone: true,
		emitInitializedOnAttach:         true,
		emitStartDebuggingAfterAttach:   true,
	}
	childDAP := &fakeDAP{attachWaitsForConfigurationDone: true, emitInitializedOnAttach: true}
	dialCount := 0
	var signaled struct {
		pid int
		sig string
	}
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9005, Close: func() error { return nil }}, nil
		},
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCount++
			if dialCount == 1 {
				return rootDAP, nil
			}
			return childDAP, nil
		},
		ReservePort: func() (int, error) { return 41011, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "node"}, {pid: 101, comm: "node"}}
		},
		SignalProcess: func(pid int, sig string) error {
			signaled.pid = pid
			signaled.sig = sig
			return nil
		},
		RunningProcessStderr: func(deploymentID string) []string {
			return []string{"Debugger listening on ws://127.0.0.1:9229/uuid"}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageNode
	dep.Command = ""
	dep.CodeDebug = nil
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"package_manager": "pnpm", "script": "dev"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 101, signaled.pid)
	assert.Equal(t, "SIGUSR1", signaled.sig)
}

func TestResolveLeaseNodeParsesInspectorPortAfterSignal(t *testing.T) {
	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		JSDebugServerPath: "/data/js-debug/src/dapDebugServer.js",
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9100, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41020, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "node"}}
		},
		SignalProcess: func(int, string) error { return nil },
		RunningProcessStderr: func(deploymentID string) []string {
			return []string{"node tick 1", "Debugger listening on ws://127.0.0.1:9229/uuid"}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageNode
	dep.Command = ""
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "server.js"},
	}
	dep.CodeDebug = &model.CodeDebugConfig{AdapterCommand: "node-debug-adapter"}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, 9229, dap.attachConnectPort)
}

func TestResolveLeaseNodeNoInspectorPortReportsError(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		listProcessGroup: func(pgid int) []procInfo {
			return []procInfo{{pid: 100, comm: "node"}}
		},
		SignalProcess:        func(int, string) error { return nil },
		RunningProcessStderr: func(deploymentID string) []string { return []string{"node tick 1"} },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageNode
	dep.Command = ""
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "server.js"},
	}
	dep.CodeDebug = &model.CodeDebugConfig{}

	_, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	if !errors.Is(err, ErrAttachTargetUnresolved) {
		t.Fatalf("node without inspector port should report unresolved, got %v", err)
	}
}

func TestResolveLeasePythonConnectsListenPortFromArgv(t *testing.T) {
	dap := &fakeDAP{}
	signalCalled := false
	adapterLaunched := false
	var dialAddr string
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			adapterLaunched = true
			return AdapterProcess{PID: 9003, Close: func() error { return nil }}, nil
		},
		Dial: func(_ context.Context, addr string, _ time.Duration) (DAP, error) {
			dialAddr = addr
			return dap, nil
		},
		ReservePort: func() (int, error) { return 41011, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 100, 100, deploymentID == "dep-api-dev"
		},
		// prearm 进程的 argv 自带 --listen 端口；attach 时从 argv 反解，不需独立存储。
		RunningProcessArgv: func(deploymentID string) []string {
			return []string{"python", "-m", "debugpy", "--listen", "127.0.0.1:5678", "app.py"}
		},
		SignalProcess: func(int, string) error { signalCalled = true; return nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguagePython
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "app.py"},
	}
	dep.CodeDebug = &model.CodeDebugConfig{}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.False(t, signalCalled, "prearm should connect to listen port, not signal")
	// debugpy --listen 端口本身即 DAP 服务：直连该端口，不另起 adapter，attach 不带 connect。
	assert.False(t, adapterLaunched, "python prearm must not spawn a separate adapter")
	assert.Equal(t, "127.0.0.1:5678", dialAddr, "must dial the debuggee listen port directly")
	assert.Equal(t, 0, dap.attachConnectPort, "direct-dap attach must not carry connect port")
}

func TestResolveLeasePythonMissingListenPortReportsError(t *testing.T) {
	mgr := NewManager(ManagerOptions{
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 4321, 4321, deploymentID == "dep-api-dev"
		},
		// 普通启动（start_normal，无 --listen）的 Python 进程不可 attach，不应静默 launch。
		RunningProcessArgv: func(deploymentID string) []string {
			return []string{"python", "app.py"}
		},
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguagePython

	_, _, err := mgr.ResolveLease(context.Background(), project, service, dep, "")
	if !errors.Is(err, ErrAttachUnsupported) {
		t.Fatalf("python without listen port should report unsupported, not silently launch; got %v", err)
	}
}

func TestResolveLeaseJVMConnectsJDWPListenPortFromArgv(t *testing.T) {
	dap := &fakeDAP{}
	signalCalled := false
	var launched AdapterCommand
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(_ context.Context, cmd AdapterCommand) (AdapterProcess, error) {
			launched = cmd
			return AdapterProcess{PID: 9004, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41012, nil },
		RunningProcess: func(deploymentID string) (int, int, bool) {
			return 1234, 1234, deploymentID == "dep-api-dev"
		},
		// JVM start_dev embeds the allocated JDWP port in argv; attach must reuse that
		// prearmed port instead of treating JVM like a plain PID attach target.
		RunningProcessArgv: func(deploymentID string) []string {
			return []string{"java", "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5005", "-cp", "build/classes", "App"}
		},
		SignalProcess: func(int, string) error { signalCalled = true; return nil },
	})
	project, service, dep := managerTestTarget(t.TempDir())
	service.Language = model.LanguageJava
	dep.Runtime = &model.RuntimeConfig{
		Type: model.RuntimeTypeLanguage,
		CWD:  ".",
		Config: map[string]any{
			"program":   "App",
			"classpath": "build/classes",
		},
	}
	dep.CodeDebug = &model.CodeDebugConfig{AdapterCommand: "jvm-debug-wrapper"}

	_, created, err := mgr.ResolveLease(context.Background(), project, service, dep, "")

	require.NoError(t, err)
	assert.True(t, created)
	assert.False(t, signalCalled, "JVM prearm should not signal the process")
	assert.Equal(t, model.CodeDebugProviderJVM, launched.Provider)
	assert.Equal(t, "jvm-debug-wrapper", launched.Name)
	assert.Equal(t, 5005, dap.attachConnectPort, "JVM attach should connect adapter to the JDWP port")
}

func TestParseListenPort(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want int
	}{
		{"space form host:port", []string{"python", "-m", "debugpy", "--listen", "127.0.0.1:5678", "app.py"}, 5678},
		{"equals form host:port", []string{"python", "--listen=127.0.0.1:9001", "app.py"}, 9001},
		{"jdwp agentlib address", []string{"java", "-agentlib:jdwp=transport=dt_socket,server=y,suspend=n,address=127.0.0.1:5005", "App"}, 5005},
		{"port only", []string{"python", "--listen", "5678"}, 5678},
		{"no listen", []string{"python", "app.py"}, 0},
		{"listen without value", []string{"python", "--listen"}, 0},
		{"nil argv", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseListenPort(tc.argv))
		})
	}
}

func TestParseInspectPort(t *testing.T) {
	cases := []struct {
		name        string
		argv        []string
		wantPort    int
		wantPresent bool
	}{
		{"equals port", []string{"node", "--inspect=12345", "server.js"}, 12345, true},
		{"equals host port", []string{"node", "--inspect=127.0.0.1:9230", "server.js"}, 9230, true},
		{"space port", []string{"node", "--inspect", "9231", "server.js"}, 9231, true},
		{"default port", []string{"node", "--inspect", "server.js"}, defaultNodeInspectorPort, true},
		{"inspect zero", []string{"node", "--inspect=0", "server.js"}, 0, true},
		{"inspect brk", []string{"node", "--inspect-brk=9232", "server.js"}, 9232, true},
		{"missing", []string{"node", "server.js"}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotPort, gotPresent := parseInspectPort(tc.argv)
			assert.Equal(t, tc.wantPort, gotPort)
			assert.Equal(t, tc.wantPresent, gotPresent)
		})
	}
}

func TestWaitInspectorPortFallsBackToOpenedDefaultPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	mgr := NewManager(ManagerOptions{
		RunningProcessStderr: func(string) []string { return nil },
	})

	got, err := mgr.waitInspectorPort("dep-node", nodeInspectorFallback{port: port, enabled: true})

	require.NoError(t, err)
	assert.Equal(t, port, got)
}

func TestManagerSignalReadinessSendsSignalBeforeAttach(t *testing.T) {
	dap := &fakeDAP{}
	var signaled struct {
		pid int
		sig string
	}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 9100, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 41007, nil },
		SignalProcess: func(pid int, sig string) error {
			signaled.pid = pid
			signaled.sig = sig
			return nil
		},
	})

	_, err := mgr.attachWithReadiness(context.Background(), readinessRequest{
		cfg: LaunchConfig{
			Target:     Target{ProjectID: "p1", DeploymentID: "dep-go", RootPath: "/repo"},
			Provider:   model.CodeDebugProviderGo,
			WorkingDir: "/repo",
		},
		provider:  NewGoProvider(),
		readiness: langruntime.ReadinessSignalAttach,
		signal:    "SIGUSR1",
		pid:       4321,
	})

	require.NoError(t, err)
	assert.Equal(t, 4321, signaled.pid)
	assert.Equal(t, "SIGUSR1", signaled.sig)
	assert.Equal(t, 4321, dap.attachProcessID)
}

func TestManagerPrearmReadinessConnectsPortNoSignal(t *testing.T) {
	dap := &fakeDAP{}
	signalCalled := false
	adapterLaunched := false
	var dialAddr string
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			adapterLaunched = true
			return AdapterProcess{PID: 9101, Close: func() error { return nil }}, nil
		},
		Dial: func(_ context.Context, addr string, _ time.Duration) (DAP, error) {
			dialAddr = addr
			return dap, nil
		},
		ReservePort: func() (int, error) { return 41008, nil },
		SignalProcess: func(int, string) error {
			signalCalled = true
			return nil
		},
	})

	_, err := mgr.attachWithReadiness(context.Background(), readinessRequest{
		cfg: LaunchConfig{
			Target:     Target{ProjectID: "p1", DeploymentID: "dep-python", RootPath: "/repo"},
			Provider:   model.CodeDebugProviderPython,
			WorkingDir: "/repo",
		},
		provider:  NewPythonProvider("python"),
		readiness: langruntime.ReadinessPrearmListen,
		port:      5678,
	})

	require.NoError(t, err)
	assert.False(t, signalCalled, "prearm should connect to listen port, not signal")
	// 直连 debugpy --listen 端口，不另起 adapter，attach 不带 connect。
	assert.False(t, adapterLaunched, "python prearm must not spawn a separate adapter")
	assert.Equal(t, "127.0.0.1:5678", dialAddr, "must dial the debuggee listen port directly")
	assert.Equal(t, 0, dap.attachConnectPort, "direct-dap attach must not carry connect port")
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

func TestSetBreakpointsReportsResolvedSourcePath(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "server.js"), []byte("let n=0\n"), 0o644))

	dap := &fakeDAP{}
	mgr := NewManager(ManagerOptions{
		AdapterLaunch: func(context.Context, AdapterCommand) (AdapterProcess, error) {
			return AdapterProcess{PID: 1234, Close: func() error { return nil }}, nil
		},
		Dial:        func(context.Context, string, time.Duration) (DAP, error) { return dap, nil },
		ReservePort: func() (int, error) { return 39003, nil },
	})
	project, service, dep := managerTestTarget(root)
	dep.Command = ""
	dep.WorkDir = ""
	dep.CodeDebug = nil
	dep.Runtime = &model.RuntimeConfig{
		Type:   model.RuntimeTypeLanguage,
		CWD:    ".",
		Config: map[string]any{"program": "./server.js"},
	}
	service.Deployments = []model.Deployment{dep}
	project.Services = []model.Service{service}

	session, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})
	require.NoError(t, err)

	body, err := mgr.SetBreakpoints(context.Background(), session.ID, "server.js", []int{1})
	require.NoError(t, err)
	got, _ := body["resolved_source"].(string)
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "server.js"))
	assert.Equal(t, want, got)
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

func TestCaptureAtClearsBreakpointsAfterCapture(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "main.go")
	dap := &fakeDAP{
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

	_, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "main.go",
		Line:      20,
		ThreadID:  1,
	})
	require.NoError(t, err)

	// 采集完成后必须清掉本次断点：残留断点会让后续命中它的请求把 debuggee
	// 永久挂起（实测服务 /health 冻结），且没有任何人来 continue。
	history := dap.breakpointsHistory()
	require.NotEmpty(t, history)
	assert.Equal(t, []int{20}, history[0])
	assert.Empty(t, history[len(history)-1], "capture should clear breakpoints for the source before returning")
	require.GreaterOrEqual(t, len(history), 2, "expected a second SetBreakpoints call clearing the lines")
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

func TestCaptureAtNodeSkipsInitialPauseAndWaitsForBreakpoint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "server.js")
	dap := &fakeDAP{
		autoStoppedOnSetBreakpoints: true,
		setBreakpointsThreadID:      0,
		stackResult: map[string]any{
			"stackFrames": []map[string]any{
				{
					"id":     0,
					"line":   5,
					"source": map[string]any{"path": source},
				},
				{
					"id":     1,
					"line":   507,
					"source": map[string]any{"path": "<node_internals>/events"},
				},
			},
		},
		scopesResult: map[string]any{
			"scopes": []map[string]any{{"variablesReference": 7}},
		},
		variablesResult: map[string]any{
			"variables": []map[string]any{{"name": "marker", "value": "node tick 1"}},
		},
	}
	mgr, session := openManagerTestSession(t, root, dap)
	mgr.mu.Lock()
	mgr.runtimes[session.DeploymentID].Provider = model.CodeDebugProviderNode
	mgr.sessions[session.ID].Provider = model.CodeDebugProviderNode
	mgr.mu.Unlock()

	result, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "server.js",
		Line:      5,
		ThreadID:  1,
		Timeout:   time.Second,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, dap.pauseCalls)
	assert.Equal(t, 0, dap.continueCalls)
	assert.Equal(t, 0, result["frame_id"], "js-debug 的首帧 0 是合法 DAP identity")
	assert.Equal(t, map[string]any{"threadId": 0}, result["stopped"])
}

func TestCaptureAtJVMSkipsThreadlessPauseAndWaitsForBreakpoint(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "FixtureServer.kt")
	dap := &fakeDAP{
		autoStoppedOnSetBreakpoints: true,
		setBreakpointsThreadID:      17,
		stackResult: map[string]any{
			"stackFrames": []map[string]any{{
				"id":     11,
				"line":   25,
				"source": map[string]any{"path": source},
			}},
		},
		scopesResult: map[string]any{
			"scopes": []map[string]any{{"variablesReference": 7}},
		},
		variablesResult: map[string]any{
			"variables": []map[string]any{{"name": "fixtureMarker", "value": "breakpoint-visible"}},
		},
	}
	mgr, session := openManagerTestSession(t, root, dap)
	mgr.mu.Lock()
	mgr.runtimes[session.DeploymentID].Provider = model.CodeDebugProviderJVM
	mgr.sessions[session.ID].Provider = model.CodeDebugProviderJVM
	mgr.mu.Unlock()

	result, err := mgr.CaptureAt(context.Background(), CaptureAtRequest{
		SessionID: session.ID,
		Source:    "FixtureServer.kt",
		Line:      25,
		ThreadID:  0,
		Timeout:   time.Second,
	})

	require.NoError(t, err)
	assert.Equal(t, 0, dap.pauseCalls)
	assert.Equal(t, 0, dap.continueCalls)
	assert.Equal(t, map[string]any{"threadId": 17}, result["stopped"])
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

func TestManagerExplicitAdapterStartFailureDoesNotFallBack(t *testing.T) {
	root := t.TempDir()
	explicit := filepath.Join(root, "Program Files", "Delve", "dlv.exe")
	dialCalled := false
	mgr := NewManager(ManagerOptions{
		ReservePort: func() (int, error) { return 39001, nil },
		Dial: func(context.Context, string, time.Duration) (DAP, error) {
			dialCalled = true
			return nil, errors.New("dial must not run after adapter start failure")
		},
	})
	project, service, dep := managerTestTarget(root)
	dep.CodeDebug.AdapterCommand = explicit

	_, err := mgr.Open(context.Background(), project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.ErrorIs(t, err, ErrAdapterUnavailable)
	info, ok := AdapterErrorDetails(err)
	require.True(t, ok)
	assert.Equal(t, CodeAdapterUnavailable, info.Code)
	assert.Equal(t, AdapterCauseNotFound, info.CauseCode)
	assert.Equal(t, AdapterCommandSourceExplicit, info.Source)
	assert.Equal(t, "dlv.exe", info.Executable)
	assert.NotContains(t, info.Command, root, "error context must not leak the absolute install path")
	assert.False(t, dialCalled, "an explicit launch failure must stop instead of falling through to another candidate")
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

func TestCloseAdapterProcessTreatsNaturalExitAsAlreadyClosed(t *testing.T) {
	waitDone := make(chan struct{})
	close(waitDone)
	killCalls := 0

	err := closeAdapterProcess(waitDone, func() error {
		killCalls++
		return errors.New("TerminateProcess: Access is denied")
	})

	require.NoError(t, err)
	assert.Zero(t, killCalls, "an already-reaped adapter must not be killed by a reusable PID")
}

func TestCloseAdapterProcessAcceptsExitRacingWithKillError(t *testing.T) {
	waitDone := make(chan struct{})

	err := closeAdapterProcess(waitDone, func() error {
		close(waitDone)
		return errors.New("TerminateProcess: Access is denied")
	})

	require.NoError(t, err)
}

func TestCloseAdapterProcessPreservesLiveKillError(t *testing.T) {
	waitDone := make(chan struct{})
	want := errors.New("live adapter termination denied")

	err := closeAdapterProcess(waitDone, func() error { return want })

	require.ErrorIs(t, err, want)
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

func TestManagerLaunchConfigRejectsLegacyPythonCommandRuntime(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	service.Language = model.LanguagePython
	dep.Runtime = &model.RuntimeConfig{Type: model.RuntimeTypeCommand, Command: "python ./app.py --port 8000"}

	_, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.ErrorIs(t, err, ErrTargetUnsupported)
}

func TestManagerLaunchConfigRejectsLegacyCommandWorkingDir(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	service.Language = model.LanguagePython
	dep.Runtime = &model.RuntimeConfig{
		Type:       model.RuntimeTypeCommand,
		Command:    "python app.py --port 8000",
		WorkingDir: filepath.Join(root, "server"),
	}

	_, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.ErrorIs(t, err, ErrTargetUnsupported)
}

func TestManagerLaunchConfigRejectsLegacyNodeCommandRuntime(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	service.Language = model.LanguageNode
	dep.Runtime = &model.RuntimeConfig{Type: model.RuntimeTypeCommand, Command: "node server.js --watch"}
	dep.CodeDebug = &model.CodeDebugConfig{AdapterCommand: "node-debug-adapter"}

	_, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.ErrorIs(t, err, ErrTargetUnsupported)
}

func TestManagerLaunchConfigRejectsLegacyGoCommandRuntime(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(ManagerOptions{})
	project, service, dep := managerTestTarget(root)
	dep.Runtime = &model.RuntimeConfig{
		Type:       model.RuntimeTypeCommand,
		Command:    "go run ./cmd/server",
		WorkingDir: filepath.Join(root, "server"),
	}

	_, _, err := mgr.launchConfig(project, service, dep, OpenRequest{DeploymentID: dep.ID})

	require.ErrorIs(t, err, ErrTargetUnsupported)
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
		// code_debug 只保留调试策略和 adapter override，启动入口由 runtime.config 提供。
		CodeDebug: &model.CodeDebugConfig{
			AdapterCommand: "dlv",
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
	assert.Equal(t, "dlv", cfg.AdapterCommand)
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
		ID:          "dep-api-dev",
		EnvName:     "dev",
		Location:    model.LocationLocal,
		ControlMode: model.ControlModeManaged,
		Runtime: &model.RuntimeConfig{
			Type:   model.RuntimeTypeLanguage,
			CWD:    ".",
			Config: map[string]any{"program": "."},
		},
		CodeDebug: &model.CodeDebugConfig{},
	}
	service := model.Service{ID: "svc-api", Name: "api", Language: model.LanguageGo, Deployments: []model.Deployment{dep}}
	project := model.Project{ID: "p1", Name: "demo", RootPath: root, Services: []model.Service{service}}
	return project, service, dep
}

type fakeDAP struct {
	mu                                     sync.Mutex
	emitInitializedOnInitialize            bool
	breakpointsSource                      string
	breakpointsLines                       [][]int
	breakpointsResult                      map[string]any
	stackResult                            map[string]any
	scopesResult                           map[string]any
	variablesResult                        map[string]any
	evaluateResult                         map[string]any
	subs                                   []chan map[string]any
	waitForStoppedViaSubscribe             bool
	pauseCalls                             int
	pauseThreadID                          int
	pauseErr                               error
	autoStoppedOnPause                     bool
	continueCalls                          int
	continueThreadID                       int
	continueErr                            error
	autoStoppedOnContinue                  bool
	autoStoppedOnSetBreakpoints            bool
	setBreakpointsThreadID                 int
	disconnectCalls                        int
	detachCalls                            int
	attachCalls                            int
	attachProcessID                        int
	attachConnectPort                      int
	attachProgram                          string
	attachPendingTargetID                  string
	launchCalls                            int
	launchPendingTargetID                  string
	launchWaitsForConfigurationDone        bool
	emitInitializedOnLaunch                bool
	emitStartDebuggingAfterLaunch          bool
	emitStartDebuggingBeforeLaunchResponse bool
	attachWaitsForConfigurationDone        bool
	emitInitializedOnAttach                bool
	emitStartDebuggingAfterAttach          bool
	emitStartDebuggingBeforeAttachResponse bool
	requestSubs                            []chan map[string]any
	respondedStartDebugging                bool
	startDebuggingResponseCh               chan struct{}
	startDebuggingResponseClosed           bool
	configurationDoneCh                    chan struct{}
	configurationDoneClosed                bool
	configurationDoneCalls                 int
	configurationDoneResponseStaysPending  bool
	waitForStoppedCalls                    int
}

func (f *fakeDAP) Initialize(context.Context) (map[string]any, error) {
	if f.emitInitializedOnInitialize {
		f.emit(map[string]any{"event": "initialized", "body": map[string]any{}})
	}
	return map[string]any{}, nil
}
func (f *fakeDAP) Launch(ctx context.Context, args map[string]any) error {
	f.mu.Lock()
	f.launchCalls++
	if pendingTargetID, ok := args["__pendingTargetId"].(string); ok {
		f.launchPendingTargetID = pendingTargetID
	}
	waitForConfigurationDone := f.launchWaitsForConfigurationDone
	if waitForConfigurationDone && f.configurationDoneCh == nil {
		f.configurationDoneCh = make(chan struct{})
		if f.configurationDoneClosed {
			close(f.configurationDoneCh)
		}
	}
	ch := f.configurationDoneCh
	emitInitialized := f.emitInitializedOnLaunch
	emitStartDebugging := f.emitStartDebuggingAfterLaunch
	emitStartDebuggingBeforeResponse := f.emitStartDebuggingBeforeLaunchResponse
	if emitStartDebuggingBeforeResponse && f.startDebuggingResponseCh == nil {
		f.startDebuggingResponseCh = make(chan struct{})
	}
	startDebuggingResponseCh := f.startDebuggingResponseCh
	f.mu.Unlock()
	if emitInitialized {
		f.emit(map[string]any{"event": "initialized", "body": map[string]any{}})
	}
	if waitForConfigurationDone {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if emitStartDebuggingBeforeResponse {
		f.emitStartDebuggingRequest("launch")
		select {
		case <-startDebuggingResponseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if emitStartDebugging {
		f.emitStartDebuggingRequest("launch")
	}
	return nil
}
func (f *fakeDAP) Attach(ctx context.Context, args map[string]any) error {
	f.mu.Lock()
	f.attachCalls++
	if pid, ok := args["processId"].(int); ok {
		f.attachProcessID = pid
	}
	if pid, ok := args["pid"].(int); ok {
		f.attachProcessID = pid
	}
	if program, ok := args["program"].(string); ok {
		f.attachProgram = program
	}
	if conn, ok := args["connect"].(map[string]any); ok {
		if port, ok := conn["port"].(int); ok {
			f.attachConnectPort = port
		}
	}
	if port, ok := args["port"].(int); ok {
		f.attachConnectPort = port
	}
	if pendingTargetID, ok := args["__pendingTargetId"].(string); ok {
		f.attachPendingTargetID = pendingTargetID
	}
	waitForConfigurationDone := f.attachWaitsForConfigurationDone
	if waitForConfigurationDone && f.configurationDoneCh == nil {
		f.configurationDoneCh = make(chan struct{})
		if f.configurationDoneClosed {
			close(f.configurationDoneCh)
		}
	}
	ch := f.configurationDoneCh
	emitInitialized := f.emitInitializedOnAttach
	emitStartDebugging := f.emitStartDebuggingAfterAttach
	emitStartDebuggingBeforeResponse := f.emitStartDebuggingBeforeAttachResponse
	if emitStartDebuggingBeforeResponse && f.startDebuggingResponseCh == nil {
		f.startDebuggingResponseCh = make(chan struct{})
	}
	startDebuggingResponseCh := f.startDebuggingResponseCh
	f.mu.Unlock()
	if emitInitialized {
		f.emit(map[string]any{"event": "initialized", "body": map[string]any{}})
	}
	if waitForConfigurationDone {
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if emitStartDebuggingBeforeResponse {
		f.emitStartDebuggingRequest("attach")
		select {
		case <-startDebuggingResponseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if emitStartDebugging {
		f.emitStartDebuggingRequest("attach")
	}
	return nil
}
func (f *fakeDAP) Detach(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detachCalls++
	return nil
}
func (f *fakeDAP) ConfigurationDone(ctx context.Context) error {
	f.mu.Lock()
	f.configurationDoneCalls++
	if f.configurationDoneCh != nil && !f.configurationDoneClosed {
		close(f.configurationDoneCh)
	}
	f.configurationDoneClosed = true
	staysPending := f.configurationDoneResponseStaysPending
	f.mu.Unlock()
	if staysPending {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (f *fakeDAP) SetBreakpoints(_ context.Context, source string, lines []int) (map[string]any, error) {
	f.mu.Lock()
	f.breakpointsSource = source
	f.breakpointsLines = append(f.breakpointsLines, append([]int{}, lines...))
	f.mu.Unlock()
	if f.autoStoppedOnSetBreakpoints {
		f.emit(map[string]any{"event": "stopped", "body": map[string]any{"threadId": f.setBreakpointsThreadID}})
	}
	if f.breakpointsResult != nil {
		return f.breakpointsResult, nil
	}
	return map[string]any{}, nil
}

// breakpointsHistory 返回每次 SetBreakpoints 收到的 lines（按调用顺序）。
func (f *fakeDAP) breakpointsHistory() [][]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	history := make([][]int, len(f.breakpointsLines))
	copy(history, f.breakpointsLines)
	return history
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

func (f *fakeDAP) SubscribeRequests() (<-chan map[string]any, func()) {
	f.mu.Lock()
	ch := make(chan map[string]any, 16)
	f.requestSubs = append(f.requestSubs, ch)
	f.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			f.mu.Lock()
			for i, sub := range f.requestSubs {
				if sub == ch {
					f.requestSubs = append(f.requestSubs[:i], f.requestSubs[i+1:]...)
					close(ch)
					break
				}
			}
			f.mu.Unlock()
		})
	}
	return ch, cancel
}

func (f *fakeDAP) RespondToRequest(_ context.Context, request map[string]any, success bool, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if command, _ := request["command"].(string); command == "startDebugging" && success {
		f.respondedStartDebugging = true
		if f.startDebuggingResponseCh != nil && !f.startDebuggingResponseClosed {
			close(f.startDebuggingResponseCh)
			f.startDebuggingResponseClosed = true
		}
	}
	return nil
}

func (f *fakeDAP) emit(event map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sub := range f.subs {
		sub <- event
	}
}

func (f *fakeDAP) emitRequest(request map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, sub := range f.requestSubs {
		sub <- request
	}
}

func (f *fakeDAP) emitStartDebuggingRequest(request string) {
	if request == "" {
		request = "attach"
	}
	f.emitRequest(map[string]any{
		"type":    "request",
		"seq":     7,
		"command": "startDebugging",
		"arguments": map[string]any{
			"request": request,
			"configuration": map[string]any{
				"type":              "pwa-node",
				"name":              "Remote Process [0]",
				"__pendingTargetId": "pending-node-target",
			},
		},
	})
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
