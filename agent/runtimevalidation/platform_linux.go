//go:build linux

// platform_linux.go 使用 Linux uname 检测 kernel/machine 和用户态异构执行。
//
// 职责：读取原生 machine architecture，并拒绝与当前用户态二进制不一致。
// 边界：不读取 bundle target 自报身份，也不执行外部 uname 命令。
package runtimevalidation

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

func detectNativeHostIdentity() (HostIdentity, error) {
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return HostIdentity{}, fmt.Errorf("linux uname: %w", err)
	}
	machine := linuxChars(uname.Machine[:])
	architecture, err := normalizeMachineArchitecture(machine)
	if err != nil {
		return HostIdentity{}, err
	}
	compatibility := ""
	if runtime.GOARCH != architecture {
		compatibility = "linux-userland-architecture-mismatch"
	}
	return HostIdentity{
		OS: "linux", Architecture: architecture, Kernel: linuxChars(uname.Release[:]), Machine: machine,
		Native: compatibility == "", CompatibilityLayer: compatibility,
		DetectionSource: "linux:uname(2)",
	}, nil
}

func linuxChars(value []byte) string {
	bytes := make([]byte, 0, len(value))
	for _, item := range value {
		if item == 0 {
			break
		}
		bytes = append(bytes, item)
	}
	return string(bytes)
}
