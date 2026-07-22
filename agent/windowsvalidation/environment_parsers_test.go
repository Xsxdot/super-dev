// environment_parsers_test.go 锁定 Windows 环境预检固定输出的稳定解析合同。
//
// 职责：
//   - 覆盖七语言工具链、浏览器依赖与 debugger adapter 的真实版本输出
//   - 防止 expected version 被误写成 observed version
//
// 边界：
//   - 只使用固定文本，不调用本机命令或外部服务
//   - 不验证安装、启动或调试行为
package windowsvalidation

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validWindowsPlatformProbeOutput = "windows_product=Windows 10 Pro\r\nwindows_build=19045\r\nwindows_display_version=22H2\r\nwindows_installation_type=Client\r\nwindows_ubr=5737\r\nwindows_installed_kbs=KB5060531,KB5062554\r\narch=AMD64\r\npowershell=5.1.19041.5608\r\npowershell_edition=Desktop\r\n"

func TestParseEnvironmentProbeFixedOutputs(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		stdout   string
		stderr   string
		version  string
		identity string
	}{
		{"platform", EnvironmentKeyPlatformWindows, validWindowsPlatformProbeOutput, "", "19045", "windows-client/amd64"},
		{"go", EnvironmentKeyToolchainGo, "go version go1.23.5 windows/amd64\n", "", "1.23.5", "windows/amd64"},
		{"delve", EnvironmentKeyToolchainDelve, "Delve Debugger\nVersion: 1.24.0\nBuild: $Id: abc $\n", "", "1.24.0", "dlv"},
		{"python", EnvironmentKeyToolchainPython, "Python 3.13.1\r\n", "", "3.13.1", "python"},
		{"debugpy", EnvironmentKeyToolchainDebugpy, "1.8.11\r\n", "", "1.8.11", "debugpy"},
		{"node", EnvironmentKeyToolchainNode, "v24.18.0\r\n", "", "24.18.0", "node"},
		{"npm", EnvironmentKeyToolchainNPM, "11.16.0\r\n", "", "11.16.0", "npm"},
		{"vs", EnvironmentKeyToolchainVSBuildTools, "17.14.36221.1\r\n", "", "17.14.36221.1", "vs-build-tools"},
		{"cmake", EnvironmentKeyToolchainCMake, "cmake version 4.4.0\r\n", "", "4.4.0", "cmake"},
		{"ninja", EnvironmentKeyToolchainNinja, "1.13.2\r\n", "", "1.13.2", "ninja"},
		{"llvm", EnvironmentKeyToolchainLLVM, "clang version 22.1.3\r\nTarget: x86_64-pc-windows-msvc\r\n", "", "22.1.3", "x86_64-pc-windows-msvc"},
		{"jdk", EnvironmentKeyToolchainJDK, "", "openjdk version \"21.0.11\" 2026-04-21 LTS\r\nOpenJDK Runtime Environment Temurin-21.0.11+10 (build 21.0.11+10-LTS)\r\n", "21.0.11+10", "openjdk"},
		{"kotlin", EnvironmentKeyToolchainKotlin, "", "info: kotlinc-jvm 2.4.0 (JRE 21.0.11+10-LTS)\r\n", "2.4.0", "kotlinc-jvm"},
		{"rust", EnvironmentKeyToolchainRust, "rustc 1.97.0 (abc 2026-03-01)\r\n", "", "1.97.0", "rustc"},
		{"rust target", EnvironmentKeyToolchainRustMSVCTarget, "aarch64-pc-windows-msvc\r\nx86_64-pc-windows-msvc\r\n", "", "", "x86_64-pc-windows-msvc"},
		{"native adapter", EnvironmentKeyAdapterNative, "lldb version 22.1.3\r\n", "", "22.1.3", "lldb-dap"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact, err := ParseEnvironmentProbe(test.key, test.stdout, test.stderr)
			require.NoError(t, err)
			assert.Equal(t, test.version, fact.Version)
			assert.Equal(t, test.identity, fact.Identity)
			if test.key == EnvironmentKeyPlatformWindows {
				assert.Equal(t, "22H2", fact.Attributes["display_version"])
				assert.Equal(t, "5737", fact.Attributes["ubr"])
				assert.Equal(t, "KB5060531,KB5062554", fact.Attributes["installed_kbs"])
				assert.Equal(t, WindowsValidationSupportScope, fact.Attributes["support_scope"])
				assert.Equal(t, WindowsValidationESUEvidenceStatus, fact.Attributes["esu_evidence_status"])
			}
		})
	}
}

func TestParseEnvironmentProbeRejectsMissingOrUnparseableObservedOutput(t *testing.T) {
	_, err := ParseEnvironmentProbe(EnvironmentKeyToolchainNode, "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvironmentKeyToolchainNode)

	_, err = ParseEnvironmentProbe(EnvironmentKeyToolchainNode, "Node expected v24.18.0", "")
	require.Error(t, err)

	coreFact, err := ParseEnvironmentProbe(EnvironmentKeyPowerShell51, strings.ReplaceAll(strings.ReplaceAll(validWindowsPlatformProbeOutput, "powershell=5.1.19041.5608", "powershell=7.4.2"), "powershell_edition=Desktop", "powershell_edition=Core"), "")
	require.NoError(t, err)
	require.ErrorContains(t, validateWindowsPowerShell51Fact(coreFact), "PowerShell 5.1")

	_, err = ParseEnvironmentProbe(EnvironmentKeyPlatformWindows, "unstructured platform output", "")
	require.Error(t, err)
}

func TestParseEnvironmentProbePreservesValidNonTargetWindowsFacts(t *testing.T) {
	tests := map[string]string{
		"Windows 11 build":      strings.Replace(validWindowsPlatformProbeOutput, "windows_build=19045", "windows_build=22631", 1),
		"wrong display version": strings.Replace(validWindowsPlatformProbeOutput, "windows_display_version=22H2", "windows_display_version=21H2", 1),
		"server":                strings.Replace(validWindowsPlatformProbeOutput, "windows_installation_type=Client", "windows_installation_type=Server", 1),
		"missing UBR":           strings.Replace(validWindowsPlatformProbeOutput, "windows_ubr=5737\r\n", "", 1),
		"missing installed KB":  strings.Replace(validWindowsPlatformProbeOutput, "windows_installed_kbs=KB5060531,KB5062554\r\n", "", 1),
	}
	for name, output := range tests {
		t.Run(name, func(t *testing.T) {
			fact, err := ParseEnvironmentProbe(EnvironmentKeyPlatformWindows, output, "")
			require.NoError(t, err)
			require.Error(t, validateEnvironmentPlatformFact(EnvironmentKeyPlatformWindows, fact))
		})
	}
}
