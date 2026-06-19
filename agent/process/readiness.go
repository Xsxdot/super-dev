// Package process 中的就绪探测：判断一个 deployment 是否「起来了」。
//
// 职责：
//   - 按 ReadinessProbe 配置做 HTTP / TCP 轮询探活
//   - 在超时内反复尝试，连得上 / 拿到 2xx-3xx 即返回 nil
//
// 边界：
//   - 不负责进程是否存活（那是 Runner.ProcessGroupAlive 的职责）
//   - 不缓存结果（缓存是编排器单次编排周期内的职责）
package process

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	readinessPollInterval   = 500 * time.Millisecond
	readinessDefaultTimeout = 30 * time.Second
)

// ProbeReady 在超时内轮询探测 probe 指向的目标，直到就绪或超时。
//
// 参数：
//   - ctx: 上下文，取消即中断探测
//   - probe: 探测配置；type 仅支持 "http" / "tcp"
//
// 返回：就绪返回 nil；超时或类型不支持返回 error（带上下文）。
func ProbeReady(ctx context.Context, probe *model.ReadinessProbe) error {
	if probe == nil {
		return nil
	}
	timeout := readinessDefaultTimeout
	if probe.TimeoutSeconds > 0 {
		timeout = time.Duration(probe.TimeoutSeconds) * time.Second
	}
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var lastErr error
	for {
		switch probe.Type {
		case "http":
			lastErr = probeHTTP(ctx, probe.Target)
		case "tcp":
			lastErr = probeTCP(ctx, probe.Target)
		default:
			return fmt.Errorf("readiness: 不支持的探测类型 %q", probe.Type)
		}
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			// 超时退出时带上最后一次失败原因，现场日志才能指向真实边界问题。
			return fmt.Errorf("readiness: 探测 %s %s 超时(%s): %w", probe.Type, probe.Target, timeout, lastErr)
		case <-time.After(readinessPollInterval):
		}
	}
}

func probeHTTP(ctx context.Context, target string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func probeTCP(ctx context.Context, target string) error {
	d := net.Dialer{Timeout: readinessPollInterval}
	conn, err := d.DialContext(ctx, "tcp", target)
	if err != nil {
		return err
	}
	conn.Close()
	return nil
}
