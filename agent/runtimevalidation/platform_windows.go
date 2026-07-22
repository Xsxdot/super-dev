//go:build windows

// platform_windows.go 使用 IsWow64Process2/RtlGetVersion 检测原生 Windows 身份和 Wine。
//
// 职责：读取 native machine、当前 process machine 与 kernel version。
// 边界：拒绝 Wine/WOW 用户态异构执行，不调用 shell 或 WMI。
package runtimevalidation

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

const (
	imageFileMachineUnknown = 0
	imageFileMachineAMD64   = 0x8664
	imageFileMachineARM64   = 0xAA64
)

func detectNativeHostIdentity() (HostIdentity, error) {
	var processMachine uint16
	var nativeMachine uint16
	if err := windows.IsWow64Process2(windows.CurrentProcess(), &processMachine, &nativeMachine); err != nil {
		return HostIdentity{}, fmt.Errorf("windows IsWow64Process2: %w", err)
	}
	machine := windowsMachineName(nativeMachine)
	architecture, err := normalizeMachineArchitecture(machine)
	if err != nil {
		return HostIdentity{}, err
	}
	version := windows.RtlGetVersion()
	kernel := fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber)
	compatibility := ""
	ntdll := windows.NewLazySystemDLL("ntdll.dll")
	if ntdll.NewProc("wine_get_version").Find() == nil {
		compatibility = "wine"
	} else if processMachine != imageFileMachineUnknown || runtime.GOARCH != architecture {
		compatibility = "windows-userland-architecture-mismatch"
	}
	return HostIdentity{
		OS: "windows", Architecture: architecture, Kernel: kernel, Machine: machine,
		Native: compatibility == "", CompatibilityLayer: compatibility,
		DetectionSource: "windows:IsWow64Process2+RtlGetVersion+ntdll",
	}, nil
}

func windowsMachineName(machine uint16) string {
	switch machine {
	case imageFileMachineAMD64:
		return "amd64"
	case imageFileMachineARM64:
		return "arm64"
	default:
		return fmt.Sprintf("machine-0x%x", machine)
	}
}
