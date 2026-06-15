// jsdebug_install_test.go 验证 js-debug standalone DAP server 的本地路径定位。
//
// 职责：
//   - 覆盖 DataDir 下 js-debug/src/dapDebugServer.js 存在时返回绝对入口
//   - 覆盖未落地时返回空路径
//
// 边界：
//   - 不下载 js-debug，不启动 Node
package codedebug

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJSDebugServerPathWhenPresent(t *testing.T) {
	dir := t.TempDir()
	serverPath := filepath.Join(dir, "js-debug", "src", "dapDebugServer.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(serverPath), 0o755))
	require.NoError(t, os.WriteFile(serverPath, []byte("// js-debug"), 0o644))

	got := JSDebugServerPath(dir)

	assert.Equal(t, serverPath, got)
}

func TestJSDebugServerPathWhenMissing(t *testing.T) {
	got := JSDebugServerPath(t.TempDir())

	assert.Equal(t, "", got)
}
