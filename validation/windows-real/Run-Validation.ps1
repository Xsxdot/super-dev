# Run-Validation.ps1 is the Windows-native entry point for one frozen validation lane.
#
# Responsibilities:
#   - enforce Windows 10 22H2 x64 (build 19045), package and installer identity preflight;
#   - require the exact pre-install backup created by Prepare-Validation.ps1;
#   - collect one human-entered credential through a hidden prompt for functional lanes;
#   - invoke the fixed packaged driver for MSI smoke, NSIS core, or diagnostic core-only validation.
#
# Boundaries:
#   - this script never installs or uninstalls SuperDev and never requests elevation;
#   - UAC belongs only to the separately launched frozen installer;
#   - the one-time credential is never written to runtime input, command arguments, events, or disk;
#   - this script accepts no arbitrary command, MCP tool, or scenario path.
# Parameters:
#   - Lane selects msi_smoke, nsis_core, or core_only; RuntimeInput points to mutable machine facts outside the package;
#   - PreparedBackupDirectory binds the run to one completed preparation; the driver fixes Agent access to the lifecycle-proven loopback listener.
# Exit behavior:
#   - exits with the packaged driver result after all identity gates pass;
#   - exits 1 with structured pre-driver evidence when any gate or invocation fails.
# Notes:
#   - run from a normal Windows PowerShell 5.1 process after the matching official installer completes.
[CmdletBinding()]
param(
    [ValidateSet('msi_smoke', 'nsis_core', 'core_only')]
	[string]$Lane = 'nsis_core',
	[string]$RuntimeInput = '',
	[Parameter(Mandatory = $true)]
	[string]$PreparedBackupDirectory
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 默认沿用系统代码页；在第一次结构化事件写出前固定 UTF-8，
# 才能保证重定向日志与后续原生 driver 管道在不同 Windows 语言环境中保持同一字节合同。
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$script:validationCampaignId = ''
$script:platformFacts = $null
$debugCredentialEnvironmentName = 'SUPERDEV_WINDOWS_VALIDATION_DEBUG_CREDENTIAL'

function Write-ValidationEvent {
    param([string]$Level, [string]$Stage, [string]$Outcome, [hashtable]$Fields = @{})
    $record = [ordered]@{
        timestamp = [DateTime]::UtcNow.ToString('o')
        level = $Level
        component = 'windows-validation-entry'
        stage = $Stage
        outcome = $Outcome
        lane = $Lane
        campaign_id = $script:validationCampaignId
    }
    foreach ($key in $Fields.Keys) { $record[$key] = $Fields[$key] }
    # 直接写控制台而不是返回 pipeline 对象，避免后续若包装为返回值函数时混入日志文本。
    [Console]::Out.WriteLine(($record | ConvertTo-Json -Compress))
}

function Assert-WindowsX64 {
    if ($env:OS -ne 'Windows_NT') { throw 'This package executes functional validation only on Windows.' }
    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -ne 'X64') {
        throw 'Windows x64 is required.'
    }
    $windows = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop
    if ($null -eq $windows.PSObject.Properties['DisplayVersion'] -or $null -eq $windows.PSObject.Properties['UBR']) { throw 'Windows platform servicing identity is incomplete.' }
    $build = [string]$windows.CurrentBuildNumber
    $displayVersion = [string]$windows.DisplayVersion
    $ubr = [string]$windows.UBR
    $productName = [string]$windows.ProductName
    $installationType = [string]$windows.InstallationType
    $installedKBs = @(Get-HotFix -ErrorAction Stop | ForEach-Object { [string]$_.HotFixID } | Where-Object { $_ -match '^KB[0-9]+$' } | ForEach-Object { $_.ToUpperInvariant() } | Sort-Object -Unique)
    if ($productName -notlike 'Windows 10*' -or $installationType -ne 'Client' -or $displayVersion -ne '22H2' -or $build -ne '19045' -or $ubr -notmatch '^[1-9][0-9]*$' -or $installedKBs.Count -eq 0) {
        throw "Windows 10 Client 22H2 x64 build 19045 with servicing evidence is required; observed $productName $displayVersion build $build.$ubr."
    }
    # 这里只证明 compatibility 平台，不能由 UBR/KB 合成 ESU entitlement。
    $script:platformFacts = [ordered]@{
        current_build = $build
        display_version = $displayVersion
        ubr = $ubr
        installed_kbs = @($installedKBs)
        support_scope = 'compatibility_only'
        esu_evidence_status = 'not_mechanically_verified'
    }
}

