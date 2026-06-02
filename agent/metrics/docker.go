package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/superdev/agent/model"
)

type dockerInspectItem struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		Status    string              `json:"Status"`
		StartedAt string              `json:"StartedAt"`
		Health    *dockerHealthStatus `json:"Health"`
	} `json:"State"`
}

type dockerHealthStatus struct {
	Status string `json:"Status"`
}

func (s *Sampler) sampleDocker(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error) {
	statsOut, err := s.cmd.Run(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}", target.Container)
	if err != nil {
		return unknownMetrics(target.Base), err
	}
	inspectOut, err := s.cmd.Run(ctx, "docker", "inspect", target.Container)
	if err != nil {
		return unknownMetrics(target.Base), err
	}

	cpu, mem, err := parseDockerStats(statsOut)
	if err != nil {
		return unknownMetrics(target.Base), err
	}
	health, restarts, uptime, err := parseDockerInspectWithClock(inspectOut, s.clock.Now())
	if err != nil {
		return unknownMetrics(target.Base), err
	}
	return model.InstanceMetrics{
		CPUPercent: cpu,
		MemBytes:   mem,
		UptimeSec:  uptime,
		Restarts:   restarts,
		Health:     health,
		Base:       target.Base,
	}, nil
}

func parseDockerStats(output string) (*float64, *int64, error) {
	parts := strings.Split(strings.TrimSpace(output), "|")
	if len(parts) != 2 {
		return nil, nil, fmt.Errorf("unexpected docker stats output: %q", output)
	}
	cpuValue := strings.TrimSuffix(strings.TrimSpace(parts[0]), "%")
	cpu, err := strconv.ParseFloat(cpuValue, 64)
	if err != nil {
		return nil, nil, err
	}
	memText := strings.TrimSpace(parts[1])
	if beforeSlash, _, ok := strings.Cut(memText, "/"); ok {
		memText = strings.TrimSpace(beforeSlash)
	}
	mem, err := parseBytes(memText)
	if err != nil {
		return nil, nil, err
	}
	return &cpu, &mem, nil
}

func parseDockerInspect(output string) (health model.Health, restarts *int, uptime *int64, err error) {
	return parseDockerInspectWithClock(output, time.Now())
}

func parseDockerInspectWithClock(output string, now time.Time) (model.Health, *int, *int64, error) {
	var payload []dockerInspectItem
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return model.HealthUnknown, nil, nil, err
	}
	if len(payload) == 0 {
		return model.HealthUnknown, nil, nil, fmt.Errorf("docker inspect returned no containers")
	}

	item := payload[0]
	restarts := item.RestartCount
	var uptime *int64
	if startedAt, err := time.Parse(time.RFC3339Nano, item.State.StartedAt); err == nil && !startedAt.IsZero() {
		seconds := int64(now.Sub(startedAt).Seconds())
		if seconds >= 0 {
			uptime = &seconds
		}
	}
	return dockerHealth(item.State.Status, item.State.Health), &restarts, uptime, nil
}

func dockerHealth(status string, health *dockerHealthStatus) model.Health {
	if status == "running" && health != nil {
		switch health.Status {
		case "healthy":
			return model.HealthHealthy
		case "starting":
			return model.HealthRestarting
		case "unhealthy":
			return model.HealthFailed
		}
	}
	switch status {
	case "running":
		return model.HealthRunning
	case "restarting":
		return model.HealthRestarting
	case "exited", "created", "paused":
		return model.HealthStopped
	case "dead":
		return model.HealthFailed
	default:
		return model.HealthUnknown
	}
}

func parseBytes(value string) (int64, error) {
	value = strings.TrimSpace(value)
	units := []struct {
		suffix string
		scale  float64
	}{
		{"TiB", 1024 * 1024 * 1024 * 1024},
		{"GiB", 1024 * 1024 * 1024},
		{"MiB", 1024 * 1024},
		{"KiB", 1024},
		{"TB", 1000 * 1000 * 1000 * 1000},
		{"GB", 1000 * 1000 * 1000},
		{"MB", 1000 * 1000},
		{"KB", 1000},
		{"B", 1},
	}
	for _, unit := range units {
		if strings.HasSuffix(value, unit.suffix) {
			raw := strings.TrimSpace(strings.TrimSuffix(value, unit.suffix))
			n, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return 0, err
			}
			return int64(n * unit.scale), nil
		}
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}
