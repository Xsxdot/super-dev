// adapter_resolution_test.go 在 Provider 公共缝验证统一 executable 解析合同。
//
// 职责：
//   - 参数化覆盖所有会启动外部 adapter 的 provider
//   - 锁定显式、provider 默认与 PATH fallback 的来源语义
//
// 边界：
//   - 不检查真实 adapter 是否安装
//   - 不启动进程或建立 DAP 连接
package codedebug

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xsxdot/super-dev/agent/model"
)

func TestResolveAdapterExecutablePrecedenceForEveryExternalProvider(t *testing.T) {
	providers := []model.CodeDebugProvider{
		model.CodeDebugProviderGo,
		model.CodeDebugProviderPython,
		model.CodeDebugProviderNode,
		model.CodeDebugProviderNative,
		model.CodeDebugProviderJVM,
	}
	cases := []struct {
		name        string
		explicit    string
		provider    string
		fallback    string
		wantName    string
		wantSource  AdapterCommandSource
		unavailable bool
	}{
		{name: "explicit_overrides_provider_and_path", explicit: "explicit-adapter", provider: "provider-adapter", fallback: "path-adapter", wantName: "explicit-adapter", wantSource: AdapterCommandSourceExplicit},
		{name: "provider_default_overrides_path", provider: "provider-adapter", fallback: "path-adapter", wantName: "provider-adapter", wantSource: AdapterCommandSourceProviderDefault},
		{name: "path_is_last_candidate", fallback: "path-adapter", wantName: "path-adapter", wantSource: AdapterCommandSourcePATHFallback},
		{name: "no_candidate_is_unavailable", unavailable: true},
	}

	for _, provider := range providers {
		for _, tc := range cases {
			t.Run(string(provider)+"_"+tc.name, func(t *testing.T) {
				resolved, err := ResolveAdapterExecutable(AdapterResolutionRequest{
					Provider:        provider,
					ExplicitCommand: tc.explicit,
					ProviderDefault: tc.provider,
					PATHFallback:    tc.fallback,
				})
				if tc.unavailable {
					require.ErrorIs(t, err, ErrAdapterUnavailable)
					info, ok := AdapterErrorDetails(err)
					require.True(t, ok)
					assert.Equal(t, provider, info.Provider)
					return
				}
				require.NoError(t, err)
				assert.Equal(t, tc.wantName, resolved.Name)
				assert.Equal(t, tc.wantSource, resolved.Source)
			})
		}
	}
}

func TestProviderAdapterExecutableResolutionContract(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		wantArgs []string
	}{
		{name: "go", provider: NewGoProvider(), wantArgs: []string{"dap", "--listen=127.0.0.1:43100"}},
		{name: "python", provider: NewPythonProvider("provider-python"), wantArgs: []string{"-m", "debugpy.adapter", "--host", "127.0.0.1", "--port", "43100"}},
		{name: "node", provider: NewNodeProvider("/bundle/js-debug/dapDebugServer.js"), wantArgs: []string{"/bundle/js-debug/dapDebugServer.js", "43100", "127.0.0.1"}},
		{name: "native", provider: NewNativeDebugProvider("provider-lldb-dap"), wantArgs: []string{"--connection", "listen://127.0.0.1:43100"}},
		{name: "jvm", provider: NewJVMDebugProvider("provider-jvm-wrapper"), wantArgs: []string{"--workspace", "/repo", "43100"}},
	}

	for _, tt := range tests {
		t.Run(tt.name+"_explicit_overrides_lower_candidates", func(t *testing.T) {
			cmd, err := tt.provider.AdapterCommand(LaunchConfig{
				AdapterPort:    43100,
				AdapterCommand: `C:\Program Files\SuperDev Adapters\explicit-adapter.exe`,
				AdapterArgs:    []string{"--workspace", "/repo"},
				WorkingDir:     "/repo",
				Env:            map[string]string{"ADAPTER_TEST": "contract"},
			})

			require.NoError(t, err)
			assert.Equal(t, `C:\Program Files\SuperDev Adapters\explicit-adapter.exe`, cmd.Name)
			assert.Equal(t, AdapterCommandSourceExplicit, cmd.Source)
			assert.Equal(t, tt.wantArgs, cmd.Args)
			assert.Equal(t, "/repo", cmd.WorkDir)
			assert.Equal(t, "contract", cmd.Env["ADAPTER_TEST"])
		})
	}
}

