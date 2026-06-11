// playwright.go 封装 Playwright-Go 对 CDP browser session 的控制。
//
// 职责：
//   - 连接由 browserdebug 创建的 Chromium CDP endpoint
//   - 在目标页面上执行 snapshot、click、type、screenshot、evaluate
//
// 边界：
//   - 不创建或关闭业务浏览器会话
//   - 不向 API/MCP 泄漏 Playwright 原始对象
package browsercontrol

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"sync"

	playwright "github.com/playwright-community/playwright-go"
)

const defaultSnapshotMaxText = 12000

// PlaywrightController 使用 Playwright connectOverCDP 执行页面控制动作。
type PlaywrightController struct {
	runOptions  *playwright.RunOptions
	mu          sync.Mutex
	driverReady bool
}

// NewPlaywrightController 创建 Playwright 浏览器控制器。
func NewPlaywrightController(driverDir ...string) *PlaywrightController {
	options := &playwright.RunOptions{
		SkipInstallBrowsers: true,
		Stdout:              io.Discard,
		Stderr:              io.Discard,
		Verbose:             false,
	}
	if len(driverDir) > 0 {
		options.DriverDirectory = strings.TrimSpace(driverDir[0])
	}
	return &PlaywrightController{runOptions: options}
}

// Snapshot 返回当前页面的标题、URL 和可读文本。
func (c *PlaywrightController) Snapshot(ctx context.Context, session SessionRef, req SnapshotRequest) (Snapshot, error) {
	var out Snapshot
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		selector := strings.TrimSpace(req.Selector)
		maxText := req.MaxText
		if maxText <= 0 {
			maxText = defaultSnapshotMaxText
		}
		expression := `({ selector, maxText }) => {
  const root = selector ? document.querySelector(selector) : document.body;
  const text = root ? (root.innerText || root.textContent || "") : "";
  return {
    url: location.href,
    title: document.title,
    text: text.replace(/\s+\n/g, "\n").trim().slice(0, maxText)
  };
}`
		value, err := page.Evaluate(expression, map[string]any{"selector": selector, "maxText": maxText})
		if err != nil {
			return err
		}
		data, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("snapshot returned unexpected shape")
		}
		out = Snapshot{
			SessionID: session.ID,
			URL:       stringValue(data["url"]),
			Title:     stringValue(data["title"]),
			Text:      stringValue(data["text"]),
		}
		return nil
	})
	return out, err
}

// Click 点击页面元素。
func (c *PlaywrightController) Click(ctx context.Context, session SessionRef, req ClickRequest) (ActionResult, error) {
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		return ActionResult{}, fmt.Errorf("selector is required")
	}
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		return page.Click(selector)
	})
	return ActionResult{SessionID: session.ID, OK: err == nil}, err
}

// Type 输入文本；Fill 为 true 时替换输入框内容，否则逐字键入。
func (c *PlaywrightController) Type(ctx context.Context, session SessionRef, req TypeRequest) (ActionResult, error) {
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		return ActionResult{}, fmt.Errorf("selector is required")
	}
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		if req.Fill {
			return page.Fill(selector, req.Text)
		}
		return page.Type(selector, req.Text)
	})
	return ActionResult{SessionID: session.ID, OK: err == nil}, err
}

// Screenshot 截取页面 PNG 并返回 base64。
func (c *PlaywrightController) Screenshot(ctx context.Context, session SessionRef, req ScreenshotRequest) (Screenshot, error) {
	var out Screenshot
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		data, err := page.Screenshot(playwright.PageScreenshotOptions{FullPage: playwright.Bool(req.FullPage)})
		if err != nil {
			return err
		}
		out = Screenshot{SessionID: session.ID, MimeType: "image/png", DataBase64: base64.StdEncoding.EncodeToString(data)}
		return nil
	})
	return out, err
}

// Evaluate 执行页面 JavaScript 表达式。
func (c *PlaywrightController) Evaluate(ctx context.Context, session SessionRef, req EvaluateRequest) (EvaluateResult, error) {
	expression := strings.TrimSpace(req.Expression)
	if expression == "" {
		return EvaluateResult{}, fmt.Errorf("expression is required")
	}
	var out EvaluateResult
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		result, err := page.Evaluate(expression)
		if err != nil {
			return err
		}
		out = EvaluateResult{SessionID: session.ID, Result: result}
		return nil
	})
	return out, err
}

func (c *PlaywrightController) withPage(ctx context.Context, session SessionRef, fn func(playwright.Page) error) error {
	if strings.TrimSpace(session.BrowserWS) == "" {
		return fmt.Errorf("browser websocket endpoint is required")
	}
	if err := c.ensureDriver(); err != nil {
		return err
	}
	pw, err := playwright.Run(c.runOptions)
	if err != nil {
		return fmt.Errorf("start playwright driver: %w", err)
	}
	defer func() { _ = pw.Stop() }()
	browser, err := pw.Chromium.ConnectOverCDP(session.BrowserWS)
	if err != nil {
		return fmt.Errorf("connect over cdp: %w", err)
	}
	page, err := selectPage(browser, session)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- fn(page)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func (c *PlaywrightController) ensureDriver() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.driverReady {
		return nil
	}
	if err := playwright.Install(c.runOptions); err != nil {
		return fmt.Errorf("install playwright driver: %w", err)
	}
	c.driverReady = true
	return nil
}

func selectPage(browser playwright.Browser, session SessionRef) (playwright.Page, error) {
	var first playwright.Page
	for _, context := range browser.Contexts() {
		for _, page := range context.Pages() {
			if first == nil {
				first = page
			}
			if session.TargetURL != "" && page.URL() == session.TargetURL {
				return page, nil
			}
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("browser page not found")
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
