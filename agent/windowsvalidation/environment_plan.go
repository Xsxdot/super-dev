// environment_plan.go 构造 Windows 真机验证冻结环境的默认只读采集计划。
//
// 职责：
//   - 把七语言 fixture、浏览器与 debugger adapter 的已冻结依赖集中为稳定 prerequisite key
//   - 为每个命令声明唯一的只读 argv、expected identity/version 与确定性 remediation
//
// 边界：
//   - 不读取机器 observed 值、不执行命令，也不把缺失的 JVM wrapper 或 js-debug 路径伪装成已配置
//   - 只描述验证包合同；Windows 真机事实仍由 CollectEnvironmentManifest 采集
package windowsvalidation

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/xsxdot/super-dev/agent/model"
)

const environmentPowerShellObservationScript = `$cv=Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop;$kbs=@(Get-HotFix -ErrorAction SilentlyContinue|ForEach-Object { [string]$_.HotFixID }|Where-Object { $_ -match '^KB[0-9]+$' }|ForEach-Object { $_.ToUpperInvariant() }|Sort-Object -Unique);$arch=$env:PROCESSOR_ARCHITECTURE;$ps=$PSVersionTable.PSVersion.ToString();$edition=[string]$PSVersionTable.PSEdition;Write-Output ("windows_product="+$cv.ProductName);Write-Output ("windows_build="+$cv.CurrentBuildNumber);Write-Output ("windows_display_version="+$cv.DisplayVersion);Write-Output ("windows_installation_type="+$cv.InstallationType);Write-Output ("windows_ubr="+$cv.UBR);Write-Output ("windows_installed_kbs="+($kbs -join ','));Write-Output ("arch="+$arch);Write-Output ("powershell="+$ps);Write-Output ("powershell_edition="+$edition)`

// DefaultEnvironmentPlanOptions 提供候选构建和安装后才知道的 adapter 文件位置。
type DefaultEnvironmentPlanOptions struct {
	FrozenBuild          FrozenBuild
	AgentDataDirectory   string
	JVMAdapterCommand    string
	JVMAdapterSHA256     string
	GoAdapterCommand     string
	PythonAdapterCommand string
	NodeAdapterCommand   string
	NativeAdapterCommand string
	LinuxHostID          string
	ChromeVersion        string
	ChromeSHA256         string
	ChromeSignerIdentity string
	EdgeVersion          string
	EdgeSHA256           string
	EdgeSignerIdentity   string
}

