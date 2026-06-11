// Command browser-debug-smoke runs a real local frontend browser debug smoke test.
//
// 职责：
//   - 通过已运行的 SuperDev agent 打开真实 Chromium/Arc 调试会话
//   - 通过 MCP browser tools 验证 snapshot/click/type/screenshot/console
//   - 输出 JSON lines，便于人工和自动化排查失败步骤
//
// 边界：
//   - 不进入普通 CI
//   - 不自动安装浏览器
//   - 不支持远端 frontend 或 tunnel 页面
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/xsxdot/super-dev/agent/browsercontrol"
	"github.com/xsxdot/super-dev/agent/mcp"
)

const defaultAgentURL = "http://127.0.0.1:57018"

type smokeConfig struct {
	AgentURL     string
	BrowserID    string
	DeploymentID string
	SkipClose    bool
}

type smokeStep struct {
	Step              string `json:"step"`
	OK                bool   `json:"ok"`
	SessionID         string `json:"session_id,omitempty"`
	Title             string `json:"title,omitempty"`
	Selector          string `json:"selector,omitempty"`
	Bytes             int    `json:"bytes,omitempty"`
	Count             int    `json:"count,omitempty"`
	AccessibilityTree bool   `json:"accessibility_tree,omitempty"`
	DOMFallback       bool   `json:"dom_fallback,omitempty"`
	Message           string `json:"message,omitempty"`
	Skipped           bool   `json:"skipped,omitempty"`
	Error             string `json:"error,omitempty"`
}

func main() {
	cfg, err := loadSmokeConfigFromEnv()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runSmoke(ctx, cfg, os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func loadSmokeConfigFromEnv() (smokeConfig, error) {
	cfg := smokeConfig{
		AgentURL:     strings.TrimSpace(os.Getenv("SUPERDEV_AGENT_URL")),
		BrowserID:    strings.TrimSpace(os.Getenv("SUPERDEV_BROWSER_ID")),
		DeploymentID: strings.TrimSpace(os.Getenv("SUPERDEV_DEPLOYMENT_ID")),
		SkipClose:    parseBoolEnv(os.Getenv("SUPERDEV_SKIP_CLOSE")),
	}
	if cfg.AgentURL == "" {
		cfg.AgentURL = defaultAgentURL
	}
	if cfg.DeploymentID == "" {
		return smokeConfig{}, errors.New("SUPERDEV_DEPLOYMENT_ID is required")
	}
	return cfg, nil
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func runSmoke(ctx context.Context, cfg smokeConfig, out io.Writer) error {
	client := mcp.NewHTTPAgentClient(cfg.AgentURL, &http.Client{Timeout: 20 * time.Second})
	server := mcp.NewServer(client)
	openDevtools := true
	openArgs := map[string]any{
		"deployment_id": cfg.DeploymentID,
		"browser_id":    cfg.BrowserID,
		"open_devtools": openDevtools,
	}
	openResult, err := callSmokeTool(ctx, server, out, "open", "open_browser_debug_session", openArgs)
	if err != nil {
		return err
	}
	session, err := decodeToolData[mcp.BrowserSession](openResult, "session")
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "open", OK: false, Error: err.Error()})
		return err
	}
	if err := emitStep(out, smokeStep{Step: "open", OK: true, SessionID: session.ID}); err != nil {
		return err
	}

	controller := browsercontrol.NewPlaywrightController()
	capability, err := controller.ProbeSnapshotCapability(ctx, browsercontrol.SessionRef{
		ID:        session.ID,
		TargetURL: session.TargetURL,
		BrowserWS: session.BrowserWS,
	})
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "accessibility_spike", OK: false, Error: err.Error()})
		return err
	}
	if err := emitStep(out, smokeStep{
		Step:              "accessibility_spike",
		OK:                true,
		AccessibilityTree: capability.AccessibilityTree,
		DOMFallback:       capability.DOMFallback,
		Message:           capability.Message,
	}); err != nil {
		return err
	}

	if err := smokeWaitForSelector(ctx, server, out, session.ID, "body", "visible"); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	if err := smokeWaitForSelector(ctx, server, out, session.ID, "input,textarea,[role=\"textbox\"]", "visible"); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	snapshot, err := smokeSnapshot(ctx, server, out, session.ID)
	if err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	if err := smokeClick(ctx, server, out, session.ID, snapshot); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	if err := smokeType(ctx, server, out, session.ID, snapshot); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	if err := smokeScreenshot(ctx, server, out, session.ID); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	if err := smokeConsole(ctx, server, out, session.ID); err != nil {
		_ = closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
		return err
	}
	return closeSmokeSession(ctx, server, out, session.ID, cfg.SkipClose)
}

func smokeWaitForSelector(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string, selector string, state string) error {
	_, err := callSmokeTool(ctx, server, out, "wait_for_selector", "browser_wait_for_selector", map[string]any{
		"session_id": sessionID,
		"selector":   selector,
		"state":      state,
		"timeout_ms": 5000,
	})
	if err != nil {
		return err
	}
	return emitStep(out, smokeStep{Step: "wait_for_selector", OK: true, Selector: selector})
}

