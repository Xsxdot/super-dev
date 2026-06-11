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