// DefaultWindowsEnvironmentPlan 返回覆盖正式 Windows campaign 的版本化只读 preflight plan。
//
// 参数：
//   - options: 冻结候选构建、Agent 数据目录、JVM wrapper 身份和可选显式 adapter 命令
//
// 返回：
//   - 平台、七语言工具链以及 Go/Python/Node/Native/JVM adapter 的固定计划
//
// 注意：空的 JVM wrapper 和 Agent 数据目录会保留为空候选，collector 将其如实派生为 BLOCKED。
func DefaultWindowsEnvironmentPlan(options DefaultEnvironmentPlanOptions) EnvironmentCollectionPlan {
	platformCommand := EnvironmentCommand{
		Executable: "powershell.exe",
		Arguments: []string{
			"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
			environmentPowerShellObservationScript,
		},
	}
	probe := func(key, executable string, args []string, expected EnvironmentExpected, remediation string) EnvironmentProbePlan {
		command := EnvironmentCommand{Key: key, Executable: executable, Arguments: append([]string{}, args...)}
		return EnvironmentProbePlan{Key: key, Required: true, Expected: expected, Command: command, Remediation: remediation}
	}
	platformProbe := func(key string, expected EnvironmentExpected, remediation string) EnvironmentProbePlan {
		command := platformCommand
		command.Key = key
		return EnvironmentProbePlan{Key: key, Required: true, Expected: expected, Command: command, Remediation: remediation}
	}

	dataDir := strings.TrimSpace(options.AgentDataDirectory)
	jsDebugAsset := ""
	jsDebugVersion := ""
	if dataDir != "" {
		jsDebugAsset = filepath.Join(dataDir, "js-debug", "src", "dapDebugServer.js")
		jsDebugVersion = filepath.Join(dataDir, "js-debug", ".superdev-version")
	}
	adapters := []EnvironmentAdapterPlan{
		adapterPlan(EnvironmentKeyAdapterGo, model.CodeDebugProviderGo, options.GoAdapterCommand, "", "dlv", []string{"version"}, "", "", EnvironmentExpected{Version: "1.26.1", Identity: "dlv"}, "Install frozen Delve 1.26.1 and configure the selected executable without falling back after launch failure."),
		adapterPlan(EnvironmentKeyAdapterPython, model.CodeDebugProviderPython, options.PythonAdapterCommand, "python3", "python3", []string{"-B", "-c", "import debugpy;print(debugpy.__version__)"}, "", "", EnvironmentExpected{Version: "1.8.21", Identity: "debugpy"}, "Install debugpy 1.8.21 in the Python environment resolved by the provider."),
		adapterPlan(EnvironmentKeyAdapterNode, model.CodeDebugProviderNode, options.NodeAdapterCommand, "", "node", nil, jsDebugAsset, jsDebugVersion, EnvironmentExpected{Version: "1.117.0", Identity: "dapDebugServer.js"}, "Restore the packaged js-debug 1.117.0 asset and version marker under Agent data."),
		adapterPlan(EnvironmentKeyAdapterNative, model.CodeDebugProviderNative, options.NativeAdapterCommand, "", "lldb-dap", []string{"--version"}, "", "", EnvironmentExpected{Version: "22.1.3", Identity: "lldb-dap"}, "Install frozen LLVM/lldb-dap 22.1.3 x64 and restart Desktop Agent."),
		adapterPlan(EnvironmentKeyAdapterJVM, model.CodeDebugProviderJVM, options.JVMAdapterCommand, "", "", nil, options.JVMAdapterCommand, "", EnvironmentExpected{Identity: safeWindowsBase(options.JVMAdapterCommand)}, "Provide the project-approved frozen JVM DAP wrapper and its SHA-256 identity."),
	}
	if strings.TrimSpace(options.JVMAdapterSHA256) != "" {
		adapters[len(adapters)-1].ExpectedAssetSHA256 = strings.ToLower(strings.TrimSpace(options.JVMAdapterSHA256))
	}
	for index := range adapters {
		resolved, err := ResolveEnvironmentAdapter(adapters[index])
		if err == nil {
			adapters[index].Expected.Source = string(resolved.Source)
		}
	}

	return EnvironmentCollectionPlan{
		SchemaVersion:     EnvironmentPlanSchemaVersion,
		Kind:              EnvironmentPlanKind,
		CatalogVersion:    EnvironmentPrerequisiteCatalogVersion,
		RemoteLinuxHostID: strings.TrimSpace(options.LinuxHostID),
		CandidateBuild: EnvironmentExpected{
			Version:  strings.TrimSpace(options.FrozenBuild.Build.ProductVersion),
			Identity: "superdev-mcp",
		},
		Probes: []EnvironmentProbePlan{
			platformProbe(EnvironmentKeyPlatformWindows, EnvironmentExpected{Version: windowsValidationBuild}, "Run the package only on Windows 10 Client 22H2 build 19045; architecture is admitted independently, and servicing KBs are not ESU entitlement."),
			platformProbe(EnvironmentKeyPlatformArchitecture, EnvironmentExpected{Identity: "amd64"}, "Use an x64 Windows process and candidate build."),
			platformProbe(EnvironmentKeyPowerShell51, EnvironmentExpected{Version: "5.1.*", Identity: "powershell.exe"}, "Run the documented commands unchanged in Windows PowerShell 5.1."),
			probe(EnvironmentKeyToolchainGo, "go", []string{"version"}, EnvironmentExpected{Version: "1.26.1", Identity: "windows/amd64"}, "Install frozen Go 1.26.1 x64 and restart Desktop Agent."),
			probe(EnvironmentKeyToolchainDelve, "dlv", []string{"version"}, EnvironmentExpected{Version: "1.26.1", Identity: "dlv"}, "Install frozen Delve 1.26.1 in the same environment used by Desktop Agent."),
			probe(EnvironmentKeyToolchainPython, "python", []string{"--version"}, EnvironmentExpected{Version: "3.14.6", Identity: "python"}, "Install frozen CPython 3.14.6 x64 and restart Desktop Agent."),
			probe(EnvironmentKeyToolchainDebugpy, "python", []string{"-B", "-c", "import debugpy;print(debugpy.__version__)"}, EnvironmentExpected{Version: "1.8.21", Identity: "debugpy"}, "Install frozen debugpy 1.8.21 in the validated CPython environment."),
			probe(EnvironmentKeyToolchainNode, "node", []string{"--version"}, EnvironmentExpected{Version: "24.18.0", Identity: "node"}, "Install frozen Node.js 24.18.0 x64 and restart Desktop Agent."),
			probe(EnvironmentKeyToolchainNPM, "npm", []string{"--version"}, EnvironmentExpected{Version: "11.16.0", Identity: "npm"}, "Install frozen npm 11.16.0 with Node.js 24.18.0."),
			probe(EnvironmentKeyToolchainVSBuildTools, "vswhere.exe", []string{"-latest", "-products", "*", "-requires", "Microsoft.VisualStudio.Component.VC.Tools.x86.x64", "-property", "catalog_productDisplayVersion"}, EnvironmentExpected{Version: "17.14.*", Identity: "vs-build-tools"}, "Install VS 2022 Build Tools 17.14 with the VCTools workload."),
			probe(EnvironmentKeyToolchainCMake, "cmake", []string{"--version"}, EnvironmentExpected{Version: "4.4.0", Identity: "cmake"}, "Install frozen CMake 4.4.0 x64."),
			probe(EnvironmentKeyToolchainNinja, "ninja", []string{"--version"}, EnvironmentExpected{Version: "1.13.2", Identity: "ninja"}, "Install frozen Ninja 1.13.2."),
			probe(EnvironmentKeyToolchainLLVM, "clang-cl", []string{"--version"}, EnvironmentExpected{Version: "22.1.3", Identity: "x86_64-pc-windows-msvc"}, "Install frozen LLVM 22.1.3 x64."),
			probe(EnvironmentKeyToolchainJDK, "java", []string{"-version"}, EnvironmentExpected{Version: "21.0.11+10", Identity: "openjdk"}, "Install frozen Temurin JDK 21.0.11+10 x64."),
			probe(EnvironmentKeyToolchainKotlin, "kotlinc", []string{"-version"}, EnvironmentExpected{Version: "2.4.0", Identity: "kotlinc-jvm"}, "Install frozen Kotlin compiler 2.4.0."),
			probe(EnvironmentKeyToolchainRust, "rustc", []string{"--version"}, EnvironmentExpected{Version: "1.97.0", Identity: "rustc"}, "Install frozen Rust 1.97.0 MSVC toolchain."),
			probe(EnvironmentKeyToolchainRustMSVCTarget, "rustup", []string{"target", "list", "--installed"}, EnvironmentExpected{Identity: "x86_64-pc-windows-msvc"}, "Install the x86_64-pc-windows-msvc Rust target."),
		},
		Adapters: adapters,
		Browsers: []EnvironmentBrowserPlan{
			browserPlan(EnvironmentKeyBrowserChrome, "chrome", options.ChromeVersion, options.ChromeSHA256, options.ChromeSignerIdentity, "Freeze the installed Chrome four-part version, executable SHA-256, and Authenticode signer before this campaign."),
			browserPlan(EnvironmentKeyBrowserEdge, "msedge", options.EdgeVersion, options.EdgeSHA256, options.EdgeSignerIdentity, "Freeze the installed Edge four-part version, executable SHA-256, and Authenticode signer before this campaign."),
		},
	}
}

