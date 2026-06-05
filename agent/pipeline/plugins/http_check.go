// Package plugins 中的 http_check.go 实现 HTTP 健康检查插件。
//
// 职责：
//   - 校验 http_check 参数
//   - 对 URL 发起 HTTP 请求并校验状态码
//   - 支持按 target 替换 `${host}`
//
// 边界：
//   - 只做健康检查轮询，不执行部署或回滚动作
//   - 不处理证书文件生成或 Nginx 配置
package plugins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/pipeline"
)

// HTTPCheck verifies that an HTTP endpoint returns the expected status.
type HTTPCheck struct {
	client *http.Client
}

// NewHTTPCheck creates HTTPCheck.
//
// 参数：
//   - client: HTTP 客户端，nil 时使用 http.DefaultClient
//
// 返回：
//   - http_check 插件实例
func NewHTTPCheck(client *http.Client) *HTTPCheck {
	if client == nil {
		client = http.DefaultClient
	}
	return &HTTPCheck{client: client}
}

// Name returns the plugin type name.
//
// 返回：
//   - 固定值 `http_check`
func (p *HTTPCheck) Name() string { return "http_check" }

// Validate checks http_check step configuration.
//
// 参数：
//   - step: 待校验步骤
//
// 返回：
//   - with.url 缺失时返回错误
func (p *HTTPCheck) Validate(step model.Step) error {
	if withString(step.With, "url") == "" {
		return errors.New("http_check requires with.url")
	}
	return nil
}

// Execute performs HTTP checks for global or per-target URLs.
//
// 参数：
//   - ctx: 插件运行上下文
//   - step: http_check 步骤
//   - targets: 可选目标列表；存在时用 HostName 替换 URL 中的 `${host}`
//
// 返回：
//   - 请求失败或状态码不匹配时返回错误
func (p *HTTPCheck) Execute(ctx *pipeline.RunContext, step model.Step, targets []pipeline.Target) error {
	if err := p.Validate(step); err != nil {
		return err
	}
	url := withString(step.With, "url")
	expected := withInt(step.With, "expected_status", http.StatusOK)
	timeout, hasTimeout, err := withDuration(step.With, "timeout")
	if err != nil {
		return err
	}
	interval, _, err := withDuration(step.With, "interval")
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return p.checkMaybePolling(ctx.Context, url, expected, timeout, interval, hasTimeout)
	}
	for _, target := range targets {
		host := target.HostName
		if host == "" {
			host = target.HostID
		}
		if err := p.checkMaybePolling(ctx.Context, strings.ReplaceAll(url, "${host}", host), expected, timeout, interval, hasTimeout); err != nil {
			return err
		}
	}
	return nil
}

func (p *HTTPCheck) checkMaybePolling(ctx context.Context, url string, expected int, timeout time.Duration, interval time.Duration, hasTimeout bool) error {
	if !hasTimeout {
		return p.check(ctx, url, expected)
	}
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = p.check(ctx, url, expected)
		if lastErr == nil {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("http_check timed out after %s: %w", timeout, lastErr)
		}
		wait := interval
		if wait > remaining {
			wait = remaining
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *HTTPCheck) check(ctx context.Context, url string, expected int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != expected {
		return fmt.Errorf("http_check expected status %d got %d", expected, resp.StatusCode)
	}
	return nil
}

func withInt(values map[string]interface{}, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	raw, ok := values[key]
	if !ok || raw == nil {
		return fallback
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(v)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func withDuration(values map[string]interface{}, key string) (time.Duration, bool, error) {
	raw := withString(values, key)
	if raw == "" {
		return 0, false, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, true, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return d, true, nil
}
