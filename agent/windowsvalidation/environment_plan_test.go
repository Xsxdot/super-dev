// environment_plan_test.go 锁定正式 Windows 环境计划与冻结 prerequisite catalog。
//
// 职责：
//   - 防止新增/重构 plan 时遗漏平台、七语言工具链或 debugger adapter
//   - 确认 final catalog 由 production 默认 plan 完整覆盖
//
// 边界：
//   - 不执行计划命令、不读取本机环境或 adapter 文件
//   - 不声明任何 Windows observed 值通过
package windowsvalidation

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreInstallEnvironmentPlanBindingAllowsOnlyPostInstallHostExpansion(t *testing.T) {
	t.Parallel()
	frozen := FrozenBuild{}
	frozen.Build.ProductVersion = "0.2.1"
	base := DefaultEnvironmentPlanOptions{
		FrozenBuild: frozen, LinuxHostID: "REPLACE_AFTER_FRESH_PROFILE",
		JVMAdapterCommand: `C:\Tools\jvm-wrapper.exe`, JVMAdapterSHA256: strings.Repeat("a", 64),
		ChromeVersion: "126.0.6478.127", ChromeSHA256: strings.Repeat("b", 64), ChromeSignerIdentity: "CHROME",
		EdgeVersion: "126.0.2592.87", EdgeSHA256: strings.Repeat("c", 64), EdgeSignerIdentity: "EDGE",
	}
	preInstall := DefaultWindowsEnvironmentPlan(base)
	base.LinuxHostID = "fresh-linux-host-id"
	postInstall := DefaultWindowsEnvironmentPlan(base)

	require.NotEqual(t, CanonicalEnvironmentPlanDigest(preInstall), CanonicalEnvironmentPlanDigest(postInstall))
	require.NoError(t, VerifyPreInstallEnvironmentPlanBinding(preInstall, postInstall))
	require.Equal(t, CanonicalPreInstallEnvironmentPlanDigest(preInstall), CanonicalPreInstallEnvironmentPlanDigest(postInstall))

	changed := postInstall
	changed.Browsers = append([]EnvironmentBrowserPlan{}, postInstall.Browsers...)
	changed.Browsers[0].Expected.Version = "127.0.0.0"
	require.ErrorContains(t, VerifyPreInstallEnvironmentPlanBinding(preInstall, changed), "stable subset")
}

func TestDefaultWindowsEnvironmentPlanCoversFrozenFinalCatalog(t *testing.T) {
	frozen := FrozenBuild{}
	frozen.Build.ProductVersion = "0.2.1"
	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{
		FrozenBuild: frozen, AgentDataDirectory: `C:\ProgramData\SuperDev`,
		JVMAdapterCommand: `C:\Program Files\SuperDev\jvm-wrapper.exe`, JVMAdapterSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		LinuxHostID: "linux-validation", ChromeVersion: "126.0.6478.127", ChromeSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChromeSignerIdentity: "CHROME-SIGNER",
		EdgeVersion: "126.0.2592.87", EdgeSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", EdgeSignerIdentity: "EDGE-SIGNER",
	})

	assert.Equal(t, EnvironmentPrerequisiteCatalogVersion, plan.CatalogVersion)
	assert.Equal(t, "superdev.windows-environment-prerequisites/v2", plan.CatalogVersion)
	assert.Len(t, RequiredEnvironmentPrerequisiteKeys(), 34)
	keys := []string{
		EnvironmentKeyCandidateBuild,
		EnvironmentKeyBrowserChrome, EnvironmentKeyBrowserEdge,
		EnvironmentKeyRemoteLinuxHost, EnvironmentKeyRemoteLinuxAgent, EnvironmentKeyRemoteTunnel,
		EnvironmentKeyRemoteLinuxMachine, EnvironmentKeyRemoteManagedBaseline,
		EnvironmentKeyRemoteDirectExposure, EnvironmentKeyRemoteGovernance,
		EnvironmentKeySecurityApproval, EnvironmentKeySecurityCredential,
	}
	for _, probe := range plan.Probes {
		keys = append(keys, probe.Key)
	}
	for _, adapter := range plan.Adapters {
		keys = append(keys, adapter.Key)
	}
	sort.Strings(keys)
	require.Equal(t, RequiredEnvironmentPrerequisiteKeys(), keys)
}

func TestEnvironmentVersionPolicyHandlesMinimumAndFrozenMinor(t *testing.T) {
	assert.True(t, environmentVersionMatches(">=1.22", "1.23.5"))
	assert.False(t, environmentVersionMatches(">=1.22", "1.21.9"))
	assert.True(t, environmentVersionMatches("<20", "19"))
	assert.False(t, environmentVersionMatches("<20", "21"))
	assert.True(t, environmentVersionMatches(">=10,<20", "19"))
	assert.False(t, environmentVersionMatches(">=10,<20", "9"))
	assert.True(t, environmentVersionMatches("17.14.*", "17.14.36221.1"))
	assert.False(t, environmentVersionMatches("17.14.*", "17.15.0"))
}

