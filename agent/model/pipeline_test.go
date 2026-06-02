// Package model_test 验证插件化项目流水线的声明与执行模型。
//
// 职责：
//   - 验证 Pipeline / Step 的插件化 DAG JSON roundtrip
//   - 验证 Run / StepRun / Task 的阶段化执行状态模型
//   - 锁定通用状态枚举，避免前后端状态值漂移
//
// 边界：
//   - 不测试 YAML 读写、模板展开、DAG 调度或插件执行
//   - 不访问文件系统或远程主机
package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

func TestPipelineJSONRoundTrip(t *testing.T) {
	p := model.Pipeline{
		Variables: map[string]string{"app_name": "api", "version": "1.0.0"},
		Roles:     map[string][]string{"compute": {"h1", "h2"}},
		Build: []model.Step{{
			Name: "Build",
			Type: "local_command",
			With: map[string]interface{}{"cmd": "go build ./cmd/server", "workDir": "."},
		}},
		Deploy: []model.Step{{
			Name:  "Deploy",
			Type:  "include",
			Needs: []string{"Package"},
			With: map[string]interface{}{
				"template": "builtin://systemd-seamless-deploy",
				"version":  "1.0.0",
				"digest":   "sha256:test",
				"vars": map[string]interface{}{
					"role":     "compute",
					"app_name": "${app_name}",
				},
			},
		}},
		Finally: []model.Step{{Name: "Cleanup", Type: "local_command"}},
	}
	data, err := json.Marshal(p)
	require.NoError(t, err)
	var got model.Pipeline
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, p, got)
}

func TestRunStatusConstantsIncludeSkipped(t *testing.T) {
	assert.Equal(t, "pending", string(model.StatusPending))
	assert.Equal(t, "running", string(model.RunStatusRunning))
	assert.Equal(t, "success", string(model.StatusSuccess))
	assert.Equal(t, "failed", string(model.RunStatusFailed))
	assert.Equal(t, "skipped", string(model.StatusSkipped))
	assert.Equal(t, "canceled", string(model.StatusCanceled))
}

func TestRunJSONRoundTripUsesDAGStepRun(t *testing.T) {
	r := model.Run{
		ID: "run-1", DeploymentID: "dep-1", Status: model.RunStatusRunning,
		StartedAt: 1716000000,
		StepRuns: []model.StepRun{{
			StepName: "Deploy.Upload",
			Type:     "transfer",
			Phase:    model.PhaseDeploy,
			Needs:    []string{"Deploy.Prepare Dir"},
			Status:   model.RunStatusRunning,
			Tasks:    []model.Task{{HostID: "h1", HostName: "box1", Status: model.RunStatusRunning}},
		}},
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	var got model.Run
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, r, got)
}