function Assert-PackageFiles {
    param([string]$PackageRoot)
    $manifestPath = Join-Path $PackageRoot 'manifest\package-files.json'
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) { throw 'package-files.json is missing.' }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    foreach ($entry in $manifest.files) {
        $path = Join-Path $PackageRoot ([string]$entry.path).Replace('/', '\')
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Package file missing: $($entry.path)" }
        $item = Get-Item -LiteralPath $path
        if ($item.Length -ne [long]$entry.size_bytes) { throw "Package size mismatch: $($entry.path)" }
        $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($hash -ne ([string]$entry.sha256).ToLowerInvariant()) { throw "Package SHA-256 mismatch: $($entry.path)" }
    }
}

function Assert-Installers {
    param([object]$RuntimeInputObject, [object]$Frozen)
    if ($Lane -eq 'core_only') { throw 'core_only must not verify an installer artifact.' }
    $format = if ($Lane -eq 'msi_smoke') { 'msi' } else { 'nsis' }
    $selected = @($Frozen.installers | Where-Object { $_.format -eq $format })
    # 两条 lane 必须能独立失败；当前安装器损坏时拒绝执行，但绝不要求另一格式同时存在。
    if ($selected.Count -ne 1) { throw "Frozen manifest must contain exactly one $format installer." }
    $checks = @()
    foreach ($expected in $selected) {
        $path = Join-Path ([string]$RuntimeInputObject.installer_directory) ([string]$expected.filename)
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Frozen installer missing: $($expected.filename)" }
        $item = Get-Item -LiteralPath $path
        if ($item.Length -ne [long]$expected.size_bytes) { throw "Frozen installer size mismatch: $($expected.filename)" }
        $hash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($hash -ne ([string]$expected.sha256).ToLowerInvariant()) { throw "Frozen installer SHA-256 mismatch: $($expected.filename)" }
        $checks += [ordered]@{ path = $path; size_bytes = [long]$item.Length; sha256 = $hash }
    }
    return ,$checks
}

function Write-RunFailure {
    param([object]$BackupManifest, [string]$Message)
    if ($null -eq $BackupManifest -or [string]::IsNullOrWhiteSpace([string]$BackupManifest.campaign_id)) { return }
    $observedAtUtc = [DateTime]::UtcNow.ToString('o')
    $record = [ordered]@{
        schema_version = 2
        kind = 'superdev.windows-validation.pre-driver-failure'
        campaign_id = [string]$BackupManifest.campaign_id
        lane = $Lane
        stage = $script:runStage
        execution_facts = [ordered]@{
            attempted = $true
            succeeded = $false
            failure = $Message
            started_at_utc = $script:runStartedAtUtc
            finished_at_utc = $observedAtUtc
        }
        artifact_verification = $script:artifactVerification
        installer_checks = $script:installerChecks
        error = $Message
        observed_at_utc = $observedAtUtc
    }
    $failurePath = Join-Path $PreparedBackupDirectory 'run-failure.json'
    Write-ValidationEvent 'info' 'pre_driver_failure_record' 'started' @{ campaign_id = $BackupManifest.campaign_id; path = $failurePath }
    $json = $record | ConvertTo-Json -Depth 8
    [System.IO.File]::WriteAllText($failurePath, $json, [System.Text.UTF8Encoding]::new($false))
    Write-ValidationEvent 'info' 'pre_driver_failure_record' 'succeeded' @{ campaign_id = $BackupManifest.campaign_id; path = $failurePath }
}

