# Invoke-InstallerLifecycle.ps1 是安装器生命周期的公开标准用户入口。
#
# 职责：
#   - 只接受 install/start/stop/uninstall 固定动作选择；
#   - 在 Windows 10 22H2 x64 (build 19045) 标准用户进程中调用 packaged validation driver；
#   - 输出不含路径、命令行和凭据的结构化阶段日志。
#
# 边界：
#   - 不接收 attempted/succeeded/command/observation 等事实；
#   - 不执行安装器、进程或注册表操作，所有事实由 package-integrity 校验后的内部 helper 采集；
#   - 不提升自身权限；只有内部 helper 的 install/uninstall 固定分支可以请求 UAC。
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('install', 'start', 'stop', 'uninstall')]
    [string]$Action,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$BackupDirectory,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$InstallerPath,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$InstallDirectory,
    [string]$ResultsRoot = 'C:\SuperDevValidation\results'
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$script:lifecycleStage = 'entry'
$script:platformBuild = ''
$script:platformDisplayVersion = ''
$script:platformUBR = ''

function Write-LifecycleEvent {
    param([string]$Outcome, [string]$FailureCode = '')
    $record = [ordered]@{
        timestamp = [DateTime]::UtcNow.ToString('o')
        level = if ($Outcome -eq 'failed') { 'error' } else { 'info' }
        component = 'windows-validation-installer-lifecycle'
        stage = $script:lifecycleStage
        action = $Action
        outcome = $Outcome
    }
    if (-not [string]::IsNullOrWhiteSpace($FailureCode)) { $record.failure_code = $FailureCode }
    if (-not [string]::IsNullOrWhiteSpace($script:platformBuild)) {
        $record.windows_build = $script:platformBuild
        $record.windows_display_version = $script:platformDisplayVersion
        $record.windows_ubr = $script:platformUBR
        $record.support_scope = 'compatibility_only'
        $record.esu_evidence_status = 'not_mechanically_verified'
    }
    [Console]::Out.WriteLine(($record | ConvertTo-Json -Compress))
}

function Test-ProcessElevated {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

try {
    Write-LifecycleEvent 'started'
    $script:lifecycleStage = 'platform_gate'
    if ($env:OS -ne 'Windows_NT') { throw 'platform' }
    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -ne 'X64') { throw 'platform' }
    $windows = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop
    if ($null -eq $windows.PSObject.Properties['DisplayVersion'] -or $null -eq $windows.PSObject.Properties['UBR']) { throw 'platform' }
    $script:platformBuild = [string]$windows.CurrentBuildNumber
    $script:platformDisplayVersion = [string]$windows.DisplayVersion
    $script:platformUBR = [string]$windows.UBR
    $productName = ([string]$windows.ProductName).Trim()
    $ubrValue = 0
    $hasValidUBR = [int]::TryParse($script:platformUBR, [ref]$ubrValue) -and $ubrValue -gt 0
    $isWindows10Product = $productName -eq 'Windows 10' -or $productName.StartsWith('Windows 10 ', [StringComparison]::Ordinal)
    if (-not $isWindows10Product -or [string]$windows.InstallationType -ne 'Client' -or
        $script:platformDisplayVersion -ne '22H2' -or $script:platformBuild -ne '19045' -or -not $hasValidUBR) {
        throw 'platform'
    }
    if (Test-ProcessElevated) { throw 'elevated_parent' }

    $script:lifecycleStage = 'fixed_driver'
    $driverPath = Join-Path $PSScriptRoot 'bin\superdev-windows-validation.exe'
    if (-not (Test-Path -LiteralPath $driverPath -PathType Leaf)) { throw 'driver_missing' }
    & $driverPath '--execute-installer-lifecycle' '--lifecycle-action' $Action '--package-root' $PSScriptRoot '--prepared-backup' $BackupDirectory '--installer-path' $InstallerPath '--install-directory' $InstallDirectory '--results-root' $ResultsRoot
    if ($LASTEXITCODE -ne 0) { throw 'fixed_driver_failed' }

    $script:lifecycleStage = 'complete'
    Write-LifecycleEvent 'succeeded'
    exit 0
} catch {
    $failureCode = switch ([string]$_.Exception.Message) {
        'platform' { 'platform_not_windows_10_22h2_build_19045_x64' }
        'elevated_parent' { 'elevated_parent_rejected' }
        'driver_missing' { 'packaged_driver_missing' }
        default { 'fixed_driver_failed' }
    }
    Write-LifecycleEvent 'failed' $failureCode
    exit 1
}
