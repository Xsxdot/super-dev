// providers_test.go 验证各语言 DAP provider 的命令构造。
//
// 职责：
//   - 覆盖 Go、Python 开箱 provider 的 adapter 命令
//   - 覆盖 Node experimental provider 缺少 adapter_command 时的提示错误
//
// 边界：
//   - 不校验真实调试器是否安装
package codedebug

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestGoProviderBuildsDelveDAPCommand(t *testing.T) {
	provider := NewGoProvider()
	cmd, err := provider.AdapterCommand(LaunchConfig{AdapterPort: 39001})

	require.NoError(t, err)
	assert.Equal(t, "dlv", cmd.Name)
	assert.Equal(t, []string{"dap", "--listen=127.0.0.1:39001"}, cmd.Args)
}

func TestPythonProviderBuildsDebugpyAdapterCommand(t *testing.T) {
	provider := NewPythonProvider("python3")
	cmd, err := provider.AdapterCommand(LaunchConfig{AdapterPort: 39002})

	require.NoError(t, err)
	assert.Equal(t, "python3", cmd.Name)
	assert.Equal(t, []string{"-m", "debugpy.adapter", "--host", "127.0.0.1", "--port", "39002"}, cmd.Args)
}

func TestNodeProviderRequiresAdapterCommand(t *testing.T) {
	provider := NewNodeProvider()
	_, err := provider.AdapterCommand(LaunchConfig{Provider: model.CodeDebugProviderNode})

	require.ErrorIs(t, err, ErrAdapterUnavailable)
}
