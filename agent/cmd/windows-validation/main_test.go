// main_test.go 验证 Windows validation CLI 的进程内凭据交接。
//
// 职责：
//   - 证明人类输入只从专用环境读取一次并立即从当前进程环境删除
//
// 边界：
//   - 不解析真实命令行，不执行 Windows campaign
//   - 不使用或记录真实凭据
package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConsumeDebugCredentialEnvironmentReadsOnceAndUnsets(t *testing.T) {
	const value = "human-test-secret"
	require.NoError(t, os.Setenv(debugCredentialEnvName, value))
	t.Cleanup(func() { _ = os.Unsetenv(debugCredentialEnvName) })

	assert.Equal(t, value, consumeDebugCredentialEnvironment())
	_, exists := os.LookupEnv(debugCredentialEnvName)
	assert.False(t, exists)
	assert.Empty(t, consumeDebugCredentialEnvironment())
}
