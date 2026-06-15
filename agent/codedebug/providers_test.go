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

func TestGoProviderPassesLaunchEnvToDelveAdapter(t *testing.T) {
	provider := NewGoProvider()
	cmd, err := provider.AdapterCommand(LaunchConfig{
		AdapterPort: 39001,
		Env:         map[string]string{"GOCACHE": "/tmp/superdev-gocache"},
	})

	require.NoError(t, err)
	assert.Equal(t, "/tmp/superdev-gocache", cmd.Env["GOCACHE"])
}

func TestGoProviderStartsDelveInDebuggeeWorkingDir(t *testing.T) {
	provider := NewGoProvider()
	cmd, err := provider.AdapterCommand(LaunchConfig{
		AdapterPort: 39001,
		WorkingDir:  "/tmp/project/server",
	})

	require.NoError(t, err)
	assert.Equal(t, "/tmp/project/server", cmd.WorkDir)
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

func TestProviderAttachCapability(t *testing.T) {
	if NewGoProvider().AttachCapability() != AttachModePID {
		t.Fatal("Go should support pid-attach")
	}
	if NewPythonProvider("python3").AttachCapability() != AttachModeListen {
		t.Fatal("Python should support listen attach")
	}
	if NewNodeProvider().AttachCapability() != AttachModePID {
		t.Fatal("Node should support pid attach")
	}
}

func TestPythonProviderSupportsListenAttach(t *testing.T) {
	assert.Equal(t, AttachModeListen, NewPythonProvider("python").AttachCapability())
}

func TestPythonAttachArgumentsConnectsPort(t *testing.T) {
	provider := NewPythonProvider("python")
	args := provider.AttachArguments(LaunchConfig{WorkingDir: "/repo", AdapterPort: 5678}, 0)
	require.NotNil(t, args)
	conn, _ := args["connect"].(map[string]any)
	require.NotNil(t, conn)
	assert.Equal(t, "127.0.0.1", conn["host"])
	assert.Equal(t, 5678, conn["port"])
	assert.Equal(t, "/repo", args["cwd"])
}

func TestNodeProviderSupportsPIDAttach(t *testing.T) {
	assert.Equal(t, AttachModePID, NewNodeProvider().AttachCapability())
}
