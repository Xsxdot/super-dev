package metrics

import (
	"context"
	"sync"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

// MetricsSampler 采样单个 deployment 实例的运行指标。
type MetricsSampler interface {
	Sample(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error)
}

// SampleTarget 描述一次指标采样的目标实例。
type SampleTarget struct {
	DeploymentID string
	Base         string
	Unit         string
	Container    string
	Label        string
	PID          int
}

// Clock 抽象当前时间，便于测试 CPU 增量计算。
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Sampler 基于系统命令采样实例级运行指标。
type Sampler struct {
	cmd   CommandExecutor
	clock Clock
	mu    sync.Mutex
	last  map[string]systemdLastSample
}

// NewSampler 创建使用系统时钟的指标采样器。
func NewSampler(cmd CommandExecutor) *Sampler {
	return NewSamplerWithClock(cmd, systemClock{})
}

// NewSamplerWithClock 创建使用指定时钟的指标采样器。
func NewSamplerWithClock(cmd CommandExecutor, clock Clock) *Sampler {
	return &Sampler{cmd: cmd, clock: clock, last: map[string]systemdLastSample{}}
}

// Sample 根据目标运行基座分发到对应采样器。
func (s *Sampler) Sample(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error) {
	switch target.Base {
	case "systemd":
		return s.sampleSystemd(ctx, target)
	case "docker":
		return s.sampleDocker(ctx, target)
	case "launchd":
		return s.sampleLaunchd(ctx, target)
	case "process", "command", "":
		return s.sampleProcess(ctx, target)
	default:
		return unknownMetrics(target.Base), nil
	}
}

func unknownMetrics(base string) model.InstanceMetrics {
	if base == "" {
		base = "unknown"
	}
	return model.InstanceMetrics{Health: model.HealthUnknown, Base: base}
}

func stoppedMetrics(base string) model.InstanceMetrics {
	if base == "" {
		base = "process"
	}
	return model.InstanceMetrics{Health: model.HealthStopped, Base: base}
}
