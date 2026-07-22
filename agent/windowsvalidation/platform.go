// platform.go 固定 Windows 功能判定的唯一操作系统合同。
//
// 职责：
//   - 拒绝在非 Windows x64 运行架构执行功能场景；
//   - 统一验证入口、环境采集与安装生命周期的 Windows 10 22H2 身份门禁；
//   - 把兼容性验证与无法机械证明的 ESU 支持资格明确分开。
//
// 边界：
//   - 不读取注册表或执行系统命令，调用方必须提供真实只读观察；
//   - 不把 UBR 或已安装 KB 推导成 ESU entitlement；
//   - 不限制 macOS 上的静态校验、单元测试和 Windows 交叉构建。
package windowsvalidation

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	windowsValidationProductPrefix  = "Windows 10"
	windowsValidationBuild          = "19045"
	windowsValidationDisplayVersion = "22H2"
	windowsValidationInstallType    = "Client"
	// WindowsValidationTargetLabel 是所有受管报告和 operator 文档使用的唯一目标平台标签。
	WindowsValidationTargetLabel = "Windows 10 22H2 x64 (build 19045)"
	// WindowsValidationSupportScope 明确该 campaign 只证明产品兼容性，不证明微软支持资格。
	WindowsValidationSupportScope = "compatibility_only"
	// WindowsValidationESUEvidenceStatus 明确 ESU entitlement 没有权威机械证据。
	WindowsValidationESUEvidenceStatus = "not_mechanically_verified"
)

// WindowsPlatformObservation 是各生产入口共享的 Windows 只读身份事实。
//
// InstalledKBs 只用于归档 servicing 证据；它不能证明 ESU entitlement。
type WindowsPlatformObservation struct {
	ProductName      string
	CurrentBuild     string
	DisplayVersion   string
	InstallationType string
	Architecture     string
	UBR              string
	InstalledKBs     []string
}

// ValidateExecutionPlatform 校验功能驱动只运行于 windows/amd64。
//
// 参数：
//   - goos/goarch: 当前二进制报告的 Go 运行平台
//
// 返回：
//   - 非 windows/amd64 时返回明确错误
func ValidateExecutionPlatform(goos, goarch string) error {
	if goos != "windows" || goarch != "amd64" {
		return fmt.Errorf("Windows functional validation requires windows/amd64, got %s/%s; package verification is not a Windows PASS", goos, goarch)
	}
	return nil
}

// ValidateWindows10ValidationPlatform 校验真机功能验证的唯一 Windows 平台身份。
//
// 参数：
//   - observation: 注册表与运行架构的真实只读观察。
//
// 返回：
//   - 仅 Windows 10 Client 22H2、build 19045、x64 且带有效 UBR 时成功；
//   - 其他 Windows 10 build、Windows 11、Server、非 x64 或缺失 servicing 身份时失败。
//
// 注意：成功只代表 compatibility campaign 的平台合同成立，不代表 ESU entitlement。
func ValidateWindows10ValidationPlatform(observation WindowsPlatformObservation) error {
	if err := validateWindows10TargetOS(observation); err != nil {
		return err
	}
	if normalizeWindowsValidationArchitecture(observation.Architecture) != "amd64" {
		return fmt.Errorf("Windows validation requires x64 architecture")
	}
	return nil
}

func validateWindows10TargetOS(observation WindowsPlatformObservation) error {
	product := strings.TrimSpace(observation.ProductName)
	build := strings.TrimSpace(observation.CurrentBuild)
	displayVersion := strings.TrimSpace(observation.DisplayVersion)
	installationType := strings.TrimSpace(observation.InstallationType)
	ubr := strings.TrimSpace(observation.UBR)
	if product != windowsValidationProductPrefix && !strings.HasPrefix(product, windowsValidationProductPrefix+" ") {
		return fmt.Errorf("Windows validation requires a Windows 10 product identity")
	}
	if installationType != windowsValidationInstallType {
		return fmt.Errorf("Windows validation requires Windows 10 Client installation type")
	}
	if build != windowsValidationBuild || displayVersion != windowsValidationDisplayVersion {
		return fmt.Errorf("Windows validation requires Windows 10 22H2 build 19045")
	}
	ubrValue, err := strconv.ParseUint(ubr, 10, 32)
	if err != nil || ubrValue == 0 {
		return fmt.Errorf("Windows validation requires a positive registry UBR observation")
	}
	return nil
}

