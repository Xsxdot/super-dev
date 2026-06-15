// portalloc_test.go 验证 prearm-listen 调试端口分配。
//
// 职责：锁定本机空闲 TCP 端口分配函数的基本契约。
// 边界：不验证调用方重新绑定端口时的 TOCTOU 竞争处理。
package codedebug

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAllocateFreePortReturnsUsablePort(t *testing.T) {
	port, err := AllocateFreePort()
	require.NoError(t, err)
	assert.Greater(t, port, 0)
	assert.LessOrEqual(t, port, 65535)
}

func TestAllocateFreePortDistinct(t *testing.T) {
	a, err := AllocateFreePort()
	require.NoError(t, err)
	b, err := AllocateFreePort()
	require.NoError(t, err)
	// 连续两次大概率不同；至少都有效。
	assert.Greater(t, a, 0)
	assert.Greater(t, b, 0)
}
