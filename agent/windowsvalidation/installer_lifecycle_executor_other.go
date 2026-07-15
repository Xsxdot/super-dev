//go:build !windows

// installer_lifecycle_executor_other.go prevents non-Windows hosts from executing installer actions.
//
// 职责：提供 production executor 的非 Windows fail-closed 实现。
// 边界：单元测试通过显式依赖注入验证状态机，不在非 Windows 模拟 UAC 或进程事实。
package windowsvalidation

import (
	"context"
	"fmt"
	"runtime"
)

func validateInstallerLifecyclePlatform() error {
	return ValidateExecutionPlatform(runtime.GOOS, runtime.GOARCH)
}

func installerLifecycleProcessElevated() (bool, error) {
	return false, fmt.Errorf("installer lifecycle execution requires Windows")
}

func executeInstallerLifecycleHelper(context.Context, string, string, string) error {
	return fmt.Errorf("installer lifecycle execution requires Windows")
}