func validateWindowsPlatformArchiveEvidence(observation WindowsPlatformObservation) error {
	if err := validateWindows10TargetOS(observation); err != nil {
		return err
	}
	if len(normalizeWindowsKBs(observation.InstalledKBs)) == 0 {
		return fmt.Errorf("Windows validation requires at least one installed KB observation")
	}
	return nil
}

func windowsPlatformObservationAttributes(observation WindowsPlatformObservation) map[string]string {
	return map[string]string{
		"product_name":        strings.TrimSpace(observation.ProductName),
		"current_build":       strings.TrimSpace(observation.CurrentBuild),
		"display_version":     strings.TrimSpace(observation.DisplayVersion),
		"installation_type":   strings.TrimSpace(observation.InstallationType),
		"architecture":        normalizeWindowsValidationArchitecture(observation.Architecture),
		"ubr":                 strings.TrimSpace(observation.UBR),
		"installed_kbs":       strings.Join(normalizeWindowsKBs(observation.InstalledKBs), ","),
		"support_scope":       WindowsValidationSupportScope,
		"esu_evidence_status": WindowsValidationESUEvidenceStatus,
	}
}

func validateWindowsArchitectureFact(fact EnvironmentProbeFact) error {
	if strings.TrimSpace(fact.Identity) != "amd64" {
		return fmt.Errorf("Windows validation requires an amd64 architecture observation")
	}
	if normalizeWindowsValidationArchitecture(fact.Attributes["architecture"]) != fact.Identity {
		return fmt.Errorf("Windows architecture identity differs from the observed attribute")
	}
	return nil
}

func validateWindowsPowerShell51Fact(fact EnvironmentProbeFact) error {
	if !environmentVersionMatches("5.1.*", fact.Version) {
		return fmt.Errorf("Windows validation requires Windows PowerShell 5.1")
	}
	if strings.TrimSpace(fact.Identity) != "powershell.exe" || !strings.EqualFold(strings.TrimSpace(fact.Attributes["powershell_edition"]), "Desktop") {
		return fmt.Errorf("Windows validation requires Windows PowerShell Desktop edition")
	}
	if strings.TrimSpace(fact.Attributes["powershell_version"]) != strings.TrimSpace(fact.Version) {
		return fmt.Errorf("Windows PowerShell version differs from the observed attribute")
	}
	return nil
}

func windowsPlatformObservationFromAttributes(attributes map[string]string) WindowsPlatformObservation {
	return WindowsPlatformObservation{
		ProductName:      attributes["product_name"],
		CurrentBuild:     attributes["current_build"],
		DisplayVersion:   attributes["display_version"],
		InstallationType: attributes["installation_type"],
		Architecture:     attributes["architecture"],
		UBR:              attributes["ubr"],
		InstalledKBs:     strings.Split(attributes["installed_kbs"], ","),
	}
}

func normalizeWindowsValidationArchitecture(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "amd64", "x64", "x86_64":
		return "amd64"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func normalizeWindowsKBs(values []string) []string {
	unique := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if len(value) <= 2 || !strings.HasPrefix(value, "KB") {
			continue
		}
		if _, err := strconv.ParseUint(strings.TrimPrefix(value, "KB"), 10, 64); err != nil {
			continue
		}
		unique[value] = struct{}{}
	}
	out := make([]string, 0, len(unique))
	for value := range unique {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
