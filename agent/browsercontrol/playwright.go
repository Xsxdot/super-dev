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
	"net/url"
	"strings"
	"sync"
	"time"

	playwright "github.com/playwright-community/playwright-go"
)

const defaultSnapshotMaxText = 12000
const browserEventBufferLimit = 200

type browserConnection struct {
	pw             *playwright.Playwright
	browser        playwright.Browser
	mu             sync.Mutex
	lastUsed       time.Time
	eventsMu       sync.Mutex
	console        []ConsoleLog
	network        []NetworkRequest
	eventsAttached map[playwright.Page]bool
}

// PlaywrightController 使用 Playwright connectOverCDP 执行页面控制动作。
type PlaywrightController struct {
	runOptions  *playwright.RunOptions
	mu          sync.Mutex
	driverReady bool
	connections map[string]*browserConnection
	now         func() time.Time
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
	return &PlaywrightController{
		runOptions:  options,
		connections: map[string]*browserConnection{},
		now:         time.Now,
	}
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
		maxElements := normalizeSnapshotMaxElements(req.MaxElements)
		expression := `({ selector, maxText, maxElements }) => {
  const root = selector ? document.querySelector(selector) : document.body;
  const text = root ? (root.innerText || root.textContent || "") : "";
  const candidates = root ? Array.from(root.querySelectorAll("button,a,input,textarea,select,[role],[data-test],[data-testid]")) : [];
  const clean = (value) => String(value || "").replace(/\s+/g, " ").trim();
  const isVisible = (el) => {
    const rect = el.getBoundingClientRect();
    const style = window.getComputedStyle(el);
    return rect.width > 0 && rect.height > 0 && style.display !== "none" && style.visibility !== "hidden";
  };
  const readElement = (el) => {
    const rect = el.getBoundingClientRect();
    const tag = clean(el.tagName).toLowerCase();
    const type = clean(el.getAttribute("type")).toLowerCase();
    const isInputLike = tag === "input" || tag === "textarea";
    return {
      tag,
      role: clean(el.getAttribute("role")),
      text: isInputLike || type === "password" ? "" : clean(el.innerText || el.textContent).slice(0, 200),
      ariaLabel: clean(el.getAttribute("aria-label")),
      dataTest: clean(el.getAttribute("data-test")),
      dataTestID: clean(el.getAttribute("data-testid")),
      id: clean(el.getAttribute("id")),
      nameAttr: type === "password" ? "[redacted]" : clean(el.getAttribute("name")),
      type,
      visible: isVisible(el),
      enabled: !el.disabled && el.getAttribute("aria-disabled") !== "true",
      bounds: { x: rect.x, y: rect.y, width: rect.width, height: rect.height }
    };
  };
  const elements = candidates.filter(isVisible).slice(0, maxElements).map(readElement);
  const active = document.activeElement && root && root.contains(document.activeElement) ? readElement(document.activeElement) : null;
  return {
    url: location.href,
    title: document.title,
    text: text.replace(/\s+\n/g, "\n").trim().slice(0, maxText),
    elements,
    focused: active
  };
}`
		value, err := page.Evaluate(expression, map[string]any{"selector": selector, "maxText": maxText, "maxElements": maxElements})
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
			Text:      redactSnapshotText(stringValue(data["text"])),
			Elements:  snapshotElementsFromValue(data["elements"]),
			Focused:   snapshotFocusedFromValue(data["focused"]),
		}
		return nil
	})
	return out, err
}

// ProbeSnapshotCapability 返回当前结构化 snapshot 的可用来源。
func (c *PlaywrightController) ProbeSnapshotCapability(ctx context.Context, session SessionRef) (SnapshotCapability, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotCapability{}, err
	}
	return SnapshotCapability{
		AccessibilityTree: false,
		DOMFallback:       true,
		Message:           "playwright-go cdp accessibility snapshot is unavailable; using DOM fallback",
	}, nil
}

