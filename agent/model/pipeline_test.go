// Package model_test 验证 pipeline 声明与执行模型。
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