func smokeSnapshot(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string) (mcp.BrowserSnapshot, error) {
	result, err := callSmokeTool(ctx, server, out, "snapshot", "browser_snapshot", map[string]any{
		"session_id":   sessionID,
		"max_elements": 50,
	})
	if err != nil {
		return mcp.BrowserSnapshot{}, err
	}
	snapshot, err := decodeToolData[mcp.BrowserSnapshot](result, "snapshot")
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "snapshot", OK: false, Error: err.Error()})
		return mcp.BrowserSnapshot{}, err
	}
	return snapshot, emitStep(out, smokeStep{Step: "snapshot", OK: true, Title: snapshot.Title})
}

func smokeClick(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string, snapshot mcp.BrowserSnapshot) error {
	selector := selectClickSelector(snapshot)
	_, err := callSmokeTool(ctx, server, out, "click", "browser_click", map[string]any{
		"session_id": sessionID,
		"selector":   selector,
	})
	if err != nil {
		return err
	}
	return emitStep(out, smokeStep{Step: "click", OK: true, Selector: selector})
}

func smokeType(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string, snapshot mcp.BrowserSnapshot) error {
	selector := selectTypeSelector(snapshot)
	if selector == "" {
		err := errors.New("snapshot did not expose an enabled textbox for browser_type")
		_ = emitStep(out, smokeStep{Step: "type", OK: false, Error: err.Error()})
		return err
	}
	_, err := callSmokeTool(ctx, server, out, "type", "browser_type", map[string]any{
		"session_id": sessionID,
		"selector":   selector,
		"text":       "superdev-smoke",
		"fill":       true,
	})
	if err != nil {
		return err
	}
	return emitStep(out, smokeStep{Step: "type", OK: true, Selector: selector})
}

func smokeScreenshot(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string) error {
	result, err := callSmokeTool(ctx, server, out, "screenshot", "browser_screenshot", map[string]any{
		"session_id": sessionID,
	})
	if err != nil {
		return err
	}
	screenshot, err := decodeToolData[mcp.BrowserScreenshot](result, "screenshot")
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "screenshot", OK: false, Error: err.Error()})
		return err
	}
	data, err := base64.StdEncoding.DecodeString(screenshot.DataBase64)
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "screenshot", OK: false, Error: err.Error()})
		return err
	}
	return emitStep(out, smokeStep{Step: "screenshot", OK: true, Bytes: len(data)})
}

func smokeConsole(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string) error {
	result, err := callSmokeTool(ctx, server, out, "console", "browser_console_logs", map[string]any{
		"session_id": sessionID,
		"limit":      20,
	})
	if err != nil {
		return err
	}
	logs, err := decodeToolData[mcp.BrowserConsoleLogsResult](result, "result")
	if err != nil {
		_ = emitStep(out, smokeStep{Step: "console", OK: false, Error: err.Error()})
		return err
	}
	return emitStep(out, smokeStep{Step: "console", OK: true, Count: len(logs.Logs)})
}

func closeSmokeSession(ctx context.Context, server *mcp.Server, out io.Writer, sessionID string, skip bool) error {
	if skip {
		return emitStep(out, smokeStep{Step: "close", OK: true, SessionID: sessionID, Skipped: true})
	}
	_, err := callSmokeTool(ctx, server, out, "close", "close_browser_debug_session", map[string]any{"session_id": sessionID})
	if err != nil {
		return err
	}
	return emitStep(out, smokeStep{Step: "close", OK: true, SessionID: sessionID})
}

func selectClickSelector(snapshot mcp.BrowserSnapshot) string {
	for _, element := range snapshot.Elements {
		if element.Visible && element.Enabled && strings.TrimSpace(element.Selector) != "" {
			return element.Selector
		}
	}
	return "body"
}

func selectTypeSelector(snapshot mcp.BrowserSnapshot) string {
	for _, element := range snapshot.Elements {
		if element.Visible && element.Enabled && element.Role == "textbox" && strings.TrimSpace(element.Selector) != "" {
			return element.Selector
		}
	}
	return ""
}

func callSmokeTool(ctx context.Context, server *mcp.Server, out io.Writer, step string, name string, args any) (mcp.CallToolResult, error) {
	data, err := json.Marshal(args)
	if err != nil {
		_ = emitStep(out, smokeStep{Step: step, OK: false, Error: err.Error()})
		return mcp.CallToolResult{}, err
	}
	result, err := server.CallToolForSmoke(ctx, name, data)
	if err != nil {
		_ = emitStep(out, smokeStep{Step: step, OK: false, Error: err.Error()})
		return mcp.CallToolResult{}, err
	}
	if result.IsError {
		err := errors.New(toolMessage(result))
		_ = emitStep(out, smokeStep{Step: step, OK: false, Error: err.Error()})
		return mcp.CallToolResult{}, err
	}
	return result, nil
}

func decodeToolData[T any](result mcp.CallToolResult, key string) (T, error) {
	var zero T
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return zero, err
	}
	var payload struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return zero, err
	}
	raw, ok := payload.Data[key]
	if !ok {
		return zero, fmt.Errorf("tool response missing data.%s", key)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func emitStep(out io.Writer, step smokeStep) error {
	return json.NewEncoder(out).Encode(step)
}

func toolMessage(result mcp.CallToolResult) string {
	if len(result.Content) == 0 {
		return "tool returned error"
	}
	if text := strings.TrimSpace(result.Content[0]["text"]); text != "" {
		return text
	}
	return "tool returned error"
}
