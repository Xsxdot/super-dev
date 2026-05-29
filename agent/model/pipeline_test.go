// Package model_test 验证 pipeline 声明模型（Pipeline / Step / StepScope / StepAction）。
//
// 职责：
//   - 验证 StepScope / StepAction 枚举常量字符串值
//   - 验证 Pipeline JSON 序列化/反序列化（roundtrip）
//   - 验证 Step omitempty 字段在零值时不出现在 JSON 输出中
package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestStepScopeAndActionConstants(t *testing.T) {
	assert.Equal(t, "local", string(model.ScopeLocal))
	assert.Equal(t, "fan-out", string(model.ScopeFanOut))
	assert.Equal(t, "run", string(model.ActionRun))
	assert.Equal(t, "sync", string(model.ActionSync))
}

func TestPipelineJSONRoundTrip(t *testing.T) {
	p := model.Pipeline{Steps: []model.Step{
		{ID: "s1", Name: "构建", Scope: model.ScopeLocal, Action: model.ActionRun,
			Command: "go build -o app ./cmd", WorkDir: "./server"},
		{ID: "s2", Name: "同步", Scope: model.ScopeFanOut, Action: model.ActionSync,
			SyncFrom: "./server/app", SyncTo: "/opt/app"},
	}}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	var got model.Pipeline
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, p, got)
}

// TestStepOmitemptyZeroValues 验证 Step 中标记 omitempty 的字段在零值时不出现在 JSON 输出中。
// 仅设置必填字段（ID/Name/Scope/Action），Command/WorkDir/SyncFrom/SyncTo 应全部被省略。
func TestStepOmitemptyZeroValues(t *testing.T) {
	step := model.Step{
		ID:     "s3",
		Name:   "重启",
		Scope:  model.ScopeFanOut,
		Action: model.ActionRun,
		// Command / WorkDir / SyncFrom / SyncTo 均为零值
	}
	data, err := json.Marshal(step)
	require.NoError(t, err)

	// 零值 omitempty 字段不应出现在序列化结果中
	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.NotContains(t, raw, "command",   "command 为零值，应被 omitempty 省略")
	assert.NotContains(t, raw, "work_dir",  "work_dir 为零值，应被 omitempty 省略")
	assert.NotContains(t, raw, "sync_from", "sync_from 为零值，应被 omitempty 省略")
	assert.NotContains(t, raw, "sync_to",   "sync_to 为零值，应被 omitempty 省略")

	// 必填字段必须保留
	assert.Equal(t, "s3", raw["id"])
	assert.Equal(t, "重启", raw["name"])

	// roundtrip 验证反序列化后数据一致
	var got model.Step
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, step, got)
}

func TestRunStatusConstants(t *testing.T) {
	assert.Equal(t, "pending", string(model.StatusPending))
	assert.Equal(t, "running", string(model.RunStatusRunning))
	assert.Equal(t, "success", string(model.StatusSuccess))
	assert.Equal(t, "failed", string(model.RunStatusFailed))
	assert.Equal(t, "canceled", string(model.StatusCanceled))
}

func TestRunJSONRoundTrip(t *testing.T) {
	r := model.Run{
		ID: "run-1", DeploymentID: "dep-1", Status: model.RunStatusRunning,
		StartedAt: 1716000000,
		StepRuns: []model.StepRun{{
			StepID: "s1", Name: "构建", Scope: model.ScopeLocal,
			Status: model.RunStatusRunning,
			Tasks:  []model.Task{{Status: model.RunStatusRunning, StartedAt: 1716000000}},
		}},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	var got model.Run
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, r, got)
}

func TestPipelineExpand(t *testing.T) {
	p := model.Pipeline{Steps: []model.Step{
		{ID: "build", Name: "构建", Scope: model.ScopeLocal, Action: model.ActionRun},
		{ID: "sync", Name: "同步", Scope: model.ScopeFanOut, Action: model.ActionSync},
	}}
	hosts := []model.HostRef{{ID: "stg-01", Name: "staging-01"}, {ID: "stg-02", Name: "staging-02"}}

	run := p.Expand("dep-1", hosts)

	assert.Equal(t, "dep-1", run.DeploymentID)
	assert.Equal(t, model.StatusPending, run.Status)
	require.Len(t, run.StepRuns, 2)

	// local 步骤：1 个无 host 的 task
	local := run.StepRuns[0]
	assert.Equal(t, "build", local.StepID)
	assert.Equal(t, model.ScopeLocal, local.Scope)
	require.Len(t, local.Tasks, 1)
	assert.Empty(t, local.Tasks[0].HostID)
	assert.Equal(t, model.StatusPending, local.Tasks[0].Status)

	// fan-out 步骤：每台 host 一个 task
	fan := run.StepRuns[1]
	require.Len(t, fan.Tasks, 2)
	assert.Equal(t, "stg-01", fan.Tasks[0].HostID)
	assert.Equal(t, "staging-01", fan.Tasks[0].HostName)
	assert.Equal(t, "stg-02", fan.Tasks[1].HostID)
}

func TestPipelineExpandFanOutNoHosts(t *testing.T) {
	// fan-out 但没有 host：该步骤展开为 0 个 task
	p := model.Pipeline{Steps: []model.Step{
		{ID: "sync", Name: "同步", Scope: model.ScopeFanOut, Action: model.ActionSync},
	}}
	run := p.Expand("dep-1", nil)
	require.Len(t, run.StepRuns, 1)
	assert.Empty(t, run.StepRuns[0].Tasks)
}
