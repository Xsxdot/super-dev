//go:build linux

// system_linux.go 读取 Linux 内核架构与机器身份原始值。
//
// 职责：
//   - 从 uname 读取真实 kernel architecture
//   - 按 Linux 标准位置读取 machine-id，供 service.go 立即哈希
//
// 边界：
//   - 原始 machine-id 不向包外暴露
//   - 不读取 hostname 或网络地址
package remoteobservation

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func readKernelArchitecture() string {
	var value unix.Utsname
	if err := unix.Uname(&value); err != nil {
		return ""
	}
	out := make([]byte, 0, len(value.Machine))
	for _, char := range value.Machine {
		if char == 0 {
			break
		}
		out = append(out, byte(char))
	}
	return strings.TrimSpace(string(out))
}

func readMachineIdentity() ([]byte, error) {
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil {
		return data, nil
	}
	return os.ReadFile("/var/lib/dbus/machine-id")
}