func TestAdapterErrorContextOmitsPathsArgumentsAndEnvironment(t *testing.T) {
	err := NewAdapterError(CodeAdapterUnavailable, AdapterCommand{
		Provider: model.CodeDebugProviderJVM,
		Name:     `C:\Users\alice\Private Tools\jvm-wrapper.exe`,
		Source:   AdapterCommandSourceExplicit,
		Args:     []string{"--token", "super-secret-token"},
		Env:      map[string]string{"API_TOKEN": "super-secret-env"},
	}, errors.New(`fork/exec C:\Users\alice\Private Tools\jvm-wrapper.exe --token super-secret-token: access denied`))

	info, ok := AdapterErrorDetails(err)
	require.True(t, ok)
	assert.Equal(t, "jvm-wrapper.exe", info.Executable)
	assert.NotContains(t, info.Command, "alice")
	assert.NotContains(t, info.Command, "super-secret-token")
	assert.NotContains(t, info.Command, "super-secret-env")
	assert.Contains(t, err.Error(), CodeAdapterUnavailable)
	assert.Contains(t, err.Error(), string(AdapterCommandSourceExplicit))
	assert.Contains(t, err.Error(), "jvm-wrapper.exe")
	assert.NotContains(t, err.Error(), "alice")
	assert.NotContains(t, err.Error(), "super-secret-token")
}

func TestProviderDefaultAdapterCommandContract(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		want     string
	}{
		{name: "go", provider: NewGoProvider("provider-dlv"), want: "provider-dlv"},
		{name: "python", provider: NewPythonProvider("provider-python"), want: "provider-python"},
		{name: "node", provider: NewNodeProvider("/bundle/js-debug/dapDebugServer.js", "provider-node"), want: "provider-node"},
		{name: "native", provider: NewNativeDebugProvider("provider-lldb-dap"), want: "provider-lldb-dap"},
		{name: "jvm", provider: NewJVMDebugProvider("provider-jvm-wrapper"), want: "provider-jvm-wrapper"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.provider.AdapterCommand(LaunchConfig{AdapterPort: 43101})

			require.NoError(t, err)
			assert.Equal(t, tt.want, cmd.Name)
			assert.Equal(t, AdapterCommandSourceProviderDefault, cmd.Source)
		})
	}
}

func TestProviderPATHFallbackContract(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
		want     string
	}{
		{name: "go", provider: NewGoProvider(), want: "dlv"},
		{name: "python", provider: NewPythonProvider(""), want: "python3"},
		{name: "node", provider: NewNodeProvider("/bundle/js-debug/dapDebugServer.js"), want: "node"},
		{name: "native", provider: NewNativeDebugProvider(""), want: "lldb-dap"},
		{name: "jvm", provider: NewJVMDebugProvider(""), want: "jvm-dap-wrapper"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.provider.AdapterCommand(LaunchConfig{AdapterPort: 43102})

			require.NoError(t, err)
			assert.Equal(t, tt.want, cmd.Name)
			assert.Equal(t, AdapterCommandSourcePATHFallback, cmd.Source)
		})
	}
}

func TestJVMProviderExecutableResolutionPrecedence(t *testing.T) {
	tests := []struct {
		name            string
		explicitCommand string
		providerDefault string
		wantName        string
		wantSource      AdapterCommandSource
	}{
		{name: "explicit overrides provider default and PATH", explicitCommand: "explicit-jvm-wrapper", providerDefault: "provider-jvm-wrapper", wantName: "explicit-jvm-wrapper", wantSource: AdapterCommandSourceExplicit},
		{name: "provider default overrides PATH", providerDefault: "provider-jvm-wrapper", wantName: "provider-jvm-wrapper", wantSource: AdapterCommandSourceProviderDefault},
		{name: "PATH wrapper is the final candidate", wantName: "jvm-dap-wrapper", wantSource: AdapterCommandSourcePATHFallback},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := NewJVMDebugProvider(tt.providerDefault).AdapterCommand(LaunchConfig{
				AdapterPort:    43103,
				AdapterCommand: tt.explicitCommand,
			})

			require.NoError(t, err)
			assert.Equal(t, tt.wantName, cmd.Name)
			assert.Equal(t, tt.wantSource, cmd.Source)
		})
	}
}
