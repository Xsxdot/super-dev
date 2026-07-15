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

func TestExecutableFileAvailableForOSUsesPlatformExecutionSemantics(t *testing.T) {
	tests := []struct {
		name           string
		goos           string
		executablePath string
		mode           os.FileMode
		want           bool
	}{
		{name: "Windows EXE does not require Unix mode bits", goos: "windows", executablePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, mode: 0o666, want: true},
		{name: "Windows extension is case insensitive", goos: "windows", executablePath: `C:\Program Files\Microsoft\Edge\msedge.EXE`, mode: 0o666, want: true},
		{name: "Windows command extension is executable", goos: "windows", executablePath: `C:\Tools\browser.cmd`, mode: 0o666, want: true},
		{name: "Windows rejects non executable extension", goos: "windows", executablePath: `C:\Tools\browser.txt`, mode: 0o777, want: false},
		{name: "Windows rejects extensionless file", goos: "windows", executablePath: `C:\Tools\browser`, mode: 0o777, want: false},
		{name: "Windows rejects directory", goos: "windows", executablePath: `C:\Tools\browser.exe`, mode: os.ModeDir | 0o777, want: false},
		{name: "Windows rejects non regular file", goos: "windows", executablePath: `C:\Tools\browser.exe`, mode: os.ModeNamedPipe | 0o666, want: false},
		{name: "Unix accepts regular executable", goos: "darwin", executablePath: "/Applications/Chrome", mode: 0o755, want: true},
		{name: "Unix rejects regular non executable", goos: "linux", executablePath: "/opt/chrome", mode: 0o644, want: false},
		{name: "Unix rejects EXE without execute bit", goos: "linux", executablePath: "/opt/chrome.exe", mode: 0o644, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, executableFileAvailableForOS(test.goos, test.executablePath, test.mode))
		})
	}
}
