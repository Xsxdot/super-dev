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
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, strings.Contains(strings.Join(lines, ""), "hello world"))
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
	require.Eventually(t, func() bool { return !r.IsRunning() }, time.Second, 10*time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, lines, "nvm-tool")
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
	time.Sleep(200 * time.Millisecond)
	assert.False(t, r.IsRunning())
}
