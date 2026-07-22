// platform.go 定义原生宿主身份和 bundle target 的 fail-closed 匹配合同。
//
// 职责：
//   - 通过平台原生 API 获取 kernel、machine architecture 与兼容层状态
//   - 拒绝 Rosetta、Wine、用户态异构执行或 target mismatch 冒充原生 PASS
//   - 把检测来源写入可审计 HostIdentity
//
// 边界：
//   - runtime.GOOS/GOARCH 只用于识别当前二进制用户态，不作为原生机器真相
//   - 不探测语言工具链或 foundation 内容
package runtimevalidation

import (
	"fmt"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

// Target 标识一个 validation bundle 的原生 OS/architecture。
type Target struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

// HostIdentity 保存平台原生 API 观察到的 kernel、machine 和兼容层事实。
type HostIdentity struct {
	OS                 string `json:"os"`
	Architecture       string `json:"architecture"`
	Kernel             string `json:"kernel"`
	Machine            string `json:"machine"`
	Native             bool   `json:"native"`
	CompatibilityLayer string `json:"compatibility_layer,omitempty"`
	DetectionSource    string `json:"detection_source"`
}

// DetectHostIdentity 使用当前平台原生 API 获取宿主身份。
//
// 返回：
//   - kernel/machine/compatibility layer 事实
//   - 原生 API 不可用或 machine architecture 不受支持时的错误
//
// 注意：检测失败必须 BLOCKED，runner 不得回退到 bundle target 自报身份。
func DetectHostIdentity() (HostIdentity, error) {
	identity, err := detectNativeHostIdentity()
	log := logger.GetLogger().WithEntryName("RuntimeValidationPlatform")
	if err != nil {
		log.WithErr(err).Error("runtime validation 原生宿主身份检测失败")
		return HostIdentity{}, err
	}
	log.WithFields(map[string]any{
		"os": identity.OS, "architecture": identity.Architecture, "kernel": identity.Kernel,
		"machine": identity.Machine, "native": identity.Native, "compatibility_layer": identity.CompatibilityLayer,
		"detection_source": identity.DetectionSource,
	}).Info("runtime validation 原生宿主身份检测完成")
	return identity, nil
}

// ValidateHostTarget 比较 bundle target 与原生宿主身份并派生 strict gate。
//
// 参数：
//   - target: targets.txt 当前 bundle 的 OS/architecture
//   - identity: DetectHostIdentity 的原生观察结果
//
// 返回：
//   - 精确匹配且无兼容层时 PASS，否则 BLOCKED；非法 target 时 FAIL
//
// 注意：交叉编译/打包成功不参与此判定。
func ValidateHostTarget(target Target, identity HostIdentity) CheckResult {
	result := CheckResult{ID: "native-host"}
	if !supportedTarget(target) {
		result.Status = StatusFail
		result.Cause = Cause{Code: "unsupported_bundle_target", Message: fmt.Sprintf("unsupported bundle target %s/%s", target.OS, target.Architecture), Source: result.ID}
		return result
	}
	if !identity.Native || strings.TrimSpace(identity.CompatibilityLayer) != "" {
		result.Status = StatusBlocked
		result.Cause = Cause{Code: "compatibility_layer_active", Message: fmt.Sprintf("native target validation refuses compatibility layer %q", identity.CompatibilityLayer), Source: result.ID}
		return result
	}
	if target.OS != identity.OS || target.Architecture != identity.Architecture {
		result.Status = StatusBlocked
		result.Cause = Cause{Code: "native_host_mismatch", Message: fmt.Sprintf("bundle target %s/%s does not match native host %s/%s", target.OS, target.Architecture, identity.OS, identity.Architecture), Source: result.ID}
		return result
	}
	result.Status = StatusPass
	return result
}

func supportedTarget(target Target) bool {
	switch target.OS + "/" + target.Architecture {
	case "darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "windows/amd64":
		return true
	default:
		return false
	}
}

func normalizeMachineArchitecture(machine string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(machine)) {
	case "x86_64", "amd64", "x64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported native machine architecture %q", machine)
	}
}