func TestWindowsPlatformPolicyRejectsWindows11ServerAndPowerShell7(t *testing.T) {
	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{})
	windows := environmentProbePlanByKey(t, plan, EnvironmentKeyPlatformWindows)
	powershell := environmentProbePlanByKey(t, plan, EnvironmentKeyPowerShell51)

	assert.Equal(t, "19045", windows.Expected.Version)
	assert.Empty(t, windows.Expected.Identity)
	assert.Empty(t, environmentExpectationMismatch(windows.Expected, EnvironmentObserved{Version: "19045", Identity: "windows-client/amd64"}, EnvironmentResolved{}))
	assert.Contains(t, environmentExpectationMismatch(windows.Expected, EnvironmentObserved{Version: "19044", Identity: "windows-client/amd64"}, EnvironmentResolved{}), "does not satisfy")
	assert.Contains(t, environmentExpectationMismatch(windows.Expected, EnvironmentObserved{Version: "22631", Identity: "windows-client/amd64"}, EnvironmentResolved{}), "does not satisfy")
	assert.Contains(t, environmentExpectationMismatch(windows.Expected, EnvironmentObserved{Version: "20348", Identity: "windows-server/amd64"}, EnvironmentResolved{}), "does not satisfy")
	assert.Empty(t, environmentExpectationMismatch(powershell.Expected, EnvironmentObserved{Version: "5.1.19041.5608", Identity: "powershell.exe"}, EnvironmentResolved{}))
	assert.Contains(t, environmentExpectationMismatch(powershell.Expected, EnvironmentObserved{Version: "7.4.2", Identity: "powershell.exe"}, EnvironmentResolved{}), "does not satisfy")
}

func TestEnvironmentReadOnlyArgvDisablesPythonBytecodeAndRejectsBrowserLaunch(t *testing.T) {
	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{})
	debugpy := environmentProbePlanByKey(t, plan, EnvironmentKeyToolchainDebugpy)
	assert.Equal(t, []string{"-B", "-c", "import debugpy;print(debugpy.__version__)"}, debugpy.Command.Arguments)

	var pythonAdapter EnvironmentAdapterPlan
	for _, adapter := range plan.Adapters {
		if adapter.Key == EnvironmentKeyAdapterPython {
			pythonAdapter = adapter
			break
		}
	}
	require.Equal(t, EnvironmentKeyAdapterPython, pythonAdapter.Key)
	assert.Equal(t, debugpy.Command.Arguments, pythonAdapter.VersionArgs)
	assert.NoError(t, validateReadOnlyEnvironmentCommand(debugpy.Command))
	assert.Error(t, validateReadOnlyEnvironmentCommand(EnvironmentCommand{
		Key: EnvironmentKeyToolchainDebugpy, Executable: "python.exe",
		Arguments: []string{"-c", "import debugpy;print(debugpy.__version__)"},
	}))
	_, chromeAllowed := readOnlyEnvironmentArguments(EnvironmentKeyBrowserChrome)
	_, edgeAllowed := readOnlyEnvironmentArguments(EnvironmentKeyBrowserEdge)
	assert.False(t, chromeAllowed)
	assert.False(t, edgeAllowed)
}

func TestEnvironmentCommandInvocationUsesStrictWindowsBatchProbeContract(t *testing.T) {
	tests := []struct {
		name     string
		goos     string
		command  EnvironmentCommand
		resolved string
		want     environmentCommandInvocation
		wantErr  string
	}{
		{
			name: "npm cmd uses fixed cmd wrapper", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version"}},
			resolved: `C:\Program Files\nodejs\npm.cmd`,
			want:     environmentCommandInvocation{Executable: "cmd.exe", WindowsCommandLine: `/d /v:off /s /c ""C:\Program Files\nodejs\npm.cmd" --version"`},
		},
		{
			name: "kotlinc bat uses fixed cmd wrapper", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainKotlin, Executable: "kotlinc", Arguments: []string{"-version"}},
			resolved: `C:\Program Files\Kotlin\bin\kotlinc.bat`,
			want:     environmentCommandInvocation{Executable: "cmd.exe", WindowsCommandLine: `/d /v:off /s /c ""C:\Program Files\Kotlin\bin\kotlinc.bat" -version"`},
		},
		{
			name: "ordinary Windows executable stays direct", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNode, Executable: "node", Arguments: []string{"--version"}},
			resolved: `C:\Program Files\nodejs\node.exe`,
			want:     environmentCommandInvocation{Executable: `C:\Program Files\nodejs\node.exe`, Arguments: []string{"--version"}},
		},
		{
			name: "non Windows script path stays direct", goos: "linux",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version"}},
			resolved: "/usr/bin/npm",
			want:     environmentCommandInvocation{Executable: "/usr/bin/npm", Arguments: []string{"--version"}},
		},
		{
			name: "unrelated key cannot use batch", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNode, Executable: "node", Arguments: []string{"--version"}},
			resolved: `C:\Tools\node.cmd`, wantErr: "not an allowed Windows batch probe",
		},
		{
			name: "npm requires canonical cmd name", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "other", Arguments: []string{"--version"}},
			resolved: `C:\Tools\other.cmd`, wantErr: "npm batch probe executable",
		},
		{
			name: "npm cannot inject argument", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version", "& whoami"}},
			resolved: `C:\Program Files\nodejs\npm.cmd`, wantErr: "arguments differ",
		},
		{
			name: "batch path cannot expand environment", goos: "windows",
			command:  EnvironmentCommand{Key: EnvironmentKeyToolchainNPM, Executable: "npm", Arguments: []string{"--version"}},
			resolved: `C:\%TEMP%\npm.cmd`, wantErr: "unsafe characters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := environmentCommandInvocationForOS(test.goos, test.command, test.resolved)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func environmentProbePlanByKey(t *testing.T, plan EnvironmentCollectionPlan, key string) EnvironmentProbePlan {
	t.Helper()
	for _, probe := range plan.Probes {
		if probe.Key == key {
			return probe
		}
	}
	t.Fatalf("environment probe %s not found", key)
	return EnvironmentProbePlan{}
}
