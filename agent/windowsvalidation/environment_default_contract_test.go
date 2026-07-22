// environment_default_contract_test.go 验证 production 默认 plan 的完整事实与防篡改门禁。
//
// 职责：
//   - 用固定但 key-specific 的 Windows 输出构造完整 34 项 final manifest
//   - 锁定 production path/source/version 单字段篡改与 expected 联动篡改均被拒绝
//
// 边界：
//   - 不执行真实 Windows 程序、浏览器、Agent 或 MCP
package windowsvalidation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultWindowsEnvironmentPlanProducesCompleteFinalFactsAndRejectsTampering(t *testing.T) {
	plan, manifest := collectPassingDefaultEnvironmentManifest(t)
	request := EnvironmentAdmissionRequest{Mode: EnvironmentAdmissionFinal, ExpectedPlanDigest: CanonicalEnvironmentPlanDigest(plan)}
	decision, err := AdmitEnvironmentManifest(manifest, request)
	require.NoError(t, err)
	assert.True(t, decision.Admitted)
	require.NoError(t, VerifyEnvironmentManifestPlanBinding(manifest, plan))

	tests := []struct {
		name   string
		mutate func(*EnvironmentPrerequisite)
	}{
		{name: "resolved path", mutate: func(item *EnvironmentPrerequisite) { item.Resolved.Path = `D:\forged\node.exe` }},
		{name: "resolved source", mutate: func(item *EnvironmentPrerequisite) { item.Resolved.Source = "explicit" }},
		{name: "observed version", mutate: func(item *EnvironmentPrerequisite) { item.Observed.Version = "23.0.0" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tampered := manifest
			tampered.Prerequisites = append([]EnvironmentPrerequisite{}, manifest.Prerequisites...)
			item := environmentPrerequisiteIndex(t, tampered, EnvironmentKeyToolchainNode)
			test.mutate(&tampered.Prerequisites[item])
			tampered.Prerequisites[item].ObservationDigest = CanonicalEnvironmentObservationDigest(tampered.Prerequisites[item])
			_, err := AdmitEnvironmentManifest(tampered, request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "provenance")
		})
	}

	coordinated := manifest
	coordinated.Prerequisites = append([]EnvironmentPrerequisite{}, manifest.Prerequisites...)
	index := environmentPrerequisiteIndex(t, coordinated, EnvironmentKeyToolchainNode)
	coordinated.Prerequisites[index].Expected.Version = "23.0.0"
	coordinated.Prerequisites[index].Observed.Version = "23.0.0"
	coordinated.Prerequisites[index].ObservationDigest = CanonicalEnvironmentObservationDigest(coordinated.Prerequisites[index])
	require.NoError(t, VerifyEnvironmentManifest(coordinated))
	err = VerifyEnvironmentManifestPlanBinding(coordinated, plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected facts differ")
}

func collectPassingDefaultEnvironmentManifest(t *testing.T) (EnvironmentCollectionPlan, EnvironmentManifest) {
	t.Helper()
	frozen := FrozenBuild{}
	frozen.Build.ProductVersion = "0.2.1"
	const jvmWrapper = `C:\Program Files\SuperDev\jvm-wrapper.exe`
	plan := DefaultWindowsEnvironmentPlan(DefaultEnvironmentPlanOptions{
		FrozenBuild: frozen, LinuxHostID: "linux-验证-01", AgentDataDirectory: `C:\ProgramData\SuperDev`,
		JVMAdapterCommand: jvmWrapper, JVMAdapterSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ChromeVersion: "126.0.6478.127", ChromeSHA256: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc", ChromeSignerIdentity: "CHROME-SIGNER",
		EdgeVersion: "126.0.2592.87", EdgeSHA256: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", EdgeSignerIdentity: "EDGE-SIGNER",
	})
	platform := validWindowsPlatformProbeOutput
	outputs := map[string]EnvironmentCommandOutput{
		EnvironmentKeyPlatformWindows: {Stdout: platform}, EnvironmentKeyPlatformArchitecture: {Stdout: platform}, EnvironmentKeyPowerShell51: {Stdout: platform},
		EnvironmentKeyToolchainGo: {Stdout: "go version go1.26.1 windows/amd64"}, EnvironmentKeyToolchainDelve: {Stdout: "Delve Debugger\nVersion: 1.26.1"},
		EnvironmentKeyToolchainPython: {Stdout: "Python 3.14.6"}, EnvironmentKeyToolchainDebugpy: {Stdout: "1.8.21"},
		EnvironmentKeyToolchainNode: {Stdout: "v24.18.0"}, EnvironmentKeyToolchainNPM: {Stdout: "11.16.0"},
		EnvironmentKeyToolchainVSBuildTools: {Stdout: "17.14.36221.1"}, EnvironmentKeyToolchainCMake: {Stdout: "cmake version 4.4.0"},
		EnvironmentKeyToolchainNinja: {Stdout: "1.13.2"}, EnvironmentKeyToolchainLLVM: {Stdout: "clang version 22.1.3\nTarget: x86_64-pc-windows-msvc"},
		EnvironmentKeyToolchainJDK: {Stderr: "OpenJDK Runtime Environment Temurin-21.0.11+10"}, EnvironmentKeyToolchainKotlin: {Stderr: "kotlinc-jvm 2.4.0"},
		EnvironmentKeyToolchainRust: {Stdout: "rustc 1.97.0 (abc 2026-01-01)"}, EnvironmentKeyToolchainRustMSVCTarget: {Stdout: "x86_64-pc-windows-msvc"},
		EnvironmentKeyAdapterGo: {Stdout: "Delve Debugger\nVersion: 1.26.1"}, EnvironmentKeyAdapterPython: {Stdout: "1.8.21"},
		EnvironmentKeyAdapterNode: {Stdout: "v24.18.0"}, EnvironmentKeyAdapterNative: {Stdout: "lldb version 22.1.3"},
	}
	for key, output := range outputs {
		output.ExitCode = 0
		output.Source = "path"
		output.ResolvedPath = `C:\Tools\` + key + `.exe`
		outputs[key] = output
	}
	fileObservations := map[string]EnvironmentFileObservation{jvmWrapper: {
		ResolvedPath: jvmWrapper, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}}
	for _, adapter := range plan.Adapters {
		if adapter.Key == EnvironmentKeyAdapterNode {
			fileObservations[adapter.AssetPath] = EnvironmentFileObservation{ResolvedPath: adapter.AssetPath, Version: "1.117.0", SHA256: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}
		}
	}
	mcp := successfulEnvironmentMCP()
	mcp.initialize.ServerInfo.Name = "superdev-mcp"
	mcp.initialize.ServerInfo.Version = "0.2.1"
	manifest, err := CollectEnvironmentManifest(context.Background(), EnvironmentCollectorOptions{
		CampaignID: testRemoteCampaignID, Plan: plan,
		CommandRunner: &fixedEnvironmentRunner{outputs: outputs, errors: map[string]error{}},
		FileReader:    fixedEnvironmentFileReader{observations: fileObservations}, MCP: mcp,
		AgentAPI: successfulEnvironmentAgentAPIForVersion("0.2.1"), RemoteGovernanceAttestation: successfulRemoteGovernanceAttestation(testRemoteCampaignID),
		LinuxHostID:                 "linux-验证-01",
		CredentialReadinessObserved: true, CredentialReady: true,
		Now: func() time.Time { return time.Date(2026, 7, 15, 1, 2, 3, 0, time.UTC) }, Redactor: NewRedactor(),
	})
	require.NoError(t, err)
	return plan, manifest
}

func environmentPrerequisiteIndex(t *testing.T, manifest EnvironmentManifest, key string) int {
	t.Helper()
	for index := range manifest.Prerequisites {
		if manifest.Prerequisites[index].Key == key {
			return index
		}
	}
	t.Fatalf("environment prerequisite %s not found", key)
	return -1
}