// Click 点击页面元素。
func (c *PlaywrightController) Click(ctx context.Context, session SessionRef, req ClickRequest) (ActionResult, error) {
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		return ActionResult{}, NewControlError(CodeInvalidArgument, "selector is required", nil)
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
		return ActionResult{}, NewControlError(CodeInvalidArgument, "selector is required", nil)
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
	limit := req.MaxBytes
	if limit <= 0 {
		limit = DefaultScreenshotMaxBytes
	}
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		data, err := page.Screenshot(playwright.PageScreenshotOptions{FullPage: playwright.Bool(req.FullPage)})
		if err != nil {
			return err
		}
		if len(data) > limit {
			return NewControlError(CodeScreenshotTooLarge, "browser screenshot is too large", map[string]any{
				"limit_bytes":  limit,
				"actual_bytes": len(data),
				"full_page":    req.FullPage,
			})
		}
		out = Screenshot{SessionID: session.ID, MimeType: "image/png", DataBase64: base64.StdEncoding.EncodeToString(data)}
		return nil
	})
	return out, err
}

// Navigate 对同源 URL 执行整页导航。
func (c *PlaywrightController) Navigate(ctx context.Context, session SessionRef, req NavigateRequest) (NavigationResult, error) {
	target, err := resolveNavigateURL(session, req)
	if err != nil {
		return NavigationResult{}, err
	}
	waitUntil, err := parseWaitUntil(req.WaitUntil)
	if err != nil {
		return NavigationResult{}, err
	}
	var out NavigationResult
	err = c.withPage(ctx, session, func(page playwright.Page) error {
		if _, err := page.Goto(target, playwright.PageGotoOptions{WaitUntil: waitUntil}); err != nil {
			return err
		}
		title, err := page.Title()
		if err != nil {
			return err
		}
		out = NavigationResult{SessionID: session.ID, URL: page.URL(), Title: title}
		return nil
	})
	return out, err
}

// Reload 刷新当前页面。
func (c *PlaywrightController) Reload(ctx context.Context, session SessionRef, req ReloadRequest) (NavigationResult, error) {
	waitUntil, err := parseWaitUntil(req.WaitUntil)
	if err != nil {
		return NavigationResult{}, err
	}
	var out NavigationResult
	err = c.withPage(ctx, session, func(page playwright.Page) error {
		if _, err := page.Reload(playwright.PageReloadOptions{WaitUntil: waitUntil}); err != nil {
			return err
		}
		title, err := page.Title()
		if err != nil {
			return err
		}
		out = NavigationResult{SessionID: session.ID, URL: page.URL(), Title: title}
		return nil
	})
	return out, err
}

// WaitForSelector 等待页面元素进入指定状态。
func (c *PlaywrightController) WaitForSelector(ctx context.Context, session SessionRef, req WaitForSelectorRequest) (WaitForSelectorResult, error) {
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		return WaitForSelectorResult{}, NewControlError(CodeInvalidArgument, "selector is required", nil)
	}
	state, err := parseWaitForSelectorState(req.State)
	if err != nil {
		return WaitForSelectorResult{}, err
	}
	options := playwright.PageWaitForSelectorOptions{State: state}
	if req.TimeoutMS > 0 {
		options.Timeout = playwright.Float(float64(req.TimeoutMS))
	}
	var out WaitForSelectorResult
	err = c.withPage(ctx, session, func(page playwright.Page) error {
		element, err := page.WaitForSelector(selector, options)
		if err != nil {
			return err
		}
		out = WaitForSelectorResult{SessionID: session.ID, Matched: true}
		if element != nil {
			text, err := element.InnerText()
			if err == nil {
				out.Text = truncate(text, 500)
			}
		}
		return nil
	})
	return out, err
}

