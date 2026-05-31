// Package plugins 中的 http_check.go 实现 HTTP 健康检查插件。
//
// 职责：
//   - 校验 http_check 参数
//   - 对 URL 发起 HTTP 请求并校验状态码
//   - 支持按 target 替换 `${host}`
//
// 边界：
//   - 不管理重试，重试由 engine 统一处理
//   - 不处理证书文件生成或 Nginx 配置
package plugins

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/superdev/agent/model"
	"github.com/superdev/agent/pipeline"
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
	if len(targets) == 0 {
		return p.check(ctx.Context, url, expected)
	}
	for _, target := range targets {
		host := target.HostName
		if host == "" {
			host = target.HostID
		}
		if err := p.check(ctx.Context, strings.ReplaceAll(url, "${host}", host), expected); err != nil {
			return err
		}
	}
	return nil
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
