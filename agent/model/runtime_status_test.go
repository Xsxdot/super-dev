// Package model_test 验证项目概览运行态接口的数据契约。
//
// 职责：
//   - 锁定 runtime-status JSON 字段和值
//   - 验证未知数值指标序列化为 null
//
// 边界：
//   - 不采集运行指标
//   - 不访问进程、容器、systemd 或远端节点
package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestInstanceMetricsJSONUsesNullForUnknownNumbers(t *testing.T) {
	payload := model.RuntimeStatusResponse{
		Environments: []model.EnvStatus{{
			EnvName: "dev",
			Instances: []model.InstanceStatus{{
				ServiceID:    "svc-api",
				ServiceName:  "api",
				DeploymentID: "dep-api-dev",
				NodeID:       "local-node",
				NodeName:     "local",
				IsLocal:      true,
				Metrics: model.InstanceMetrics{
					CPUPercent: nil,
					MemBytes:   nil,
					UptimeSec:  nil,
					Restarts:   nil,
					Health:     model.HealthRunning,
					Base:       "process",
				},
			}},
		}},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"cpu_percent":null`)
	assert.Contains(t, string(data), `"health":"running"`)

	var got model.RuntimeStatusResponse
	require.NoError(t, json.Unmarshal(data, &got))
	require.Len(t, got.Environments, 1)
	require.Len(t, got.Environments[0].Instances, 1)
	assert.Nil(t, got.Environments[0].Instances[0].Metrics.CPUPercent)
	assert.Equal(t, model.HealthRunning, got.Environments[0].Instances[0].Metrics.Health)
}

func TestDebuggerStatusSerialize(t *testing.T) {
	st := model.InstanceStatus{
		ServiceID:    "s1",
		DeploymentID: "d1",
		Metrics:      model.InstanceMetrics{Health: model.HealthRunning},
		Debugger: &model.DebuggerStatus{
			State:    model.DebuggerStateAttached,
			Language: model.LanguageGo,
			Origin:   model.DebuggerOriginLaunched,
			PausedAt: &model.PausedLocation{Source: "main.go", Line: 42},
		},
	}
	b, err := json.Marshal(st)
	require.NoError(t, err)

	var back model.InstanceStatus
	require.NoError(t, json.Unmarshal(b, &back))
	if back.Debugger == nil || back.Debugger.State != model.DebuggerStateAttached {
		t.Fatalf("debugger not roundtripped: %s", b)
	}
	if back.Debugger.PausedAt == nil || back.Debugger.PausedAt.Line != 42 {
		t.Fatal("paused location lost")
	}
}

func TestInstanceStatusNoDebuggerOmitted(t *testing.T) {
	b, err := json.Marshal(model.InstanceStatus{ServiceID: "s1"})
	require.NoError(t, err)
	if string(b) == "" {
		t.Fatal("empty marshal")
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	if _, ok := m["debugger"]; ok {
		t.Fatal("debugger should be omitted when nil")
	}
}
