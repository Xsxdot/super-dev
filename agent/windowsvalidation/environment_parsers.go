// environment_parsers.go 解析 Windows 环境预检的固定只读命令输出。
//
// 职责：
//   - 为平台、七语言工具链与适用 debugger adapter 提取稳定 observed version/identity
//   - 拒绝空输出、描述性文本和无法证明实际版本的输出
//
// 边界：
//   - 不执行命令、不读取 expected 值，也不决定最终 PASS/BLOCKED/FAIL
//   - 不保存 opaque stdout/stderr，避免将凭据或用户环境数据带入 manifest
package windowsvalidation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

const (
	EnvironmentKeyPlatformWindows         = "platform.windows"
	EnvironmentKeyPlatformArchitecture    = "platform.architecture"
	EnvironmentKeyPowerShell51            = "platform.powershell-5.1"
	EnvironmentKeyCandidateBuild          = "candidate.build"
	EnvironmentKeyToolchainGo             = "toolchain.go"
	EnvironmentKeyToolchainDelve          = "toolchain.delve"
	EnvironmentKeyToolchainPython         = "toolchain.python"
	EnvironmentKeyToolchainDebugpy        = "toolchain.debugpy"
	EnvironmentKeyToolchainNode           = "toolchain.node"
	EnvironmentKeyToolchainNPM            = "toolchain.npm"
	EnvironmentKeyToolchainVSBuildTools   = "toolchain.vs-build-tools"
	EnvironmentKeyToolchainCMake          = "toolchain.cmake"
	EnvironmentKeyToolchainNinja          = "toolchain.ninja"
	EnvironmentKeyToolchainLLVM           = "toolchain.llvm"
	EnvironmentKeyToolchainJDK            = "toolchain.jdk"
	EnvironmentKeyToolchainKotlin         = "toolchain.kotlin"
	EnvironmentKeyToolchainRust           = "toolchain.rust"
	EnvironmentKeyToolchainRustMSVCTarget = "toolchain.rust-msvc-target"
	EnvironmentKeyBrowserChrome           = "browser.chrome"
	EnvironmentKeyBrowserEdge             = "browser.edge"
	EnvironmentKeyAdapterGo               = "adapter.go"
	EnvironmentKeyAdapterPython           = "adapter.python"
	EnvironmentKeyAdapterNode             = "adapter.node-js-debug"
	EnvironmentKeyAdapterNative           = "adapter.native"
	EnvironmentKeyAdapterJVM              = "adapter.jvm"
	EnvironmentKeyRemoteLinuxHost         = "remote.linux-host"
	EnvironmentKeyRemoteLinuxAgent        = "remote.linux-agent"
	EnvironmentKeyRemoteTunnel            = "remote.linux-tunnel"
	EnvironmentKeyRemoteLinuxMachine      = "remote.linux-machine"
	EnvironmentKeyRemoteManagedBaseline   = "remote.linux-managed-baseline"
	EnvironmentKeyRemoteDirectExposure    = "remote.linux-direct-exposure"
	EnvironmentKeyRemoteGovernance        = "remote.linux-governance-attestation"
	EnvironmentKeySecurityApproval        = "security.approval-readiness"
	EnvironmentKeySecurityCredential      = "security.credential-readiness"
)

var (
	platformLinePattern     = regexp.MustCompile(`(?m)^(windows_product|windows_build|windows_display_version|windows_installation_type|windows_ubr|windows_installed_kbs|arch|powershell|powershell_edition)=([^\r\n]*)\r?$`)
	goVersionPattern        = regexp.MustCompile(`(?i)^go version go([^\s]+)\s+([^\s]+)`)
	delveVersionPattern     = regexp.MustCompile(`(?mi)^Version:\s*([^\s]+)`)
	pythonVersionPattern    = regexp.MustCompile(`(?i)^Python\s+([^\s]+)`)
	plainVersionPattern     = regexp.MustCompile(`^v?([0-9]+(?:\.[0-9A-Za-z+-]+)+)$`)
	cmakeVersionPattern     = regexp.MustCompile(`(?i)^cmake version\s+([^\s]+)`)
	llvmVersionPattern      = regexp.MustCompile(`(?im)^(?:clang|lldb) version\s+([^\s]+)`)
	llvmTargetPattern       = regexp.MustCompile(`(?mi)^Target:\s*([^\s]+)`)
	temurinVersionPattern   = regexp.MustCompile(`(?i)Temurin-([0-9][0-9A-Za-z.+_-]*)`)
	javaBuildVersionPattern = regexp.MustCompile(`(?i)\bbuild\s+([0-9][0-9A-Za-z.+_-]*)`)
	kotlinVersionPattern    = regexp.MustCompile(`(?i)kotlinc-jvm\s+([^\s]+)`)
	rustVersionPattern      = regexp.MustCompile(`(?i)^rustc\s+([^\s]+)`)
	chromeVersionPattern    = regexp.MustCompile(`(?i)^Google Chrome\s+([^\s]+)`)
	edgeVersionPattern      = regexp.MustCompile(`(?i)^Microsoft Edge\s+([^\s]+)`)
)

