// jsdebug_install.go 定位随 agent 落地的 @vscode/js-debug standalone DAP server。
//
// 职责：
//   - 在 agent 数据目录下解析 js-debug/src/dapDebugServer.js 路径
//   - 仅在入口文件存在时返回可用路径
//
// 边界：
//   - 不下载、不解压 js-debug
//   - 实际落地由部署/打包流程把 js-debug-dap 解压到 <dataDir>/js-debug
package codedebug

import (
	"os"
	"path/filepath"
)

// JSDebugServerPath 返回 dataDir 下 js-debug standalone DAP server 入口；不存在返回空。
func JSDebugServerPath(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	path := filepath.Join(dataDir, "js-debug", "src", "dapDebugServer.js")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}