// PressKey 向当前页面或指定 selector 聚焦元素发送按键。
func (c *PlaywrightController) PressKey(ctx context.Context, session SessionRef, req PressKeyRequest) (ActionResult, error) {
	key := strings.TrimSpace(req.Key)
	if key == "" {
		return ActionResult{}, NewControlError(CodeInvalidArgument, "key is required", nil)
	}
	selector := strings.TrimSpace(req.Selector)
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		if selector != "" {
			if err := page.Focus(selector); err != nil {
				return err
			}
		}
		return page.Keyboard().Press(key)
	})
	return ActionResult{SessionID: session.ID, OK: err == nil}, err
}

// SelectOption 选择 select 元素中的 value 或 label。
func (c *PlaywrightController) SelectOption(ctx context.Context, session SessionRef, req SelectOptionRequest) (ActionResult, error) {
	selector := strings.TrimSpace(req.Selector)
	if selector == "" {
		return ActionResult{}, NewControlError(CodeInvalidArgument, "selector is required", nil)
	}
	value := strings.TrimSpace(req.Value)
	label := strings.TrimSpace(req.Label)
	if value == "" && label == "" {
		return ActionResult{}, NewControlError(CodeInvalidArgument, "value or label is required", nil)
	}
	options := playwright.SelectOptionValues{}
	if value != "" {
		options.Values = playwright.StringSlice(value)
	} else {
		options.Labels = playwright.StringSlice(label)
	}
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		_, err := page.SelectOption(selector, options)
		return err
	})
	return ActionResult{SessionID: session.ID, OK: err == nil}, err
}

// ConsoleLogs 返回页面 console 日志 ring buffer。
func (c *PlaywrightController) ConsoleLogs(ctx context.Context, session SessionRef, req ConsoleLogsRequest) (ConsoleLogsResult, error) {
	level := strings.ToLower(strings.TrimSpace(req.Level))
	if level != "" && level != "log" && level != "info" && level != "warning" && level != "error" {
		return ConsoleLogsResult{}, NewControlError(CodeInvalidArgument, "level must be log, info, warning, or error", nil)
	}
	limit := normalizeEventLimit(req.Limit)
	var out ConsoleLogsResult
	err := c.withConnectionPage(ctx, session, func(conn *browserConnection, _ playwright.Page) error {
		conn.eventsMu.Lock()
		defer conn.eventsMu.Unlock()
		logs := make([]ConsoleLog, 0, limit)
		for i := len(conn.console) - 1; i >= 0 && len(logs) < limit; i-- {
			item := conn.console[i]
			if level == "" || item.Level == level {
				logs = append(logs, item)
			}
		}
		reverseConsoleLogs(logs)
		out = ConsoleLogsResult{SessionID: session.ID, Logs: logs}
		return nil
	})
	return out, err
}

// NetworkRequests 返回页面网络请求 ring buffer。
func (c *PlaywrightController) NetworkRequests(ctx context.Context, session SessionRef, req NetworkRequestsRequest) (NetworkRequestsResult, error) {
	filter := strings.TrimSpace(req.Filter)
	limit := normalizeEventLimit(req.Limit)
	var out NetworkRequestsResult
	err := c.withConnectionPage(ctx, session, func(conn *browserConnection, _ playwright.Page) error {
		conn.eventsMu.Lock()
		defer conn.eventsMu.Unlock()
		requests := make([]NetworkRequest, 0, limit)
		for i := len(conn.network) - 1; i >= 0 && len(requests) < limit; i-- {
			item := conn.network[i]
			if filter == "" || strings.Contains(item.URL, filter) {
				requests = append(requests, item)
			}
		}
		reverseNetworkRequests(requests)
		out = NetworkRequestsResult{SessionID: session.ID, Requests: requests}
		return nil
	})
	return out, err
}

