// playwright_test.go 验证 Playwright 控制器的本地前置校验。
//
// 职责：
//   - 覆盖无需真实浏览器即可判断的参数错误
//   - 防止空 selector/expression 被错误地推进到 Playwright/CDP 层
//
// 边界：
//   - 不启动 Playwright driver
//   - 不连接真实 CDP endpoint
package browsercontrol

import (
	"context"
	"testing"

	playwright "github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlaywrightControllerRejectsMissingBrowserEndpoint(t *testing.T) {
	controller := NewPlaywrightController()

	_, err := controller.Snapshot(context.Background(), SessionRef{ID: "brs_1"}, SnapshotRequest{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "browser websocket endpoint")
}

func TestPlaywrightControllerRejectsEmptyActionInputsBeforeConnecting(t *testing.T) {
	controller := NewPlaywrightController()
	session := SessionRef{ID: "brs_1", BrowserWS: "ws://127.0.0.1:9222/devtools/browser/b"}

	_, clickErr := controller.Click(context.Background(), session, ClickRequest{})
	_, evalErr := controller.Evaluate(context.Background(), session, EvaluateRequest{})

	require.Error(t, clickErr)
	assert.Contains(t, clickErr.Error(), "selector is required")
	require.Error(t, evalErr)
	assert.Contains(t, evalErr.Error(), "expression is required")
}

func TestNewPlaywrightControllerUsesLocalDriverDir(t *testing.T) {
	driverDir := t.TempDir()

	controller := NewPlaywrightController(driverDir)

	require.NotNil(t, controller.runOptions)
	assert.Equal(t, driverDir, controller.runOptions.DriverDirectory)
	assert.True(t, controller.runOptions.SkipInstallBrowsers)
	assert.False(t, controller.runOptions.Verbose)
}

func TestBrowserControlErrorCarriesCode(t *testing.T) {
	err := NewControlError(CodeEvaluateDisabled, "browser_evaluate is disabled", nil)

	var controlErr ControlError
	require.ErrorAs(t, err, &controlErr)
	assert.Equal(t, CodeEvaluateDisabled, controlErr.Code)
	assert.Equal(t, "browser_evaluate is disabled", controlErr.Message)
}

func TestScreenshotRejectsOversizedPNG(t *testing.T) {
	err := NewControlError(CodeScreenshotTooLarge, "browser screenshot is too large", map[string]any{"limit_bytes": 1572864})

	var controlErr ControlError
	require.ErrorAs(t, err, &controlErr)
	assert.Equal(t, CodeScreenshotTooLarge, controlErr.Code)
	assert.Equal(t, map[string]any{"limit_bytes": 1572864}, controlErr.Data)
}

func TestResolveNavigateURLAllowsSameOriginPath(t *testing.T) {
	got, err := resolveNavigateURL(SessionRef{TargetURL: "http://127.0.0.1:5173/"}, NavigateRequest{Path: "/users"})

	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:5173/users", got)
}

func TestResolveNavigateURLRejectsCrossOrigin(t *testing.T) {
	_, err := resolveNavigateURL(SessionRef{TargetURL: "http://127.0.0.1:5173/"}, NavigateRequest{URL: "https://example.com/"})

	require.Error(t, err)
	var controlErr ControlError
	require.ErrorAs(t, err, &controlErr)
	assert.Equal(t, CodeNavigationDenied, controlErr.Code)
}

func TestPageURLMatchesEquivalentLocalURL(t *testing.T) {
	assert.True(t, pageURLMatches("http://127.0.0.1:5173/", "http://127.0.0.1:5173"))
	assert.True(t, pageURLMatches("http://127.0.0.1:5173/?ready=1#top", "http://127.0.0.1:5173/?ready=1"))
	assert.False(t, pageURLMatches("http://127.0.0.1:5173/users", "http://127.0.0.1:5173/settings"))
}

func TestAppendRingKeepsRecentItems(t *testing.T) {
	got := []int{}
	for i := 0; i < 5; i++ {
		got = appendRing(got, i, 3)
	}

	assert.Equal(t, []int{2, 3, 4}, got)
}

func TestBuildElementSelectorPrefersStableAttributes(t *testing.T) {
	tests := []struct {
		name string
		el   snapshotElementInput
		want string
	}{
		{name: "data test", el: snapshotElementInput{Tag: "button", DataTest: "save"}, want: `[data-test="save"]`},
		{name: "aria label", el: snapshotElementInput{Tag: "button", AriaLabel: "Save"}, want: `button[aria-label="Save"]`},
		{name: "name", el: snapshotElementInput{Tag: "input", NameAttr: "q"}, want: `input[name="q"]`},
		{name: "id", el: snapshotElementInput{Tag: "select", ID: "env"}, want: `#env`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, buildElementSelector(tt.el))
		})
	}
}

func TestRedactSnapshotElementText(t *testing.T) {
	assert.Equal(t, "[redacted]", redactSnapshotText("token=abc"))
	assert.Equal(t, "[redacted]", redactSnapshotText("password: hunter2"))
	assert.Equal(t, "Password", redactSnapshotText("Password"))
	assert.Equal(t, "Reset token expired", redactSnapshotText("Reset token expired"))
	assert.Equal(t, "Cookie 设置", redactSnapshotText("Cookie 设置"))
	assert.Equal(t, "Save", redactSnapshotText("Save"))
}

func TestDetachPageEventsRemovesClosedPageFromAttachmentMap(t *testing.T) {
	conn := &browserConnection{eventsAttached: map[playwright.Page]bool{}}
	var page playwright.Page
	conn.eventsAttached[page] = true

	conn.detachPageEvents(page)

	assert.Empty(t, conn.eventsAttached)
}

func TestNormalizeSnapshotMaxElementsDefaultsAndCaps(t *testing.T) {
	assert.Equal(t, defaultSnapshotMaxElements, normalizeSnapshotMaxElements(0))
	assert.Equal(t, 25, normalizeSnapshotMaxElements(25))
	assert.Equal(t, maxSnapshotElements, normalizeSnapshotMaxElements(maxSnapshotElements+1))
}

func TestProbeSnapshotCapabilityReturnsDOMFallback(t *testing.T) {
	controller := NewPlaywrightController()

	capability, err := controller.ProbeSnapshotCapability(context.Background(), SessionRef{ID: "brs_1"})

	require.NoError(t, err)
	assert.False(t, capability.AccessibilityTree)
	assert.True(t, capability.DOMFallback)
	assert.Contains(t, capability.Message, "DOM fallback")
}
