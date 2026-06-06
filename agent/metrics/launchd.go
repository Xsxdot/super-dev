// Package metrics 中的 launchd.go 采集 macOS LaunchAgent 运行态。
//
// 职责：
//   - 通过 launchctl print 读取 LaunchAgent 的真实 state、pid 和运行次数
//   - 将 launchd pid 转交给进程树采样器，复用 CPU、内存、运行时长计算
//
// 边界：
//   - 不执行 bootstrap、kickstart 或 bootout 等生命周期操作
//   - 不解析 plist 文件内容，label 来自上层 deployment runtime 配置
package metrics

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

type launchdPrintStatus struct {
	State        string
	PID          int
	Runs         int
	LastExitCode *int
}

func (s *Sampler) sampleLaunchd(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error) {
	base := target.Base
	if base == "" {
		base = "launchd"
	}
	label := strings.TrimSpace(target.Label)
	if label == "" {
		return unknownMetrics(base), fmt.Errorf("launchd label is empty")
	}

	out, err := s.cmd.Run(ctx, "launchctl", "print", launchdPrintTarget(label))
	if err != nil {
		if isLaunchdNotLoaded(out) || isLaunchdNotLoaded(err.Error()) {
			return stoppedMetrics(base), nil
		}
		return unknownMetrics(base), err
	}

	status := parseLaunchdPrint(out)
	restarts := launchdRestarts(status.Runs)
	if status.PID > 0 {
		processTarget := target
		processTarget.Base = base
		processTarget.PID = status.PID
		got, err := s.sampleProcess(ctx, processTarget)
		if got.Restarts == nil {
			got.Restarts = restarts
		}
		return got, err
	}

	return model.InstanceMetrics{
		Restarts: restarts,
		Health:   launchdHealth(status),
		Base:     base,
	}, nil
}

func launchdPrintTarget(label string) string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), strings.TrimSpace(label))
}

func parseLaunchdPrint(output string) launchdPrintStatus {
	status := launchdPrintStatus{}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = cleanLaunchdValue(value)
		switch key {
		case "state":
			status.State = strings.ToLower(value)
		case "pid":
			status.PID = parseLaunchdInt(value)
		case "runs":
			status.Runs = parseLaunchdInt(value)
		case "last exit code":
			if parsed, ok := parseLaunchdOptionalInt(value); ok {
				status.LastExitCode = &parsed
			}
		}
	}
	return status
}

func cleanLaunchdValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ";")
	value = strings.Trim(value, `"`)
	return strings.TrimSpace(value)
}

func parseLaunchdInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseLaunchdOptionalInt(value string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	return parsed, err == nil
}

func launchdRestarts(runs int) *int {
	if runs <= 0 {
		return nil
	}
	restarts := runs - 1
	return &restarts
}

func launchdHealth(status launchdPrintStatus) model.Health {
	switch strings.ToLower(strings.TrimSpace(status.State)) {
	case "running":
		return model.HealthRunning
	case "spawning", "starting":
		return model.HealthRestarting
	case "waiting", "exited", "terminated", "not running":
		if status.LastExitCode != nil && *status.LastExitCode != 0 {
			return model.HealthFailed
		}
		return model.HealthStopped
	default:
		if status.LastExitCode != nil && *status.LastExitCode != 0 {
			return model.HealthFailed
		}
		if status.State == "" {
			return model.HealthUnknown
		}
		return model.HealthStopped
	}
}

func isLaunchdNotLoaded(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "could not find service") ||
		strings.Contains(value, "service is not loaded") ||
		strings.Contains(value, "no such process")
}