// Evaluate 执行页面 JavaScript 表达式。
func (c *PlaywrightController) Evaluate(ctx context.Context, session SessionRef, req EvaluateRequest) (EvaluateResult, error) {
	expression := strings.TrimSpace(req.Expression)
	if expression == "" {
		return EvaluateResult{}, NewControlError(CodeInvalidArgument, "expression is required", nil)
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

// SetViewport 更新当前目标页面的 viewport 尺寸。
func (c *PlaywrightController) SetViewport(ctx context.Context, session SessionRef, req ViewportRequest) (ViewportResult, error) {
	if req.Width < 320 || req.Width > 10000 {
		return ViewportResult{}, NewControlError(CodeInvalidArgument, "width must be between 320 and 10000", nil)
	}
	if req.Height < 240 || req.Height > 10000 {
		return ViewportResult{}, NewControlError(CodeInvalidArgument, "height must be between 240 and 10000", nil)
	}
	out := ViewportResult{SessionID: session.ID, Width: req.Width, Height: req.Height}
	err := c.withPage(ctx, session, func(page playwright.Page) error {
		if err := page.SetViewportSize(req.Width, req.Height); err != nil {
			return err
		}
		if actual := page.ViewportSize(); actual != nil {
			out.Width = actual.Width
			out.Height = actual.Height
		}
		return nil
	})
	return out, err
}

func resolveNavigateURL(session SessionRef, req NavigateRequest) (string, error) {
	base, err := url.Parse(strings.TrimSpace(session.TargetURL))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", NewControlError(CodeNavigationDenied, "session target URL is invalid", nil)
	}
	raw := strings.TrimSpace(req.URL)
	if raw == "" {
		raw = strings.TrimSpace(req.Path)
	}
	if raw == "" {
		return "", NewControlError(CodeInvalidArgument, "url or path is required", nil)
	}
	next, err := url.Parse(raw)
	if err != nil {
		return "", NewControlError(CodeInvalidArgument, "url is invalid", nil)
	}
	if !next.IsAbs() {
		next = base.ResolveReference(next)
	}
	if next.Scheme != base.Scheme || next.Host != base.Host {
		return "", NewControlError(CodeNavigationDenied, "browser_navigate only supports same-origin full page navigation", nil)
	}
	return next.String(), nil
}

func normalizeEventLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > browserEventBufferLimit {
		return browserEventBufferLimit
	}
	return limit
}

func appendRing[T any](items []T, item T, limit int) []T {
	items = append(items, item)
	if len(items) <= limit {
		return items
	}
	return append([]T(nil), items[len(items)-limit:]...)
}

func reverseConsoleLogs(items []ConsoleLog) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func reverseNetworkRequests(items []NetworkRequest) {
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
}

func parseWaitUntil(value string) (*playwright.WaitUntilState, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return nil, nil
	case "load":
		return playwright.WaitUntilStateLoad, nil
	case "domcontentloaded":
		return playwright.WaitUntilStateDomcontentloaded, nil
	case "networkidle":
		return playwright.WaitUntilStateNetworkidle, nil
	case "commit":
		return playwright.WaitUntilStateCommit, nil
	default:
		return nil, NewControlError(CodeInvalidArgument, "wait_until must be load, domcontentloaded, networkidle, or commit", nil)
	}
}

func parseWaitForSelectorState(value string) (*playwright.WaitForSelectorState, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return nil, nil
	case "attached":
		return playwright.WaitForSelectorStateAttached, nil
	case "detached":
		return playwright.WaitForSelectorStateDetached, nil
	case "visible":
		return playwright.WaitForSelectorStateVisible, nil
	case "hidden":
		return playwright.WaitForSelectorStateHidden, nil
	default:
		return nil, NewControlError(CodeInvalidArgument, "state must be attached, detached, visible, or hidden", nil)
	}
}

func truncate(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit]
}

func (c *PlaywrightController) withPage(ctx context.Context, session SessionRef, fn func(playwright.Page) error) error {
	return c.withConnectionPage(ctx, session, func(_ *browserConnection, page playwright.Page) error {
		return fn(page)
	})
}