// CanonicalEnvironmentPlanDigest 返回环境 plan JSON 合同的稳定 SHA-256。
func CanonicalEnvironmentPlanDigest(plan EnvironmentCollectionPlan) string {
	digest := sha256.Sum256([]byte(CanonicalJSON(plan)))
	return fmt.Sprintf("%x", digest)
}

// CanonicalPreInstallEnvironmentPlanDigest 返回 A 阶段可冻结 plan 子集的稳定摘要。
// Remote Host 以及只在安装后存在的 Node asset 等字段不属于这个摘要。
func CanonicalPreInstallEnvironmentPlanDigest(plan EnvironmentCollectionPlan) string {
	probes := make([]EnvironmentProbePlan, 0, len(plan.Probes))
	for _, probe := range plan.Probes {
		if isPreInstallEnvironmentKey(probe.Key) {
			probes = append(probes, probe)
		}
	}
	adapters := make([]EnvironmentAdapterPlan, 0, len(plan.Adapters))
	for _, adapter := range plan.Adapters {
		if isPreInstallEnvironmentKey(adapter.Key) {
			adapters = append(adapters, adapter)
		}
	}
	browsers := make([]EnvironmentBrowserPlan, 0, len(plan.Browsers))
	for _, browser := range plan.Browsers {
		if isPreInstallEnvironmentKey(browser.Key) {
			browsers = append(browsers, browser)
		}
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i].Key < probes[j].Key })
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].Key < adapters[j].Key })
	sort.Slice(browsers, func(i, j int) bool { return browsers[i].Key < browsers[j].Key })
	binding := struct {
		SchemaVersion  string                   `json:"schema_version"`
		Kind           string                   `json:"kind"`
		CatalogVersion string                   `json:"catalog_version"`
		CandidateBuild EnvironmentExpected      `json:"candidate_build"`
		Probes         []EnvironmentProbePlan   `json:"probes"`
		Adapters       []EnvironmentAdapterPlan `json:"adapters"`
		Browsers       []EnvironmentBrowserPlan `json:"browsers"`
	}{
		SchemaVersion: plan.SchemaVersion, Kind: plan.Kind, CatalogVersion: plan.CatalogVersion,
		CandidateBuild: plan.CandidateBuild, Probes: probes, Adapters: adapters, Browsers: browsers,
	}
	digest := sha256.Sum256([]byte(CanonicalJSON(binding)))
	return fmt.Sprintf("%x", digest)
}

