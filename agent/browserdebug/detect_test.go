// detect_test.go 验证本机调试浏览器探测逻辑。
//
// 职责：
//   - 覆盖候选浏览器路径到 BrowserRecord 的转换
//   - 确认不存在或不可执行的候选不会进入可用列表
//
// 边界：
//   - 不依赖当前机器真实安装的浏览器
//   - 不启动浏览器进程
package browserdebug

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDetectBrowsersFromCandidatesReturnsExecutableBrowsers(t *testing.T) {
	dir := t.TempDir()
	chrome := filepath.Join(dir, "chrome")
	edge := filepath.Join(dir, "edge")
	require.NoError(t, os.WriteFile(chrome, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.WriteFile(edge, []byte("#!/bin/sh\n"), 0o644))

	got := DetectBrowsersFromCandidates([]BrowserCandidate{
		{ID: "chrome", Name: "Chrome", ExecutablePath: chrome},
		{ID: "edge", Name: "Edge", ExecutablePath: edge},
		{ID: "missing", Name: "Missing", ExecutablePath: filepath.Join(dir, "missing")},
	})

	require.Len(t, got, 1)
	assert.Equal(t, "chrome", got[0].ID)
	assert.Equal(t, chrome, got[0].ExecutablePath)
	assert.True(t, got[0].Available)
}
