//go:build windows

// installer_lifecycle_executor_windows.go invokes the package-integrity-verified internal PowerShell helper.
//
// 职责：拒绝 elevated driver，并用 stock Windows PowerShell 5.1 执行 driver-owned request/output paths。
// 边界：不接受 caller-owned result path，不解释 action result，不记录 PowerShell 输出或用户路径。
package windowsvalidation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func validateInstallerLifecyclePlatform() error {
	if err := ValidateExecutionPlatform(runtime.GOOS, runtime.GOARCH); err != nil {
		return err
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return fmt.Errorf("read Windows product identity")
	}
	defer key.Close()
	productName, _, productErr := key.GetStringValue("ProductName")
	installationType, _, typeErr := key.GetStringValue("InstallationType")
	buildText, _, buildErr := key.GetStringValue("CurrentBuildNumber")
	displayVersion, _, displayErr := key.GetStringValue("DisplayVersion")
	ubr, _, ubrErr := key.GetIntegerValue("UBR")
	if productErr != nil || typeErr != nil || buildErr != nil || displayErr != nil || ubrErr != nil {
		return fmt.Errorf("read strict Windows validation platform identity")
	}
	if err := ValidateWindows10ValidationPlatform(WindowsPlatformObservation{
		ProductName: productName, CurrentBuild: buildText, DisplayVersion: displayVersion,
		InstallationType: installationType, Architecture: runtime.GOARCH, UBR: strconv.FormatUint(ubr, 10),
	}); err != nil {
		return fmt.Errorf("installer lifecycle platform gate: %w", err)
	}
	return nil
}

func installerLifecycleProcessElevated() (bool, error) {
	return windows.Token(0).IsElevated(), nil
}

func executeInstallerLifecycleHelper(ctx context.Context, helperPath, requestPath, outputPath string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("installer lifecycle context expired before helper launch")
	}
	var request installerLifecycleExecutorRequest
	if err := readStrictInstallerLifecycleJSON(requestPath, &request); err != nil {
		return fmt.Errorf("read lifecycle helper request")
	}
	if request.SchemaVersion != 1 || request.Kind != installerLifecycleExecutorRequestKind || !isInstallerLifecycleAction(request.Action) {
		return fmt.Errorf("lifecycle helper request identity is invalid")
	}
	systemRoot, err := windows.GetWindowsDirectory()
	if err != nil || strings.TrimSpace(systemRoot) == "" {
		return fmt.Errorf("locate stock Windows PowerShell")
	}
	powerShellPath := filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	if _, err := fileIdentity(filepath.Dir(powerShellPath), powerShellPath); err != nil {
		return fmt.Errorf("identify stock Windows PowerShell")
	}
	command := exec.Command(powerShellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", helperPath, "-RequestPath", requestPath, "-OutputPath", outputPath)
	command.Stdout = nil
	command.Stderr = nil
	// 不使用 CommandContext：主动终止 helper 会让 install/uninstall 的 UAC 子进程脱离活动锁覆盖。
	// driver 同步等待；若 driver 被外部终止，helper 仍独立持有活动锁直到观察和 result 写入结束。
	err = command.Run()
	if err == nil {
		return nil
	}
	// 动作 FAIL 时 helper 先固化结构化结果再非零退出；只有没有结果文件才属于 executor 基础设施失败。
	if _, statErr := os.Stat(outputPath); statErr == nil {
		return nil
	}
	return fmt.Errorf("fixed installer lifecycle helper failed before writing a result")
}