// VerifyPreInstallEnvironmentPlanBinding 确认 B 沿用 A 冻结的全部安装前采集合同。
// B 可以补入 fresh Host 等 post-install 字段，但不得改写任一 A probe/adapter/browser/build。
func VerifyPreInstallEnvironmentPlanBinding(preInstall, postInstall EnvironmentCollectionPlan) error {
	if err := validateEnvironmentCollectionPlan(preInstall); err != nil {
		return fmt.Errorf("validate pre-install environment plan: %w", err)
	}
	if err := validateEnvironmentCollectionPlan(postInstall); err != nil {
		return fmt.Errorf("validate post-install environment plan: %w", err)
	}
	if CanonicalPreInstallEnvironmentPlanDigest(preInstall) != CanonicalPreInstallEnvironmentPlanDigest(postInstall) {
		return fmt.Errorf("post-install environment plan stable subset differs from prepared pre-install plan")
	}
	return nil
}

// VerifyEnvironmentManifestPlanBinding 校验 manifest 的 frozen expected 逐项来自同一份 plan。
//
// 注意：该校验同时比较 canonical digest 与每个 prerequisite expected，防止攻击者在保留
// plan_digest 字段的同时一起改写 expected/observed/resolved 后伪造一致的 PASS。
func VerifyEnvironmentManifestPlanBinding(manifest EnvironmentManifest, plan EnvironmentCollectionPlan) error {
	if plan.SchemaVersion != EnvironmentPlanSchemaVersion || plan.Kind != EnvironmentPlanKind {
		return fmt.Errorf("environment plan identity is invalid")
	}
	digest := CanonicalEnvironmentPlanDigest(plan)
	if !strings.EqualFold(digest, manifest.PlanDigest) {
		return fmt.Errorf("environment manifest plan_digest differs from the frozen plan")
	}
	expected := environmentExpectedByKey(plan)
	actual := environmentPrerequisiteMap(manifest.Prerequisites)
	for key, value := range expected {
		prerequisite, found := actual[key]
		if !found {
			return fmt.Errorf("environment manifest is missing plan prerequisite %q", key)
		}
		if CanonicalJSON(prerequisite.Expected) != CanonicalJSON(value) {
			return fmt.Errorf("environment prerequisite %q expected facts differ from the frozen plan", key)
		}
	}
	return nil
}

