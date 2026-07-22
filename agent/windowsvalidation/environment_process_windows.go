//go:build windows

// environment_process_windows.go 用受信 System32 executor 启动 Windows 环境只读命令。
//
// 职责：
//   - 普通 PE executable 继续使用 argv 直接启动
//   - npm.cmd/kotlinc.bat 使用固定 SysProcAttr.CmdLine，绕过 Go makeCmdLine 对 cmd.exe 的不兼容 quoting
//
// 边界：
//   - 不接受调用方自由 shell 文本
//   - 不使用 PATH shadow 的 cmd.exe，也不放宽 common builder 的 batch allowlist
package windowsvalidation

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func newEnvironmentCommandProcess(ctx context.Context, invocation environmentCommandInvocation) (*exec.Cmd, error) {
	if invocation.WindowsCommandLine == "" {
		return exec.CommandContext(ctx, invocation.Executable, invocation.Arguments...), nil
	}
	if invocation.Executable != "cmd.exe" || len(invocation.Arguments) != 0 {
		return nil, fmt.Errorf("Windows batch invocation executor identity is invalid")
	}
	commandPath, err := trustedWindowsCommandPath()
	if err != nil {
		return nil, err
	}
	process := exec.CommandContext(ctx, commandPath)
	process.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: syscall.EscapeArg(commandPath) + " " + invocation.WindowsCommandLine,
	}
	return process, nil
}

func trustedWindowsPowerShellPath() (string, error) {
	return trustedWindowsSystemExecutable(filepath.Join("WindowsPowerShell", "v1.0", "powershell.exe"))
}

func trustedWindowsCommandPath() (string, error) {
	return trustedWindowsSystemExecutable("cmd.exe")
}

func trustedWindowsSystemExecutable(relativePath string) (string, error) {
	// GetSystemDirectory 从 Windows API 取得系统目录；SystemRoot/ComSpec/PATH
	// 全部被忽略，不能让调用方环境参与 executor 身份判定。
	systemDirectory, err := windows.GetSystemDirectory()
	if err != nil || strings.TrimSpace(systemDirectory) == "" {
		return "", fmt.Errorf("Windows system directory is unavailable")
	}
	candidate := filepath.Clean(filepath.Join(systemDirectory, relativePath))
	info, err := os.Stat(candidate)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("trusted Windows executor %s is unavailable", safeWindowsBase(candidate))
	}
	return candidate, nil
}
