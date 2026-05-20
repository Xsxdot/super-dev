package process_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/process"
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
