// Package metrics 测试 deployment 实例级指标采样器。
//
// 职责：
//   - 验证 systemd、docker、裸进程输出解析
//   - 验证停止态、未知态和命令失败边界
//
// 边界：
//   - 不执行真实系统命令
//   - 不访问 Docker、systemd 或本机进程表
package metrics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/model"
)

type fakeCommand struct {
	outputs map[string]string
	errs    map[string]error
	calls   []string
}

func (f *fakeCommand) Run(ctx context.Context, name string, args ...string) (string, error) {
	key := name + " " + joinArgs(args)
	f.calls = append(f.calls, key)
	if err := f.errs[key]; err != nil {
		return "", err
	}
	return f.outputs[key], nil
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func TestSystemdSamplerCalculatesCPUFromSecondSample(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	cmd := &fakeCommand{
		outputs: map[string]string{
			"systemctl show api.service --property=MemoryCurrent,CPUUsageNSec,NRestarts,ActiveState,SubState": "MemoryCurrent=104857600\nCPUUsageNSec=1000000000\nNRestarts=2\nActiveState=active\nSubState=running\n",
		},
		errs: map[string]error{},
	}
	sampler := NewSamplerWithClock(cmd, clock)
	target := SampleTarget{DeploymentID: "dep-api", Base: "systemd", Unit: "api.service"}

	first, err := sampler.Sample(context.Background(), target)
	require.NoError(t, err)
	require.Nil(t, first.CPUPercent)
	assert.Equal(t, int64(104857600), *first.MemBytes)
	assert.Equal(t, 2, *first.Restarts)
	assert.Equal(t, model.HealthRunning, first.Health)

	clock.now = clock.now.Add(2 * time.Second)
	cmd.outputs["systemctl show api.service --property=MemoryCurrent,CPUUsageNSec,NRestarts,ActiveState,SubState"] = "MemoryCurrent=104857600\nCPUUsageNSec=3000000000\nNRestarts=2\nActiveState=active\nSubState=running\n"
	second, err := sampler.Sample(context.Background(), target)
	require.NoError(t, err)
	require.NotNil(t, second.CPUPercent)
	assert.InDelta(t, 100.0, *second.CPUPercent, 0.01)
}

func TestDockerSamplerParsesStatsAndInspect(t *testing.T) {
	cmd := &fakeCommand{
		outputs: map[string]string{
			"docker stats --no-stream --format {{.CPUPerc}}|{{.MemUsage}} api-dev": "12.50%|128MiB / 1GiB\n",
			"docker inspect api-dev": `[{"RestartCount":3,"State":{"Status":"running","Health":{"Status":"healthy"},"StartedAt":"2026-06-02T01:00:00Z"}}]`,
		},
		errs: map[string]error{},
	}
	sampler := NewSampler(cmd)

	got, err := sampler.Sample(context.Background(), SampleTarget{DeploymentID: "dep-api", Base: "docker", Container: "api-dev"})
	require.NoError(t, err)
	require.NotNil(t, got.CPUPercent)
	require.NotNil(t, got.MemBytes)
	require.NotNil(t, got.Restarts)
	assert.InDelta(t, 12.5, *got.CPUPercent, 0.01)
	assert.Equal(t, int64(134217728), *got.MemBytes)
	assert.Equal(t, 3, *got.Restarts)
	assert.Equal(t, model.HealthHealthy, got.Health)
}

func TestProcessSamplerSumsRootProcessAndChildren(t *testing.T) {
	cmd := &fakeCommand{
		outputs: map[string]string{
			"ps -axo pid=,ppid=,%cpu=,rss=,etime=": "100 1 5.5 1000 01:00\n101 100 2.0 2000 00:30\n102 1 9.0 9000 00:10\n",
		},
		errs: map[string]error{},
	}
	sampler := NewSampler(cmd)

	got, err := sampler.Sample(context.Background(), SampleTarget{DeploymentID: "dep-api", Base: "process", PID: 100})
	require.NoError(t, err)
	require.NotNil(t, got.CPUPercent)
	require.NotNil(t, got.MemBytes)
	require.NotNil(t, got.UptimeSec)
	assert.InDelta(t, 7.5, *got.CPUPercent, 0.01)
	assert.Equal(t, int64(3072000), *got.MemBytes)
	assert.Equal(t, int64(60), *got.UptimeSec)
	assert.Equal(t, model.HealthRunning, got.Health)
}

func TestSamplerStoppedFallbackWhenPIDMissing(t *testing.T) {
	sampler := NewSampler(&fakeCommand{outputs: map[string]string{}, errs: map[string]error{}})

	got, err := sampler.Sample(context.Background(), SampleTarget{DeploymentID: "dep-api", Base: "process", PID: 0})
	require.NoError(t, err)
	assert.Equal(t, model.HealthStopped, got.Health)
	assert.Nil(t, got.CPUPercent)
	assert.Nil(t, got.MemBytes)
}

func TestSamplerReturnsUnknownForUnsupportedOrCommandError(t *testing.T) {
	cmd := &fakeCommand{
		outputs: map[string]string{},
		errs: map[string]error{
			"systemctl show bad.service --property=MemoryCurrent,CPUUsageNSec,NRestarts,ActiveState,SubState": errors.New("not found"),
		},
	}
	sampler := NewSampler(cmd)

	unsupported, err := sampler.Sample(context.Background(), SampleTarget{DeploymentID: "dep-1", Base: "nginx_static"})
	require.NoError(t, err)
	assert.Equal(t, model.HealthUnknown, unsupported.Health)

	failed, err := sampler.Sample(context.Background(), SampleTarget{DeploymentID: "dep-2", Base: "systemd", Unit: "bad.service"})
	require.Error(t, err)
	assert.Equal(t, model.HealthUnknown, failed.Health)
}