$backupManifest = $null
$runStartedAtUtc = [DateTime]::UtcNow.ToString('o')
$runStage = 'entry'
$installerChecks = @()
$artifactVerification = [ordered]@{ attempted = $false; succeeded = $false; not_run_reason = 'installer artifact gate was not reached' }

try {
    Write-ValidationEvent 'info' 'entry' 'started'
    $runStage = 'platform_gate'
    Assert-WindowsX64
    Write-ValidationEvent 'info' 'platform_gate' 'succeeded' @{ windows_build = $script:platformFacts.current_build; windows_display_version = $script:platformFacts.display_version; windows_ubr = $script:platformFacts.ubr; installed_kb_count = @($script:platformFacts.installed_kbs).Count; support_scope = $script:platformFacts.support_scope; esu_evidence_status = $script:platformFacts.esu_evidence_status }
    $packageRoot = $PSScriptRoot
    # prepared backup 是安装前唯一稳定身份，必须先读取；后续任何包/input/installer 失败才能归入同一 campaign 并安全恢复。
    $runStage = 'prepared_backup_identity'
    $backupManifestPath = Join-Path $PreparedBackupDirectory 'backup-manifest.json'
    if (-not (Test-Path -LiteralPath $backupManifestPath -PathType Leaf)) { throw 'Prepared backup manifest is missing. Run Prepare-Validation.ps1 before installation.' }
    $backupManifest = Get-Content -LiteralPath $backupManifestPath -Raw | ConvertFrom-Json
    if ($backupManifest.kind -ne 'superdev.windows-validation.prepared-backup' -or $backupManifest.status -ne 'ready' -or $backupManifest.lane -ne $Lane -or ([string]$backupManifest.campaign_id -notmatch '^w10x64-[0-9a-f]{7}-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$')) {
        throw 'Prepared backup identity, readiness, lane, or campaign ID does not match this run.'
    }
    $script:validationCampaignId = [string]$backupManifest.campaign_id
    $runStage = 'package_integrity'
    Assert-PackageFiles $packageRoot
    $runStage = 'runtime_input'
    if ([string]::IsNullOrWhiteSpace($RuntimeInput)) {
        $RuntimeInput = Join-Path (Split-Path -Parent $packageRoot) 'runtime-input.json'
    }
    if (-not (Test-Path -LiteralPath $RuntimeInput -PathType Leaf)) {
        throw "Runtime input missing: $RuntimeInput. Copy manifest\runtime-input.example.json outside the immutable package and fill it first."
    }
    $inputObject = Get-Content -LiteralPath $RuntimeInput -Raw | ConvertFrom-Json
    $inputObject | Add-Member -NotePropertyName lane -NotePropertyValue $Lane -Force
    $inputObject | Add-Member -NotePropertyName campaign_id -NotePropertyValue ([string]$backupManifest.campaign_id) -Force
    $frozen = Get-Content -LiteralPath (Join-Path $packageRoot 'manifest\frozen-build.json') -Raw | ConvertFrom-Json
	$runStage = 'installer_artifact_verification'
	if ($Lane -eq 'core_only') {
		# core_only 是无 installer 的定向诊断 lane；这里保留真实 NOT_RUN，不能借 NSIS
		# 文件身份把 installer section 或 pre-driver failure 提升成已尝试。
		$installerChecks = @()
		$artifactVerification = [ordered]@{ attempted = $false; succeeded = $false; not_run_reason = 'core_only excludes installer artifact' }
	} else {
		$artifactStartedAtUtc = [DateTime]::UtcNow.ToString('o')
		try {
			$installerChecks = @(Assert-Installers $inputObject $frozen)
			$artifactVerification = [ordered]@{
				attempted = $true
				succeeded = $true
				started_at_utc = $artifactStartedAtUtc
				finished_at_utc = [DateTime]::UtcNow.ToString('o')
			}
		} catch {
			$artifactVerification = [ordered]@{
				attempted = $true
				succeeded = $false
				failure = $_.Exception.Message
				started_at_utc = $artifactStartedAtUtc
				finished_at_utc = [DateTime]::UtcNow.ToString('o')
			}
			throw
		}
	}
    Write-ValidationEvent 'info' 'prepared_backup' 'verified' @{ backup_directory = $PreparedBackupDirectory; campaign_id = $backupManifest.campaign_id }

    $runStage = 'driver_input'
    $derivedInput = Join-Path ([System.IO.Path]::GetTempPath()) "superdev-validation-runtime-$PID.json"
    $derivedJson = $inputObject | ConvertTo-Json -Depth 20
    [System.IO.File]::WriteAllText($derivedInput, $derivedJson, [System.Text.UTF8Encoding]::new($false))
    try {
        $debugCredentialSecure = $null
        $debugCredentialBstr = [IntPtr]::Zero
        $debugCredentialPlain = $null
        try {
            # 每次运行先清除父进程可能遗留的同名变量；MSI lane 不读取也不传递凭据。
            [Environment]::SetEnvironmentVariable($debugCredentialEnvironmentName, $null, 'Process')
            if ($Lane -ne 'msi_smoke') {
                $runStage = 'debug_credential_input'
                $debugCredentialSecure = Read-Host -Prompt 'Enter one-time Windows validation debug credential' -AsSecureString
                if ($null -eq $debugCredentialSecure -or $debugCredentialSecure.Length -eq 0) {
                    throw 'A one-time debug credential is required for functional validation.'
                }
                $debugCredentialBstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($debugCredentialSecure)
                $debugCredentialPlain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($debugCredentialBstr)
                if ([string]::IsNullOrWhiteSpace($debugCredentialPlain)) {
                    throw 'A non-empty one-time debug credential is required for functional validation.'
                }
                # 专用环境变量只在 driver 子进程继承期间存在；secret 不进入命令行或派生 runtime input。
                [Environment]::SetEnvironmentVariable($debugCredentialEnvironmentName, $debugCredentialPlain, 'Process')
            }

            $env:SUPERDEV_AGENT_DATA_DIR = Join-Path $HOME '.superdev'
            $driver = Join-Path $packageRoot 'bin\superdev-windows-validation.exe'
            $runStage = 'driver'
            Write-ValidationEvent 'info' 'driver' 'started' @{ driver = 'bin/superdev-windows-validation.exe' }
			& $driver --package-root $packageRoot --input $derivedInput --prepared-backup $PreparedBackupDirectory
            $driverExit = $LASTEXITCODE
            if ($driverExit -ne 0) { throw "Validation driver exited with code $driverExit. Preserve results and run cleanup." }
            Write-ValidationEvent 'info' 'driver' 'succeeded' @{ backup_directory = $PreparedBackupDirectory }
        } finally {
            [Environment]::SetEnvironmentVariable($debugCredentialEnvironmentName, $null, 'Process')
            if ($debugCredentialBstr -ne [IntPtr]::Zero) {
                [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($debugCredentialBstr)
            }
            if ($null -ne $debugCredentialSecure) {
                $debugCredentialSecure.Dispose()
            }
            $debugCredentialPlain = $null
        }
    } finally {
        Remove-Item -LiteralPath $derivedInput -Force -ErrorAction SilentlyContinue
    }
    Write-ValidationEvent 'info' 'entry' 'succeeded' @{ backup_directory = $PreparedBackupDirectory }
    exit 0
} catch {
    $failureMessage = $_.Exception.Message
    try {
        Write-RunFailure $backupManifest $failureMessage
    } catch {
        Write-ValidationEvent 'error' 'pre_driver_failure_record' 'failed' @{ campaign_id = if ($null -eq $backupManifest) { '' } else { [string]$backupManifest.campaign_id }; path = (Join-Path $PreparedBackupDirectory 'run-failure.json'); cause = $_.Exception.Message }
    }
    Write-ValidationEvent 'error' 'entry' 'failed' @{ error = $failureMessage }
    exit 1
}
