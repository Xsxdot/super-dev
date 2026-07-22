// powershell_contract.go 校验便携包对 Windows PowerShell 5.1 暴露的 Runbook 入口合同。
//
// 职责：
//   - 在打包前确认 Runbook 引用的 shipped PowerShell 入口真实存在；
//   - 锁定操作者可复制的 powershell.exe -File 命令。
//
// 边界：
//   - 不在非 Windows 主机模拟 PowerShell 解析或编码行为；
//   - 不规定 BOM、变量名或编码初始化语法等具体修复手段。
package windowsvalidation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xsxdot/gokit/logger"
)

var windowsPowerShellEntrypoints = []string{
	"Prepare-Validation.ps1",
	"Invoke-InstallerLifecycle.ps1",
	"Run-Validation.ps1",
	"Cleanup-Validation.ps1",
}

var windowsPowerShellInternalHelpers = []string{
	filepath.Join("internal", "Invoke-InstallerLifecycleAction.ps1"),
}

var windowsPowerShellRunbookCommands = []string{
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action install -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action start -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane msi_smoke -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <msi-backup>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action stop -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action uninstall -BackupDirectory <msi-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64_en-US.msi -InstallDirectory <msi-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <msi-id> -BackupDirectory <msi-backup> -RestoreUserState`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action install -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action start -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane nsis_core -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <nsis-backup>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action stop -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Invoke-InstallerLifecycle.ps1 -Action uninstall -BackupDirectory <nsis-backup> -InstallerPath C:\SuperDevValidation\installers\SuperDev_0.2.1_x64-setup.exe -InstallDirectory <nsis-install-dir>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <nsis-id> -BackupDirectory <nsis-backup> -RestoreUserState`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Prepare-Validation.ps1 -Lane core_only -RuntimeInput ..\runtime-input.json`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Run-Validation.ps1 -Lane core_only -RuntimeInput ..\runtime-input.json -PreparedBackupDirectory <core-backup>`,
	`powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\Cleanup-Validation.ps1 -CampaignId <core-id> -BackupDirectory <core-backup> -RestoreUserState`,
}

func validateWindowsPowerShellRunbookContract(root string) (contractErr error) {
	log := logger.GetLogger().WithEntryName("WindowsValidationPowerShellContract").WithField("package_root", root)
	log.WithField("entrypoint_count", len(windowsPowerShellEntrypoints)).Info("开始校验 Windows PowerShell 5.1 原生入口合同")
	defer func() {
		if contractErr != nil {
			log.WithErr(contractErr).Error("Windows PowerShell 5.1 原生入口合同校验失败")
		}
	}()
	for _, name := range append(append([]string{}, windowsPowerShellEntrypoints...), windowsPowerShellInternalHelpers...) {
		path := filepath.Join(root, name)
		fileLog := log.WithField("path", path)
		fileLog.Debug("开始检查 Windows PowerShell 入口文件")
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat PowerShell entrypoint %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("PowerShell entrypoint %s is not a regular file", name)
		}
		fileLog.WithField("size_bytes", info.Size()).Debug("Windows PowerShell 入口文件检查完成")
	}
	runbookPath := filepath.Join(root, "Runbook.md")
	runbookLog := log.WithField("path", runbookPath)
	runbookLog.Debug("开始读取 Windows 验证 Runbook")
	runbook, err := os.ReadFile(runbookPath)
	if err != nil {
		return fmt.Errorf("read Windows validation Runbook: %w", err)
	}
	runbookLog.WithField("size_bytes", len(runbook)).Debug("Windows 验证 Runbook 读取完成")
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
