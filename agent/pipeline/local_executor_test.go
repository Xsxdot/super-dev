package pipeline_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
)

func TestLocalExecutorRunCapturesOutput(t *testing.T) {
	ex := pipeline.NewLocalExecutor()
	var lines []string
	code, err := ex.Run(context.Background(), pipeline.Target{},
		model.Step{Command: "echo hello", Action: model.ActionRun},
		func(line, stream string) { lines = append(lines, line) })
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, strings.Join(lines, "\n"), "hello")
}

func TestLocalExecutorRunNonZeroExit(t *testing.T) {
	ex := pipeline.NewLocalExecutor()
	code, err := ex.Run(context.Background(), pipeline.Target{},
		model.Step{Command: "exit 3", Action: model.ActionRun},
		func(line, stream string) {})
	require.NoError(t, err) // 进程正常跑完，非零退出码不算 Run 自身错误
	assert.Equal(t, 3, code)
}

func TestLocalExecutorSyncIsUnsupported(t *testing.T) {
	ex := pipeline.NewLocalExecutor()
	err := ex.Sync(context.Background(), pipeline.Target{},
		model.Step{Action: model.ActionSync}, func(line, stream string) {})
	require.Error(t, err)
}
