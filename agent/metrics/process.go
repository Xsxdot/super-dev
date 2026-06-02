package metrics

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/superdev/agent/model"
)

func (s *Sampler) sampleProcess(ctx context.Context, target SampleTarget) (model.InstanceMetrics, error) {
	base := target.Base
	if base == "" {
		base = "process"
	}
	if target.PID == 0 {
		return stoppedMetrics(base), nil
	}
	out, err := s.cmd.Run(ctx, "ps", "-axo", "pid=,ppid=,%cpu=,rss=,etime=")
	if err != nil {
		return unknownMetrics(base), err
	}
	cpu, mem, uptime, found, err := parsePSProcessTree(out, target.PID)
	if err != nil {
		return unknownMetrics(base), err
	}
	if !found {
		return stoppedMetrics(base), nil
	}
	return model.InstanceMetrics{
		CPUPercent: cpu,
		MemBytes:   mem,
		UptimeSec:  uptime,
		Health:     model.HealthRunning,
		Base:       base,
	}, nil
}

func parsePSProcessTree(output string, rootPID int) (cpu *float64, memBytes *int64, uptime *int64, found bool, err error) {
	type procRow struct {
		pid       int
		ppid      int
		cpu       float64
		rssKiB    int64
		uptimeSec int64
	}
	rows := map[int]procRow{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 5 {
			return nil, nil, nil, false, fmt.Errorf("unexpected ps row: %q", line)
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, nil, nil, false, err
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, nil, nil, false, err
		}
		cpuValue, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, nil, nil, false, err
		}
		rssKiB, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, nil, nil, false, err
		}
		uptimeSec, err := parseElapsed(fields[4])
		if err != nil {
			return nil, nil, nil, false, err
		}
		rows[pid] = procRow{pid: pid, ppid: ppid, cpu: cpuValue, rssKiB: rssKiB, uptimeSec: uptimeSec}
	}

	root, ok := rows[rootPID]
	if !ok {
		return nil, nil, nil, false, nil
	}
	selected := map[int]bool{rootPID: true}
	for changed := true; changed; {
		changed = false
		for _, row := range rows {
			if selected[row.pid] || !selected[row.ppid] {
				continue
			}
			selected[row.pid] = true
			changed = true
		}
	}

	totalCPU := 0.0
	totalRSSKiB := int64(0)
	for pid := range selected {
		row := rows[pid]
		totalCPU += row.cpu
		totalRSSKiB += row.rssKiB
	}
	mem := totalRSSKiB * 1024
	up := root.uptimeSec
	return &totalCPU, &mem, &up, true, nil
}

func parseElapsed(value string) (int64, error) {
	value = strings.TrimSpace(value)
	days := int64(0)
	if before, after, ok := strings.Cut(value, "-"); ok {
		parsedDays, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return 0, err
		}
		days = parsedDays
		value = after
	}

	parts := strings.Split(value, ":")
	switch len(parts) {
	case 2:
		minutes, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, err
		}
		seconds, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return days*24*60*60 + minutes*60 + seconds, nil
	case 3:
		hours, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, err
		}
		minutes, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, err
		}
		seconds, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			return 0, err
		}
		return days*24*60*60 + hours*60*60 + minutes*60 + seconds, nil
	default:
		return 0, fmt.Errorf("unexpected elapsed value: %q", value)
	}
}
