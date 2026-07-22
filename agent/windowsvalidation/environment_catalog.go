// environment_catalog.go 定义最终 Windows campaign 不可删减的环境 prerequisite 目录。
//
// 职责：
//   - 为 final admission 提供完整、版本化且稳定排序的 required key 集合
//   - 防止调用方用只含少数 PASS 项的清单绕过正式环境门禁
//
// 边界：
//   - 不保存 observed 值、不执行 preflight，也不决定 diagnostic 允许哪些具名 BLOCKED 项
//   - catalog 只描述覆盖义务；每项结论仍由统一 ValidationResult 模型派生
package windowsvalidation

import "sort"

const (
	// EnvironmentPrerequisiteCatalogVersion 是正式 Windows preflight 覆盖目录版本。
	EnvironmentPrerequisiteCatalogVersion = "superdev.windows-environment-prerequisites/v2"
)

var environmentRequiredPrerequisiteKeys = []string{
	EnvironmentKeyAdapterGo,
	EnvironmentKeyAdapterJVM,
	EnvironmentKeyAdapterNative,
	EnvironmentKeyAdapterNode,
	EnvironmentKeyAdapterPython,
	EnvironmentKeyBrowserChrome,
	EnvironmentKeyBrowserEdge,
	EnvironmentKeyCandidateBuild,
	EnvironmentKeyPlatformArchitecture,
	EnvironmentKeyPowerShell51,
	EnvironmentKeyPlatformWindows,
	EnvironmentKeyRemoteLinuxAgent,
	EnvironmentKeyRemoteDirectExposure,
	EnvironmentKeyRemoteGovernance,
	EnvironmentKeyRemoteLinuxHost,
	EnvironmentKeyRemoteLinuxMachine,
	EnvironmentKeyRemoteManagedBaseline,
	EnvironmentKeyRemoteTunnel,
	EnvironmentKeySecurityApproval,
	EnvironmentKeySecurityCredential,
	EnvironmentKeyToolchainCMake,
	EnvironmentKeyToolchainDebugpy,
	EnvironmentKeyToolchainDelve,
	EnvironmentKeyToolchainGo,
	EnvironmentKeyToolchainJDK,
	EnvironmentKeyToolchainKotlin,
	EnvironmentKeyToolchainLLVM,
	EnvironmentKeyToolchainNinja,
	EnvironmentKeyToolchainNode,
	EnvironmentKeyToolchainNPM,
	EnvironmentKeyToolchainPython,
	EnvironmentKeyToolchainRust,
	EnvironmentKeyToolchainRustMSVCTarget,
	EnvironmentKeyToolchainVSBuildTools,
}

// environmentPreInstallPrerequisiteKeys 只包含无需已安装 SuperDev 产品即可只读观察的事实。
// Node adapter asset、远端产品拓扑与安全 MCP surface 必须等 install/start 后再采集。
var environmentPreInstallPrerequisiteKeys = []string{
	EnvironmentKeyAdapterGo,
	EnvironmentKeyAdapterJVM,
	EnvironmentKeyAdapterNative,
	EnvironmentKeyAdapterPython,
	EnvironmentKeyBrowserChrome,
	EnvironmentKeyBrowserEdge,
	EnvironmentKeyCandidateBuild,
	EnvironmentKeyPlatformArchitecture,
	EnvironmentKeyPowerShell51,
	EnvironmentKeyPlatformWindows,
	EnvironmentKeyToolchainCMake,
	EnvironmentKeyToolchainDebugpy,
	EnvironmentKeyToolchainDelve,
	EnvironmentKeyToolchainGo,
	EnvironmentKeyToolchainJDK,
	EnvironmentKeyToolchainKotlin,
	EnvironmentKeyToolchainLLVM,
	EnvironmentKeyToolchainNinja,
	EnvironmentKeyToolchainNode,
	EnvironmentKeyToolchainNPM,
	EnvironmentKeyToolchainPython,
	EnvironmentKeyToolchainRust,
	EnvironmentKeyToolchainRustMSVCTarget,
	EnvironmentKeyToolchainVSBuildTools,
}

// RequiredEnvironmentPrerequisiteKeys 返回正式 final admission 必须包含的稳定 key 副本。
//
// 返回：
//   - 按字典序排列的完整 required prerequisite key 列表
//
// 注意：调用方修改返回值不会改变 package 内的冻结 catalog。
func RequiredEnvironmentPrerequisiteKeys() []string {
	keys := append([]string{}, environmentRequiredPrerequisiteKeys...)
	sort.Strings(keys)
	return keys
}

// PreInstallEnvironmentPrerequisiteKeys 返回产品安装前必须真实观察并通过的 catalog key。
//
// 返回：
//   - 按字典序排列、无需已安装产品的 prerequisite key 副本
func PreInstallEnvironmentPrerequisiteKeys() []string {
	keys := append([]string{}, environmentPreInstallPrerequisiteKeys...)
	sort.Strings(keys)
	return keys
}

// PostInstallEnvironmentPrerequisiteKeys 返回必须依赖已安装 MCP/Agent/Product seam 的 key。
//
// 返回：
//   - 完整 v2/34 catalog 扣除 pre-install key 后的稳定排序副本
func PostInstallEnvironmentPrerequisiteKeys() []string {
	preInstall := make(map[string]struct{}, len(environmentPreInstallPrerequisiteKeys))
	for _, key := range environmentPreInstallPrerequisiteKeys {
		preInstall[key] = struct{}{}
	}
	keys := make([]string, 0, len(environmentRequiredPrerequisiteKeys)-len(preInstall))
	for _, key := range environmentRequiredPrerequisiteKeys {
		if _, exists := preInstall[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