// EnvironmentProbeFact 是从实际命令输出解析出的安全事实。
type EnvironmentProbeFact struct {
	Version    string            `json:"version,omitempty"`
	Identity   string            `json:"identity,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ParseEnvironmentProbe 从一个已执行的固定只读 probe 输出中提取 observed 事实。
//
// 参数：
//   - key: 环境 prerequisite 的稳定 key
//   - stdout: probe 的标准输出
//   - stderr: 某些工具按惯例输出版本信息的标准错误
//
// 返回：
//   - 仅包含稳定 version/identity/attributes 的安全事实
//   - 输出为空、格式漂移或 key 没有 parser 时的错误
//
// 注意：expected 值不会参与解析；无法从真实输出证明的版本必须报错，不能回填 expected。
func ParseEnvironmentProbe(key, stdout, stderr string) (EnvironmentProbeFact, error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationEnvironmentParser").WithField("prerequisite", key)
	log.Info("开始解析 Windows 环境只读 probe 输出")
	fact, err := parseEnvironmentProbe(key, strings.TrimSpace(stdout), strings.TrimSpace(stderr))
	if err != nil {
		log.WithField("cause_code", "parse_failed").Error("Windows 环境只读 probe 输出解析失败")
		return EnvironmentProbeFact{}, err
	}
	log.WithFields(map[string]any{"prerequisite": key, "observed_version": fact.Version, "observed_identity": fact.Identity}).Info("Windows 环境只读 probe 输出解析完成")
	return fact, nil
}

func parseEnvironmentProbe(key, stdout, stderr string) (EnvironmentProbeFact, error) {
	combined := strings.TrimSpace(strings.Join(nonEmptyStrings(stdout, stderr), "\n"))
	if combined == "" {
		return EnvironmentProbeFact{}, fmt.Errorf("environment probe %s produced no observed output", key)
	}

	switch key {
	case EnvironmentKeyPlatformWindows, EnvironmentKeyPlatformArchitecture, EnvironmentKeyPowerShell51:
		attributes := parsePlatformLines(combined)
		if len(attributes) == 0 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		observation := WindowsPlatformObservation{
			ProductName:      attributes["windows_product"],
			CurrentBuild:     attributes["windows_build"],
			DisplayVersion:   attributes["windows_display_version"],
			InstallationType: attributes["windows_installation_type"],
			Architecture:     attributes["arch"],
			UBR:              attributes["windows_ubr"],
			InstalledKBs:     strings.Split(attributes["windows_installed_kbs"], ","),
		}
		powershell := attributes["powershell"]
		powerShellEdition := attributes["powershell_edition"]
		archiveAttributes := windowsPlatformObservationAttributes(observation)
		archiveAttributes["powershell_version"] = strings.TrimSpace(powershell)
		archiveAttributes["powershell_edition"] = strings.TrimSpace(powerShellEdition)
		switch key {
		case EnvironmentKeyPlatformArchitecture:
			return EnvironmentProbeFact{Identity: normalizeWindowsValidationArchitecture(observation.Architecture), Attributes: archiveAttributes}, nil
		case EnvironmentKeyPowerShell51:
			return EnvironmentProbeFact{Version: powershell, Identity: "powershell.exe", Attributes: archiveAttributes}, nil
		default:
			installationType := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(observation.InstallationType), " ", "-"))
			architecture := normalizeWindowsValidationArchitecture(observation.Architecture)
			identity := ""
			if installationType != "" || architecture != "" {
				identity = "windows-" + installationType + "/" + architecture
			}
			return EnvironmentProbeFact{Version: strings.TrimSpace(observation.CurrentBuild), Identity: identity, Attributes: archiveAttributes}, nil
		}
	case EnvironmentKeyToolchainGo:
		matches := goVersionPattern.FindStringSubmatch(stdout)
		if len(matches) != 3 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: matches[1], Identity: strings.ToLower(matches[2])}, nil
	case EnvironmentKeyToolchainDelve, EnvironmentKeyAdapterGo:
		matches := delveVersionPattern.FindStringSubmatch(combined)
		if len(matches) != 2 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: matches[1], Identity: "dlv"}, nil
	case EnvironmentKeyToolchainPython:
		matches := pythonVersionPattern.FindStringSubmatch(stdout)
		if len(matches) != 2 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: matches[1], Identity: "python"}, nil
	case EnvironmentKeyToolchainDebugpy, EnvironmentKeyAdapterPython:
		return parsePlainVersionFact(key, combined, "debugpy")
	case EnvironmentKeyToolchainNode:
		return parsePlainVersionFact(key, stdout, "node")
	case EnvironmentKeyToolchainNPM:
		return parsePlainVersionFact(key, stdout, "npm")
	case EnvironmentKeyToolchainVSBuildTools:
		return parsePlainVersionFact(key, stdout, "vs-build-tools")
	case EnvironmentKeyToolchainCMake:
		matches := cmakeVersionPattern.FindStringSubmatch(stdout)
		if len(matches) != 2 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: matches[1], Identity: "cmake"}, nil
	case EnvironmentKeyToolchainNinja:
		return parsePlainVersionFact(key, stdout, "ninja")
	case EnvironmentKeyToolchainLLVM:
		matches := llvmVersionPattern.FindStringSubmatch(combined)
		if len(matches) != 2 {
			return EnvironmentProbeFact{}, parseError(key)
		}
		identity := "clang-cl"
		if target := llvmTargetPattern.FindStringSubmatch(combined); len(target) == 2 {
			identity = strings.ToLower(target[1])
		}
		return EnvironmentProbeFact{Version: matches[1], Identity: identity}, nil
	case EnvironmentKeyToolchainJDK:
		version := firstSubmatch(temurinVersionPattern, combined)
		if version == "" {
			version = firstSubmatch(javaBuildVersionPattern, combined)
		}
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		version = strings.TrimSuffix(version, "-LTS")
		return EnvironmentProbeFact{Version: version, Identity: "openjdk"}, nil
	case EnvironmentKeyToolchainKotlin:
		version := firstSubmatch(kotlinVersionPattern, combined)
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: version, Identity: "kotlinc-jvm"}, nil
	case EnvironmentKeyToolchainRust:
		version := firstSubmatch(rustVersionPattern, stdout)
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: version, Identity: "rustc"}, nil
	case EnvironmentKeyToolchainRustMSVCTarget:
		for _, line := range strings.Fields(combined) {
			if line == "x86_64-pc-windows-msvc" {
				return EnvironmentProbeFact{Identity: line}, nil
			}
		}
		return EnvironmentProbeFact{}, parseError(key)
	case EnvironmentKeyAdapterNative:
		version := firstSubmatch(llvmVersionPattern, combined)
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: version, Identity: "lldb-dap"}, nil
	case EnvironmentKeyBrowserChrome:
		version := firstSubmatch(chromeVersionPattern, combined)
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: version, Identity: "chrome"}, nil
	case EnvironmentKeyBrowserEdge:
		version := firstSubmatch(edgeVersionPattern, combined)
		if version == "" {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Version: version, Identity: "msedge"}, nil
	case EnvironmentKeyAdapterNode:
		fact, err := parsePlainVersionFact(key, combined, "dapDebugServer.js")
		return fact, err
	case EnvironmentKeyAdapterJVM:
		identity := strings.TrimSpace(combined)
		if strings.ContainsAny(identity, "\r\n\t") {
			return EnvironmentProbeFact{}, parseError(key)
		}
		return EnvironmentProbeFact{Identity: identity}, nil
	default:
		return EnvironmentProbeFact{}, fmt.Errorf("environment probe %s has no stable parser", key)
	}
}

func parsePlatformLines(value string) map[string]string {
	out := map[string]string{}
	for _, matches := range platformLinePattern.FindAllStringSubmatch(value, -1) {
		if len(matches) == 3 {
			out[strings.ToLower(matches[1])] = strings.TrimSpace(matches[2])
		}
	}
	return out
}

func parsePlainVersionFact(key, value, identity string) (EnvironmentProbeFact, error) {
	matches := plainVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 2 {
		return EnvironmentProbeFact{}, parseError(key)
	}
	return EnvironmentProbeFact{Version: matches[1], Identity: identity}, nil
}

func firstSubmatch(pattern *regexp.Regexp, value string) string {
	matches := pattern.FindStringSubmatch(value)
	if len(matches) != 2 {
		return ""
	}
	return matches[1]
}

func parseError(key string) error {
	return fmt.Errorf("environment probe %s output does not match its observed fact contract", key)
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}
