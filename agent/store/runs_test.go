// Package store_test 验证 pipeline run 和 per-host 日志持久化。
package store_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/store"
)

func TestRunStoreSaveGetListAndLogs(t *testing.T) {
	s, err := store.New(t.TempDir() + "/logs.db")
	require.NoError(t, err)
	defer s.Close()

	run := model.Run{
		ID:              "run-1",
		ProjectID:       "p1",
		PipelineID:      "deploy",
		EnvName:         "prod",
		DeploymentID:    "project:p1:pipeline:deploy:env:prod",
		ArtifactVersion: "v1",
		Status:          model.RunStatusRunning,
		StepRuns: []model.StepRun{{
			StepName: "Upload", Type: "transfer", Phase: model.PhaseDeploy,
			Status: model.RunStatusRunning,
			Tasks:  []model.Task{{HostID: "h1", HostName: "host-1", Status: model.RunStatusRunning}},
		}},
		StartedAt: 100,
	}
	require.NoError(t, s.SaveRun(run))
	got, ok, err := s.GetRun("run-1")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "v1", got.ArtifactVersion)
	assert.Equal(t, "host-1", got.StepRuns[0].Tasks[0].HostName)

	run.Status = model.StatusSuccess
	run.FinishedAt = 200
	require.NoError(t, s.SaveRun(run))
	list, err := s.ListRuns("p1", "deploy")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, model.StatusSuccess, list[0].Status)

	require.NoError(t, s.AppendRunLog("run-1", "Upload", "h1", "stdout", "uploaded", 150))
	require.NoError(t, s.AppendRunLog("run-1", "Upload", "h2", "stderr", "other", 151))
	lines, err := s.ReadRunLogs(store.RunLogQuery{RunID: "run-1", StepName: "Upload", HostID: "h1"})
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Equal(t, "uploaded", lines[0].Line)
	assert.Equal(t, "h1", lines[0].HostID)
}

func TestRunStoreAppendRunLogLineReturnsIDAndSupportsAscendingReplay(t *testing.T) {
	s, err := store.New(t.TempDir() + "/logs.db")
	require.NoError(t, err)
	defer s.Close()

	first, err := s.AppendRunLogLine("run-1", "Build", "", "stdout", "one", 100)
	require.NoError(t, err)
	second, err := s.AppendRunLogLine("run-1", "Build", "", "stderr", "two", 101)
	require.NoError(t, err)
	third, err := s.AppendRunLogLine("run-1", "Deploy", "h1", "stdout", "three", 102)
	require.NoError(t, err)

	assert.Positive(t, first.ID)
	assert.Equal(t, first.ID+1, second.ID)
	assert.Equal(t, second.ID+1, third.ID)

	lines, err := s.ReadRunLogs(store.RunLogQuery{
		RunID:     "run-1",
		Limit:     2,
		AfterID:   first.ID,
		Ascending: true,
	})
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, []string{"two", "three"}, []string{lines[0].Line, lines[1].Line})
	assert.Equal(t, []int64{second.ID, third.ID}, []int64{lines[0].ID, lines[1].ID})
}