// environmentExpectedByKey 把 plan 展开成完整 v2 catalog 的冻结 expected 映射。
//
// 远端与安全项没有独立 plan slice；它们的 expected 由候选版本和 canonical Host
// 身份确定。pre-install collector 使用同一映射创建显式 deferred 项，避免自己拼合同。
func environmentExpectedByKey(plan EnvironmentCollectionPlan) map[string]EnvironmentExpected {
	expected := map[string]EnvironmentExpected{
		EnvironmentKeyCandidateBuild:        plan.CandidateBuild,
		EnvironmentKeyRemoteLinuxHost:       {Identity: strings.TrimSpace(plan.RemoteLinuxHostID)},
		EnvironmentKeyRemoteLinuxAgent:      {Version: strings.TrimSpace(plan.CandidateBuild.Version), Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/superdev-agent"},
		EnvironmentKeyRemoteTunnel:          {Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/transport/tunnel"},
		EnvironmentKeyRemoteLinuxMachine:    {Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/linux-machine"},
		EnvironmentKeyRemoteManagedBaseline: {Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/managed-baseline"},
		EnvironmentKeyRemoteDirectExposure:  {Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/direct-exposure"},
		EnvironmentKeyRemoteGovernance:      {Identity: strings.TrimSpace(plan.RemoteLinuxHostID) + "/human-governance-attestation"},
		EnvironmentKeySecurityApproval:      {Identity: "list_operation_approvals"},
		EnvironmentKeySecurityCredential:    {Identity: "credential_lease_ready"},
	}
	for _, probe := range plan.Probes {
		expected[probe.Key] = probe.Expected
	}
	for _, adapter := range plan.Adapters {
		value := adapter.Expected
		if hash := strings.ToLower(strings.TrimSpace(adapter.ExpectedAssetSHA256)); hash != "" {
			value.AssetIdentity = "sha256:" + hash
		}
		expected[adapter.Key] = value
	}
	for _, browser := range plan.Browsers {
		expected[browser.Key] = browser.Expected
	}
	return expected
}

func browserPlan(key, identity, version, hash, signer, remediation string) EnvironmentBrowserPlan {
	expected := EnvironmentExpected{
		Version: strings.TrimSpace(version), Identity: identity,
		AssetIdentity:   "sha256:" + strings.ToLower(strings.TrimSpace(hash)),
		SignatureStatus: "Valid", SignerIdentity: strings.ToUpper(strings.TrimSpace(signer)),
	}
	if strings.TrimSpace(hash) == "" {
		expected.AssetIdentity = ""
	}
	return EnvironmentBrowserPlan{Key: key, Required: true, Expected: expected, Remediation: remediation}
}

func adapterPlan(key string, provider model.CodeDebugProvider, explicit, providerDefault, fallback string, versionArgs []string, assetPath, versionFile string, expected EnvironmentExpected, remediation string) EnvironmentAdapterPlan {
	return EnvironmentAdapterPlan{
		Key: key, Required: true, Provider: provider,
		ExplicitCommand: strings.TrimSpace(explicit), ProviderDefault: strings.TrimSpace(providerDefault), PATHFallback: strings.TrimSpace(fallback),
		VersionArgs: append([]string{}, versionArgs...), AssetPath: strings.TrimSpace(assetPath), VersionFile: strings.TrimSpace(versionFile),
		Expected: expected, Remediation: remediation,
	}
}
