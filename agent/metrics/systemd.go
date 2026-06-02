package metrics

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/superdev/agent/model"
)

type systemdLastSample struct {
	cpuNS int64
	at    time.Time
}

func (s *Sampler) sampleSystemd(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error) {
	out, err := s.cmd.Run(ctx, "systemctl", "show", target.Unit, "--property=MemoryCurrent,CPUUsageNSec,NRestarts,ActiveState,SubState")
	if err != nil {
		return unknownMetrics(target.Base), err
	}

	props := parseSystemdShow(out)
	metrics := model.InstanceMetrics{
		Health: systemdHealth(props["ActiveState"], props["SubState"]),
		Base:   target.Base,
	}
	if mem := parseOptionalInt64(props["MemoryCurrent"]); mem != nil {
		metrics.MemBytes = mem
	}
	if restarts := parseOptionalInt(props["NRestarts"]); restarts != nil {
		metrics.Restarts = restarts
	}
	if cpuNS := parseOptionalInt64(props["CPUUsageNSec"]); cpuNS != nil {
		now := s.clock.Now()
		key := systemdSampleKey(target)
		s.mu.Lock()
		last, ok := s.last[key]
		s.last[key] = systemdLastSample{cpuNS: *cpuNS, at: now}
		s.mu.Unlock()
		if ok {
			elapsedSeconds := now.Sub(last.at).Seconds()
			deltaCPUSeconds := float64(*cpuNS-last.cpuNS) / 1_000_000_000
			if elapsedSeconds > 0 && deltaCPUSeconds >= 0 {
				cpu := (deltaCPUSeconds / elapsedSeconds) * 100
				metrics.CPUPercent = &cpu
			}
		}
	}
	return metrics, nil
}

func parseSystemdShow(output string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		props[key] = value
	}
	return props
}

func systemdHealth(active, sub string) model.Health {
	switch active {
	case "active":
		if sub == "auto-restart" || sub == "activating" {
			return model.HealthRestarting
		}
		return model.HealthRunning
	case "activating", "reloading":
		return model.HealthRestarting
	case "failed":
		return model.HealthFailed
	case "inactive", "deactivating":
		return model.HealthStopped
	default:
		return model.HealthUnknown
	}
}

func systemdSampleKey(target SampleTarget) string {
	if target.DeploymentID != "" {
		return target.DeploymentID
	}
	return target.Unit
}

func parseOptionalInt64(value string) *int64 {
	value = strings.TrimSpace(value)
	if value == "" || value == "[not set]" {
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func parseOptionalInt(value string) *int {
	value = strings.TrimSpace(value)
	if value == "" || value == "[not set]" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &n
}
