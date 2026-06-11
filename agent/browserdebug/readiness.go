// readiness.go 在打开调试浏览器前等待本机 Web 入口就绪。
//
// 职责：
//   - 按 WebReadinessConfig 轮询目标 URL
//   - 避免前端服务未启动完成时打开空白调试页
//
// 边界：
//   - 不启动前端服务
//   - 不解析 deployment 配置
package browserdebug

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
)

const (
	defaultReadinessTimeout = 30 * time.Second
	readinessPollInterval   = 200 * time.Millisecond
)

// ErrReadinessTimeout 表示本机 Web 入口在配置的超时时间内没有就绪。
var ErrReadinessTimeout = errors.New("web entrypoint is not ready")

// WaitForReadiness 等待目标 URL 返回 2xx/3xx HTTP 状态。
func WaitForReadiness(ctx context.Context, targetURL string, cfg model.WebReadinessConfig, client *http.Client) error {
	if cfg.Type == "" && cfg.TimeoutSeconds == 0 {
		return nil
	}
	if cfg.Type != "" && cfg.Type != model.WebReadinessHTTP {
		return fmt.Errorf("unsupported web readiness type %q", cfg.Type)
	}
	if client == nil {
		client = http.DefaultClient
	}
	timeout := defaultReadinessTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(readinessPollInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		if ready, err := probeHTTPReadiness(ctx, client, targetURL); ready {
			return nil
		} else if err != nil {
			// 服务仍在启动时连接失败或返回 5xx 很常见，保留原因但继续轮询直到 timeout。
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				if lastErr == nil {
					lastErr = err
				}
			} else {
				lastErr = err
			}
		}
		select {
		case <-ctx.Done():
			return readinessTimeoutError(lastErr)
		case <-ticker.C:
		}
	}
}

func readinessTimeoutError(lastErr error) error {
	if lastErr == nil {
		return ErrReadinessTimeout
	}
	return fmt.Errorf("%w: last probe failed: %v", ErrReadinessTimeout, lastErr)
}

func probeHTTPReadiness(ctx context.Context, client *http.Client, targetURL string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, nil
	}
	return false, fmt.Errorf("http status %d", resp.StatusCode)
}
