//go:build windows

// system_windows.go 读取 Windows 内核架构与机器身份原始值。
//
// 职责：
//   - 区分进程架构与 WOW64 环境暴露的原生内核架构
//   - 从 MachineGuid 读取稳定机器身份，供 service.go 立即哈希
//
// 边界：
//   - 原始 MachineGuid 不向包外暴露
//   - 不读取 ComputerName、hostname 或网络地址
package remoteobservation

import (
	"os"
	"runtime"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func readKernelArchitecture() string {
	arch := strings.TrimSpace(os.Getenv("PROCESSOR_ARCHITEW6432"))
	if arch == "" {
		arch = strings.TrimSpace(os.Getenv("PROCESSOR_ARCHITECTURE"))
	}
	switch strings.ToLower(arch) {
	case "amd64", "x86_64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	case "x86", "i386", "i686":
		return "386"
	case "":
		return runtime.GOARCH
	default:
		return strings.ToLower(arch)
	}
}

func readMachineIdentity() ([]byte, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("MachineGuid")
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}
