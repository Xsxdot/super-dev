// installer 平台与二进制解析。
//
// 职责：
//   - 将远端系统信息归一化为支持的安装平台
//   - 根据平台定位本地随包携带的 agent 二进制
//
// 边界：
//   - 不执行远端探测命令
//   - 不构建或下载二进制文件
package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Platform 是远端 agent 二进制目标平台。
type Platform struct {
	OS   string
	Arch string
}

// String 返回二进制命名中使用的平台字符串。
//
// 返回：
//   - 形如 linux/amd64 的平台标识
func (p Platform) String() string {
	return p.OS + "/" + p.Arch
}

// BinaryName 返回该平台的远端 agent 文件名。
//
// 返回：
//   - 形如 superdev-agent-linux-amd64 的文件名；Windows 带 .exe 后缀
func (p Platform) BinaryName() string {
	name := "superdev-agent-" + p.OS + "-" + p.Arch
	if p.OS == "windows" {
		name += ".exe"
	}
	return name
}

// NormalizePlatform 将远端 OS/arch 探测输出归一化成受支持的平台。
//
// 参数：
//   - osName: `uname -s`、`cmd /c ver` 或等价 OS 输出
//   - machine: `uname -m` 或 `%PROCESSOR_ARCHITECTURE%` 输出
//
// 返回：
//   - 支持的安装平台
//   - 不支持的 OS 或架构错误
func NormalizePlatform(osName, machine string) (Platform, error) {
	osValue := strings.ToLower(strings.TrimSpace(osName))
	switch osValue {
	case "darwin":
		osValue = "darwin"
	case "linux":
		osValue = "linux"
	case "windows", "windows_nt", "mingw64_nt", "msys", "cygwin_nt":
		osValue = "windows"
	default:
		if strings.HasPrefix(osValue, "mingw64_nt-") || strings.HasPrefix(osValue, "msys_nt-") ||
			strings.HasPrefix(osValue, "cygwin_nt-") || strings.Contains(osValue, "windows") {
			osValue = "windows"
		} else {
			return Platform{}, fmt.Errorf("unsupported os %q", strings.TrimSpace(osName))
		}
	}

	arch := strings.ToLower(strings.TrimSpace(machine))
	switch arch {
	case "x86_64", "amd64", "amd64 ":
		arch = "amd64"
	case "arm64", "aarch64":
		arch = "arm64"
	default:
		return Platform{}, fmt.Errorf("unsupported arch %q", strings.TrimSpace(machine))
	}

	return Platform{OS: osValue, Arch: arch}, nil
}

// ResolveBinary 返回指定平台的本地安装二进制路径。
//
// 参数：
//   - binaryDir: 随桌面包携带的远端 agent 二进制目录
//   - platform: 目标平台
//
// 返回：
//   - 可上传的本地二进制路径
//   - 目录缺失或文件缺失错误
func ResolveBinary(binaryDir string, platform Platform) (string, error) {
	if strings.TrimSpace(binaryDir) == "" {
		return "", fmt.Errorf("remote install binaries are not available")
	}
	path := filepath.Join(binaryDir, platform.BinaryName())
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("missing remote install binary %s: %w", platform.BinaryName(), err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("missing remote install binary %s: path is a directory", platform.BinaryName())
	}
	return path, nil
}
