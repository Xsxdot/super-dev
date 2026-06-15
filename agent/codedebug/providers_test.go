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

func TestPythonProviderAdapterPropagatesEnv(t *testing.T) {
	provider := NewPythonProvider("python3")
	cmd, err := provider.AdapterCommand(LaunchConfig{
		AdapterPort: 39003,
		WorkingDir:  "/proj",
		Env:         map[string]string{"PATH": "/proj/.venv/bin:/usr/bin"},
	})
	require.NoError(t, err)
	// adapter 必须带上 deployment 的 env（含指向 venv 的 PATH），否则 debugpy.adapter
	// 会用系统 python3（无 debugpy）启动失败。
	assert.Equal(t, "/proj/.venv/bin:/usr/bin", cmd.Env["PATH"])
}

func TestNodeProviderAdapterCommandUsesBundledServer(t *testing.T) {
	provider := NewNodeProvider("/data/js-debug/src/dapDebugServer.js")
	cmd, err := provider.AdapterCommand(LaunchConfig{AdapterPort: 41020, WorkingDir: "/repo"})

	require.NoError(t, err)
	assert.Equal(t, "node", cmd.Name)
	assert.Equal(t, []string{"/data/js-debug/src/dapDebugServer.js", "41020", "127.0.0.1"}, cmd.Args)
}

func TestNodeProviderAdapterCommandMissingServer(t *testing.T) {
	provider := NewNodeProvider("")
	_, err := provider.AdapterCommand(LaunchConfig{AdapterPort: 41020})

	require.ErrorIs(t, err, ErrAdapterUnavailable)
}

func TestProviderAttachCapability(t *testing.T) {
	if NewGoProvider().AttachCapability() != AttachModePID {
		t.Fatal("Go should support pid-attach")
	}
	if NewPythonProvider("python3").AttachCapability() != AttachModeDirectDAP {
		t.Fatal("Python debugpy --listen exposes DAP directly; should be direct-dap")
	}
	if NewNodeProvider("/data/js-debug/src/dapDebugServer.js").AttachCapability() != AttachModeListen {
		t.Fatal("Node should support listen attach")
	}
}

func TestPythonProviderUsesDirectDAP(t *testing.T) {
	assert.Equal(t, AttachModeDirectDAP, NewPythonProvider("python").AttachCapability())
}

func TestPythonAttachArgumentsAreConnectless(t *testing.T) {
	// 直连 debugpy --listen 端口时，DAP attach 不需要 connect 字段
	// （我们已经连到了它的 DAP 服务）；带 connect 反而触发 adapter 角色错位、attach 超时。
	provider := NewPythonProvider("python")
	args := provider.AttachArguments(LaunchConfig{WorkingDir: "/repo", TargetPort: 5678}, 0)
	require.NotNil(t, args)
	_, hasConnect := args["connect"]
	assert.False(t, hasConnect, "direct-dap attach must not include connect")
	assert.Equal(t, "/repo", args["cwd"])
}

func TestNodeProviderConnectAttachToInspectorPort(t *testing.T) {
	provider := NewNodeProvider("/data/js-debug/src/dapDebugServer.js")
	assert.Equal(t, AttachModeListen, provider.AttachCapability())
	args := provider.AttachArguments(LaunchConfig{WorkingDir: "/repo", TargetPort: 9229}, 0)
	require.NotNil(t, args)
	assert.Equal(t, 9229, args["port"])
	assert.Equal(t, "attach", args["request"])
}