func (c *PlaywrightController) withConnectionPage(ctx context.Context, session SessionRef, fn func(*browserConnection, playwright.Page) error) error {
	browserWS := strings.TrimSpace(session.BrowserWS)
	if browserWS == "" {
		return NewControlError(CodeInvalidArgument, "browser websocket endpoint is required", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn, err := c.connection(browserWS)
	if err != nil {
		return err
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	page, err := selectPage(conn.browser, session)
	if err != nil {
		c.CloseBrowserConnection(browserWS)
		return WrapControlError(CodeSessionNotFound, "browser page not found", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	conn.attachPageEvents(page, c.now)
	conn.lastUsed = c.now()
	return fn(conn, page)
}

func (c *PlaywrightController) connection(browserWS string) (*browserConnection, error) {
	if err := c.ensureDriver(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if conn, ok := c.connections[browserWS]; ok {
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	pw, err := playwright.Run(c.runOptions)
	if err != nil {
		return nil, WrapControlError(CodeCDPConnectionFailed, "start playwright driver", err)
	}
	browser, err := pw.Chromium.ConnectOverCDP(browserWS)
	if err != nil {
		_ = pw.Stop()
		return nil, WrapControlError(CodeCDPConnectionFailed, "connect over cdp", err)
	}
	conn := &browserConnection{pw: pw, browser: browser, lastUsed: c.now(), eventsAttached: map[playwright.Page]bool{}}
	c.mu.Lock()
	if existing, ok := c.connections[browserWS]; ok {
		c.mu.Unlock()
		_ = pw.Stop()
		return existing, nil
	}
	c.connections[browserWS] = conn
	c.mu.Unlock()
	return conn, nil
}

func (conn *browserConnection) attachPageEvents(page playwright.Page, now func() time.Time) {
	conn.eventsMu.Lock()
	if conn.eventsAttached == nil {
		conn.eventsAttached = map[playwright.Page]bool{}
	}
	if conn.eventsAttached[page] {
		conn.eventsMu.Unlock()
		return
	}
	conn.eventsAttached[page] = true
	conn.eventsMu.Unlock()

	page.OnConsole(func(message playwright.ConsoleMessage) {
		conn.appendConsole(ConsoleLog{
			Time:  now().UTC(),
			Level: strings.ToLower(strings.TrimSpace(message.Type())),
			Text:  message.Text(),
		})
	})
	page.OnResponse(func(response playwright.Response) {
		req := response.Request()
		conn.appendNetwork(NetworkRequest{
			Time:   now().UTC(),
			Method: req.Method(),
			URL:    response.URL(),
			Status: response.Status(),
		})
	})
	page.OnRequestFailed(func(req playwright.Request) {
		errText := ""
		if err := req.Failure(); err != nil {
			errText = err.Error()
		}
		conn.appendNetwork(NetworkRequest{
			Time:   now().UTC(),
			Method: req.Method(),
			URL:    req.URL(),
			Error:  errText,
		})
	})
	page.OnClose(func(closed playwright.Page) {
		conn.detachPageEvents(closed)
	})
}

func (conn *browserConnection) detachPageEvents(page playwright.Page) {
	conn.eventsMu.Lock()
	defer conn.eventsMu.Unlock()
	delete(conn.eventsAttached, page)
}

func (conn *browserConnection) appendConsole(entry ConsoleLog) {
	conn.eventsMu.Lock()
	defer conn.eventsMu.Unlock()
	conn.console = appendRing(conn.console, entry, browserEventBufferLimit)
}

func (conn *browserConnection) appendNetwork(entry NetworkRequest) {
	conn.eventsMu.Lock()
	defer conn.eventsMu.Unlock()
	conn.network = appendRing(conn.network, entry, browserEventBufferLimit)
}

func (c *PlaywrightController) ensureDriver() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.driverReady {
		return nil
	}
	if err := playwright.Install(c.runOptions); err != nil {
		return WrapControlError(CodeCDPConnectionFailed, "install playwright driver", err)
	}
	c.driverReady = true
	return nil
}

// CloseBrowserConnection 释放某个 CDP endpoint 对应的 Playwright driver 连接。
func (c *PlaywrightController) CloseBrowserConnection(browserWS string) {
	browserWS = strings.TrimSpace(browserWS)
	if browserWS == "" {
		return
	}
	c.mu.Lock()
	conn, ok := c.connections[browserWS]
	if ok {
		delete(c.connections, browserWS)
	}
	c.mu.Unlock()
	if ok {
		_ = conn.pw.Stop()
	}
}

// Close 释放所有缓存的 Playwright driver 连接。
func (c *PlaywrightController) Close() error {
	c.mu.Lock()
	conns := make([]*browserConnection, 0, len(c.connections))
	for browserWS, conn := range c.connections {
		conns = append(conns, conn)
		delete(c.connections, browserWS)
	}
	c.mu.Unlock()
	for _, conn := range conns {
		_ = conn.pw.Stop()
	}
	return nil
}

func selectPage(browser playwright.Browser, session SessionRef) (playwright.Page, error) {
	var first playwright.Page
	for _, context := range browser.Contexts() {
		for _, page := range context.Pages() {
			if first == nil {
				first = page
			}
			if pageURLMatches(page.URL(), session.TargetURL) {
				return page, nil
			}
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("browser page not found")
}

func pageURLMatches(candidate string, expected string) bool {
	candidate = strings.TrimSpace(candidate)
	expected = strings.TrimSpace(expected)
	if candidate == "" || expected == "" {
		return false
	}
	if candidate == expected {
		return true
	}
	candidateURL, candidateErr := url.Parse(candidate)
	expectedURL, expectedErr := url.Parse(expected)
	if candidateErr != nil || expectedErr != nil {
		return false
	}
	normalizeURLForCompare(candidateURL)
	normalizeURLForCompare(expectedURL)
	return candidateURL.String() == expectedURL.String()
}

func normalizeURLForCompare(value *url.URL) {
	if value.Path == "" {
		value.Path = "/"
	}
	value.Fragment = ""
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return false
}

func floatValue(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	default:
		return 0
	}
}

func snapshotFocusedFromValue(value any) *SnapshotElement {
	data, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	element := buildSnapshotElement(snapshotElementInputFromMap(data))
	if element.Selector == "" {
		return nil
	}
	return &element
}

func snapshotElementsFromValue(value any) []SnapshotElement {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	elements := make([]SnapshotElement, 0, len(items))
	for _, item := range items {
		data, ok := item.(map[string]any)
		if !ok {
			continue
		}
		element := buildSnapshotElement(snapshotElementInputFromMap(data))
		if element.Selector == "" {
			continue
		}
		elements = append(elements, element)
	}
	return elements
}

func snapshotElementInputFromMap(data map[string]any) snapshotElementInput {
	return snapshotElementInput{
		Tag:        stringValue(data["tag"]),
		Role:       stringValue(data["role"]),
		Text:       stringValue(data["text"]),
		AriaLabel:  stringValue(data["ariaLabel"]),
		DataTest:   stringValue(data["dataTest"]),
		DataTestID: stringValue(data["dataTestID"]),
		ID:         stringValue(data["id"]),
		NameAttr:   stringValue(data["nameAttr"]),
		Type:       stringValue(data["type"]),
		Visible:    boolValue(data["visible"]),
		Enabled:    boolValue(data["enabled"]),
		Bounds:     snapshotBoundsFromValue(data["bounds"]),
	}
}

func snapshotBoundsFromValue(value any) *SnapshotBounds {
	data, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	width := floatValue(data["width"])
	height := floatValue(data["height"])
	if width <= 0 || height <= 0 {
		return nil
	}
	return &SnapshotBounds{
		X:      floatValue(data["x"]),
		Y:      floatValue(data["y"]),
		Width:  width,
		Height: height,
	}
}
