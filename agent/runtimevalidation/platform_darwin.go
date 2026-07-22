//go:build darwin

// platform_darwin.go 使用 Darwin sysctl/uname 检测原生 machine 和 Rosetta 状态。
//
// 职责：读取 hw.machine、sysctl.proc_translated 与 kernel release。
// 边界：不把 runtime.GOARCH 当作原生硬件真相。
package runtimevalidation

import (
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

func detectNativeHostIdentity() (HostIdentity, error) {
	machine, err := unix.Sysctl("hw.machine")
	if err != nil {
		return HostIdentity{}, fmt.Errorf("darwin sysctl hw.machine: %w", err)
	}
	architecture, err := normalizeMachineArchitecture(machine)
	if err != nil {
		return HostIdentity{}, err
	}
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return HostIdentity{}, fmt.Errorf("darwin uname: %w", err)
	}
	translated := false
	if value, sysctlErr := unix.Sysctl("sysctl.proc_translated"); sysctlErr == nil {
		translated = strings.TrimSpace(value) == "1"
	}
	compatibility := ""
	if translated {
		compatibility = "rosetta"
	} else if runtime.GOARCH != architecture {
		compatibility = "darwin-userland-architecture-mismatch"
	}
	return HostIdentity{
		OS: "darwin", Architecture: architecture, Kernel: darwinChars(uname.Release[:]), Machine: machine,
		Native: compatibility == "", CompatibilityLayer: compatibility,
		DetectionSource: "darwin:sysctl(hw.machine,sysctl.proc_translated)+uname",
	}, nil
}

func darwinChars(value []byte) string {
	bytes := make([]byte, 0, len(value))
	for _, item := range value {
		if item == 0 {
			break
		}
		bytes = append(bytes, item)
	}
	return string(bytes)
}
