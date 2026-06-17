// node_provider_internal_test.go 验证 Node provider 的平台注入 helper。
//
// 职责：
//   - 锁定 Windows --inspect prearm 的 argv/env 生成规则
//   - 锁定 Unix 与 Windows debugger readiness 差异
//
// 边界：
//   - 不依赖当前运行平台
//   - 不启动 Node 进程或 js-debug adapter
package langruntime

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestNodePrearmArgsForWindows(t *testing.T) {
	assert.Equal(t, []string{"--inspect=0", "--enable-source-maps"}, nodePrearmArgs("windows", []string{"--enable-source-maps"}))
	assert.Equal(t, []string{"--inspect=9230"}, nodePrearmArgs("windows", []string{"--inspect=9230"}))
	assert.Equal(t, []string{"--enable-source-maps"}, nodePrearmArgs("linux", []string{"--enable-source-maps"}))
}

func TestNodePrearmEnvForWindows(t *testing.T) {
	got := nodePrearmEnv("windows", map[string]string{"NODE_OPTIONS": "--enable-source-maps"})
	assert.Equal(t, "--enable-source-maps --inspect=0", got["NODE_OPTIONS"])

	got = nodePrearmEnv("windows", map[string]string{"NODE_OPTIONS": "--inspect=9230"})
	assert.Equal(t, "--inspect=9230", got["NODE_OPTIONS"])

	got = nodePrearmEnv("linux", map[string]string{"NODE_OPTIONS": "--enable-source-maps"})
	assert.Equal(t, "--enable-source-maps", got["NODE_OPTIONS"])
}

func TestNodeDebuggerSpecForPlatform(t *testing.T) {
	win := nodeDebuggerSpec("windows")
	assert.Equal(t, model.CodeDebugProviderNode, win.Adapter)
	assert.Equal(t, ReadinessPrearmListen, win.Readiness)
	assert.Empty(t, win.Signal)

	unix := nodeDebuggerSpec("linux")
	assert.Equal(t, ReadinessSignalAttach, unix.Readiness)
	assert.Equal(t, "SIGUSR1", unix.Signal)
}
