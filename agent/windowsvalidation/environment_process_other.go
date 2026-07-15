//go:build !windows

// environment_process_other.go 在非 Windows 主机执行普通环境只读命令。
//
// 职责：
//   - 按已校验的 executable/argv 创建可取消进程
//
// 边界：
//   - 不解释 Windows batch command line
//   - 不经 shell 执行任何环境 probe
package windowsvalidation

import (
	"context"
	"fmt"
	"os/exec"
)

func newEnvironmentCommandProcess(ctx context.Context, invocation environmentCommandInvocation) (*exec.Cmd, error) {
	if invocation.WindowsCommandLine != "" {
		return nil, fmt.Errorf("Windows batch invocation is unsupported on this platform")
	}
	return exec.CommandContext(ctx, invocation.Executable, invocation.Arguments...), nil
}

func trustedWindowsPowerShellPath() (string, error) {
	return "", fmt.Errorf("trusted Windows PowerShell is unavailable on this platform")
}

func trustedWindowsCommandPath() (string, error) {
	return "", fmt.Errorf("trusted Windows cmd.exe is unavailable on this platform")
}
