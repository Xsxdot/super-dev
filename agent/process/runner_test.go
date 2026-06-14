package process_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/process"
)

func TestRunnerCapturesOutput(t *testing.T) {
	var mu sync.Mutex
	var lines []string

	r := process.NewRunner(process.RunnerConfig{
		Command: `echo "hello world"`,
		WorkDir: t.TempDir(),
		OnLine: func(line, stream string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		},
	})

	require.NoError(t, r.Start())
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(strings.Join(lines, ""), "hello world")
	}, 5*time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, strings.Contains(strings.Join(lines, ""), "hello world"))
}

func TestRunnerStartArgvBypassesShell(t *testing.T) {
	var lines []string
	var mu sync.Mutex
	done := make(chan struct{})
	r := process.NewRunner(process.RunnerConfig{
		Argv: []string{"echo", "hello argv"},
		OnLine: func(line, stream string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		},
		OnExit: func(process.ExitInfo) { close(done) },
	})
	require.NoError(t, r.Start())
	<-done
	mu.Lock()
	defer mu.Unlock()
	require.Contains(t, lines, "hello argv")
}

func TestRunnerRunsPreRunBeforeMain(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "built.txt")
	done := make(chan process.ExitInfo, 1)
	r := process.NewRunner(process.RunnerConfig{
		PreRun: &process.CommandStep{Argv: []string{"sh", "-c", "echo built > " + marker}},
		Argv:   []string{"cat", marker},
		OnExit: func(info process.ExitInfo) { done <- info },
	})
	require.NoError(t, r.Start())
	info := <-done
	assert.Equal(t, 0, info.ExitCode)
	data, _ := os.ReadFile(marker)
	assert.Contains(t, string(data), "built")
}

func TestRunnerPreRunFailureIsStartFailure(t *testing.T) {
	r := process.NewRunner(process.RunnerConfig{
		PreRun: &process.CommandStep{Argv: []string{"sh", "-c", "echo compile error 1>&2; exit 1"}},
		Argv:   []string{"echo", "should not run"},
	})
	err := r.Start()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "compile error")
}

func TestRunnerFindsNVMToolWhenAgentPathIsMinimal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	binDir := filepath.Join(home, ".nvm", "versions", "node", "v22.14.0", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	toolPath := filepath.Join(binDir, "superdev-nvm-tool")
	require.NoError(t, os.WriteFile(toolPath, []byte("#!/bin/sh\nprintf nvm-tool\n"), 0o755))

	var mu sync.Mutex
	var lines []string
	r := process.NewRunner(process.RunnerConfig{
		Command: "superdev-nvm-tool",
		WorkDir: t.TempDir(),
		OnLine: func(line, _ string) {
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()
		},
	})

	require.NoError(t, r.Start())
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return !r.IsRunning() && len(lines) == 1 && lines[0] == "nvm-tool"
	}, 5*time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, lines, "nvm-tool")
}

func TestRunner_ExitInfoOnFailureDrainsStderr(t *testing.T) {
	exitCh := make(chan process.ExitInfo, 1)
	r := process.NewRunner(process.RunnerConfig{
		Command: "echo boom 1>&2; exit 3",
		WorkDir: t.TempDir(),
		OnLine:  func(_, _ string) {},
		OnExit:  func(info process.ExitInfo) { exitCh <- info },
	})
	require.NoError(t, r.Start())

	select {
	case info := <-exitCh:
		assert.Equal(t, process.ExitReasonExited, info.Reason)
		assert.Equal(t, 3, info.ExitCode)
		assert.Contains(t, info.StderrTail, "boom")
	case <-time.After(5 * time.Second):
		t.Fatal("onExit not called within 5s")
	}
}

func TestRunner_OnLineMayBeNil(t *testing.T) {
	exitCh := make(chan process.ExitInfo, 1)
	r := process.NewRunner(process.RunnerConfig{
		Command: "echo no-callback; exit 0",
		WorkDir: t.TempDir(),
		OnExit:  func(info process.ExitInfo) { exitCh <- info },
	})
	require.NoError(t, r.Start())

	select {
	case info := <-exitCh:
		assert.Equal(t, 0, info.ExitCode)
	case <-time.After(5 * time.Second):
		t.Fatal("onExit not called")
	}
}

func TestRunner_ProcessGroupAliveAfterExit(t *testing.T) {
	r := process.NewRunner(process.RunnerConfig{
		Command: "exit 0",
		WorkDir: t.TempDir(),
		OnLine:  func(_, _ string) {},
	})
	require.NoError(t, r.Start())

	require.Eventually(t, func() bool {
		return !r.IsRunning()
	}, 3*time.Second, 20*time.Millisecond)
	assert.False(t, r.ProcessGroupAlive())
}

func TestRunner_ProcessGroupAliveAfterShellExitsWithBackgroundChild(t *testing.T) {
	exitCh := make(chan process.ExitInfo, 1)
	r := process.NewRunner(process.RunnerConfig{
		Command: "sleep 60 &",
		WorkDir: t.TempDir(),
		OnLine:  func(_, _ string) {},
		OnExit:  func(info process.ExitInfo) { exitCh <- info },
	})
	require.NoError(t, r.Start())
	defer r.Stop()

	select {
	case info := <-exitCh:
		assert.Equal(t, 0, info.ExitCode)
	case <-time.After(5 * time.Second):
		t.Fatal("onExit not called")
	}
	assert.True(t, r.ProcessGroupAlive())
}

func TestRunnerStop(t *testing.T) {
	r := process.NewRunner(process.RunnerConfig{
		Command: "sleep 60",
		WorkDir: t.TempDir(),
		OnLine:  func(_, _ string) {},
	})
	require.NoError(t, r.Start())
	assert.True(t, r.IsRunning())

	r.Stop()
	require.Eventually(t, func() bool { return !r.IsRunning() }, 5*time.Second, 10*time.Millisecond)
	assert.False(t, r.IsRunning())
}
