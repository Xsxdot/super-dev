// powershell_contract.go 校验便携包对 Windows PowerShell 5.1 暴露的原生入口合同。
//
// 职责：
//   - 在打包和 Windows 执行前验证 shipped PowerShell 源码字节；
//   - 拒绝需要外部编码 loader 才能被 Windows PowerShell 5.1 正确读取的入口。
//
// 边界：
//   - 不执行 PowerShell、安装器或验证场景；
//   - 不重写或重新编码源文件来修复失败输入。
package windowsvalidation

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/xsxdot/gokit/logger"
)

var windowsPowerShellEntrypoints = []string{
	"Prepare-Validation.ps1",
	"Run-Validation.ps1",
	"Cleanup-Validation.ps1",
}

var windowsPowerShellRunbookCommands = []string{
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane msi_smoke`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <msi-backup>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <msi-id> -BackupDirectory <msi-backup> -RestoreUserState`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane nsis_core`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <nsis-backup>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <nsis-id> -BackupDirectory <nsis-backup> -RestoreUserState`,
}

var powershellConsoleUTF8Pattern = regexp.MustCompile(`(?im)^\s*\[Console\]::OutputEncoding\s*=\s*\[System\.Text\.UTF8Encoding\]::new\(\$false\)\s*$`)
var powershellNativePipelineUTF8Pattern = regexp.MustCompile(`(?im)^\s*\$OutputEncoding\s*=\s*\[Console\]::OutputEncoding\s*$`)

func validatePowerShell51Entrypoints(root string) (contractErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationPowerShellContract")
	log.WithField("entrypoint_count", len(windowsPowerShellEntrypoints)).Info("开始校验 Windows PowerShell 5.1 原生入口合同")
	defer func() {
		if contractErr != nil {
			log.WithErr(contractErr).Error("Windows PowerShell 5.1 原生入口合同校验失败")
		}
	}()
	for _, name := range windowsPowerShellEntrypoints {
		content, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return fmt.Errorf("read PowerShell entrypoint %s: %w", name, err)
		}
		// Windows PowerShell 5.1 会把无 BOM 文件按系统 ANSI 代码页解释；验证包包含中文注释和日志，
		// 因此必须在 shipped bytes 上保留 UTF-8 BOM，不能依赖执行者临时 loader。
		if !bytes.HasPrefix(content, []byte{0xef, 0xbb, 0xbf}) {
			return fmt.Errorf("PowerShell entrypoint %s must start with a UTF-8 BOM for Windows PowerShell 5.1", name)
		}
		body := content[3:]
		if !utf8.Valid(body) {
			return fmt.Errorf("PowerShell entrypoint %s is not valid UTF-8 after its BOM", name)
		}
		if !powershellConsoleUTF8Pattern.Match(body) || !powershellNativePipelineUTF8Pattern.Match(body) {
			return fmt.Errorf("PowerShell entrypoint %s must initialize UTF-8 console output and native-pipeline encoding", name)
		}
	}
	runbook, err := os.ReadFile(filepath.Join(root, "Runbook.md"))
	if err != nil {
		return fmt.Errorf("read Windows validation Runbook: %w", err)
	}
	for _, command := range windowsPowerShellRunbookCommands {
		if strings.Count(string(runbook), command) != 1 {
			return fmt.Errorf("Runbook must use powershell.exe exactly once for command %q", command)
		}
	}
	log.WithFields(map[string]any{
		"entrypoint_count": len(windowsPowerShellEntrypoints),
		"runbook_commands": len(windowsPowerShellRunbookCommands),
	}).Info("Windows PowerShell 5.1 原生入口合同校验完成")
	return nil
}
