# Prepare-Validation.ps1 captures a pre-install baseline and isolates existing SuperDev user state.
#
# Responsibilities:
#   - refuse preparation while SuperDev processes are running;
#   - record non-secret process, port, install, uninstall, connector-hash, and state-file facts;
#   - collect manifest A from pre-install stable input while deferring fresh Host/governance bindings to manifest B;
#   - move the existing .superdev directory into one lane-specific restore point before installation.
#
# Boundaries:
#   - this script does not install, uninstall, start, or stop SuperDev;
#   - it records hashes rather than connector or user-state contents;
#   - it never deletes or overwrites an existing backup.
# Parameters:
#   - Lane selects the independent msi_smoke, nsis_core, or diagnostic core_only preparation identity;
#   - BackupRoot is the operator-controlled parent for the new immutable backup;
#   - RuntimeInput supplies package-external, secret-free stable expectations; fresh Host/governance placeholders are allowed only at this A-stage gate.
# Exit behavior:
#   - exits 0 only after baseline, state isolation, and ready manifest persistence;
#   - exits 1 with a structured error event and leaves recovery material intact.
# Notes:
#   - run from a normal Windows PowerShell 5.1 process before launching an installer.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
	[ValidateSet('msi_smoke', 'nsis_core', 'core_only')]
	[string]$Lane,
	[string]$BackupRoot = 'C:\SuperDevValidation\backups',
	[string]$RuntimeInput = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# Windows PowerShell 5.1 默认沿用系统代码页；在第一次结构化事件写出前固定 UTF-8，
# 才能保证重定向日志与后续原生进程管道在不同 Windows 语言环境中保持同一字节合同。
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$script:platformFacts = $null

function Write-PreparationEvent {
    param([string]$Level, [string]$Stage, [string]$Outcome, [hashtable]$Fields = @{})
    $record = [ordered]@{ timestamp = [DateTime]::UtcNow.ToString('o'); level = $Level; component = 'windows-validation-prepare'; stage = $Stage; outcome = $Outcome; lane = $Lane }
    foreach ($key in $Fields.Keys) { $record[$key] = $Fields[$key] }
    # 直接写控制台而不是返回 pipeline 对象，避免调用方接收函数返回值时混入日志文本。
    [Console]::Out.WriteLine(($record | ConvertTo-Json -Compress))
}

function Assert-WindowsX64 {
    if ($env:OS -ne 'Windows_NT') { throw 'Preparation must run on Windows.' }
    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -ne 'X64') { throw 'Windows x64 is required.' }
    $windows = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop
    if ($null -eq $windows.PSObject.Properties['DisplayVersion'] -or $null -eq $windows.PSObject.Properties['UBR']) { throw 'Windows platform servicing identity is incomplete.' }
    $build = [string]$windows.CurrentBuildNumber
    $displayVersion = [string]$windows.DisplayVersion
    $ubr = [string]$windows.UBR
    $productName = ([string]$windows.ProductName).Trim()
    $installationType = [string]$windows.InstallationType
    $installedKBs = @(Get-HotFix -ErrorAction Stop | ForEach-Object { [string]$_.HotFixID } | Where-Object { $_ -match '^KB[0-9]+$' } | ForEach-Object { $_.ToUpperInvariant() } | Sort-Object -Unique)
    $isWindows10Product = $productName -eq 'Windows 10' -or $productName.StartsWith('Windows 10 ', [StringComparison]::Ordinal)
    if (-not $isWindows10Product -or $installationType -ne 'Client' -or $displayVersion -ne '22H2' -or $build -ne '19045' -or $ubr -notmatch '^[1-9][0-9]*$' -or $installedKBs.Count -eq 0) {
        throw "Windows 10 Client 22H2 x64 build 19045 with servicing evidence is required; observed $productName $displayVersion build $build.$ubr."
    }
    # 已安装 KB 只归档 servicing 事实；本地脚本没有权威 ESU entitlement 证据。
    $script:platformFacts = [ordered]@{
        product_name = $productName
        installation_type = $installationType
        current_build = $build
        display_version = $displayVersion
        ubr = $ubr
        installed_kbs = @($installedKBs)
        architecture = 'amd64'
        support_scope = 'compatibility_only'
        esu_evidence_status = 'not_mechanically_verified'
    }
}

function New-CampaignId {
    $frozenPath = Join-Path $PSScriptRoot 'manifest\frozen-build.json'
    if (-not (Test-Path -LiteralPath $frozenPath -PathType Leaf)) { throw 'Frozen build manifest is missing.' }
    $frozen = Get-Content -LiteralPath $frozenPath -Raw | ConvertFrom-Json
    $commit = [string]$frozen.build.git_commit
    if ($commit -notmatch '^[0-9a-f]{40}$') { throw 'Frozen build commit is invalid.' }
    $random = New-Object byte[] 3
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($random) } finally { $rng.Dispose() }
    $suffix = -join ($random | ForEach-Object { $_.ToString('x2') })
    return "w10x64-$($commit.Substring(0, 7))-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ'))-$suffix"
}

function Get-StateFiles {
    param([string]$Root)
    if (-not (Test-Path -LiteralPath $Root -PathType Container)) { return @() }
    return @(Get-ChildItem -LiteralPath $Root -Recurse -File | ForEach-Object {
        $relative = $_.FullName.Substring($Root.TrimEnd('\').Length).TrimStart('\').Replace('\', '/')
        [ordered]@{ path = $relative; size_bytes = $_.Length; sha256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant() }
    } | Sort-Object path)
}

function Get-TextSHA256 {
    param([string]$Text)
    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Text)
        return -join ($sha.ComputeHash($bytes) | ForEach-Object { $_.ToString('x2') })
    } finally {
        $sha.Dispose()
    }
}

function ConvertTo-ComparableJson {
    param([object]$Value)
    # -InputObject 保留空数组和单元素数组的 JSON 身份，避免 pipeline 枚举把 [] 变成空文本。
    return (ConvertTo-Json -InputObject $Value -Depth 20 -Compress)
}

function Get-RequiredPropertyValue {
    param([object]$Object, [string]$Name)
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { throw "Object is missing category $Name." }
    return $property.Value
}

function Get-BaselineCategoryHashes {
    param([object]$Baseline)
    $hashes = [ordered]@{}
    foreach ($category in @('superdev_processes', 'listening_port_57017', 'install_paths', 'uninstall_entries', 'connector_files', 'user_state')) {
        $hashes[$category] = Get-TextSHA256 (ConvertTo-ComparableJson (Get-RequiredPropertyValue $Baseline $category))
    }
    return $hashes
}

function Get-OptionalPropertyValue {
    param([object]$Object, [string]$Name)
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) { return '' }
    return [string]$property.Value
}

function Get-MachineFacts {
    param([string]$UserStateRoot)

    # 基线必须显式记录“没有 SuperDev 进程”，否则 cleanup 无法区分真正恢复与进程仍占用文件的假成功。
    $processes = @(Get-Process -ErrorAction Stop | Where-Object {
        $_.ProcessName -ieq 'SuperDev' -or $_.ProcessName -like 'superdev-agent*' -or
        $_.ProcessName -like 'superdev-mcp*' -or $_.ProcessName -like 'superdev-sample*'
    } | ForEach-Object {
        [ordered]@{ name = $_.ProcessName; id = $_.Id }
    } | Sort-Object name, id)
    # 端口探测失败不能降级成空列表；空列表会把无法观测误写为干净基线。
    $ports = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { $_.LocalPort -eq 57017 } | ForEach-Object {
        [ordered]@{ address = $_.LocalAddress; port = $_.LocalPort; owning_process_id = $_.OwningProcess }
    } | Sort-Object address, port, owning_process_id)
    $uninstall = @()
    $registryRoots = @(
        [ordered]@{ scope = 'HKCU'; path = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall' },
        [ordered]@{ scope = 'HKLM'; path = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall' },
        [ordered]@{ scope = 'HKLM-WOW6432'; path = 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall' }
    )
    foreach ($registryRoot in $registryRoots) {
        if (-not (Test-Path -LiteralPath $registryRoot.path)) { continue }
        $scope = $registryRoot.scope
        $uninstall += @(Get-ChildItem -LiteralPath $registryRoot.path -ErrorAction Stop | ForEach-Object {
            Get-ItemProperty -LiteralPath $_.PSPath -ErrorAction Stop
        } | Where-Object { (Get-OptionalPropertyValue $_ 'DisplayName') -like '*SuperDev*' } | ForEach-Object {
            $uninstallString = Get-OptionalPropertyValue $_ 'UninstallString'
            [ordered]@{
                scope = $scope
                key = $_.PSChildName
                display_name = (Get-OptionalPropertyValue $_ 'DisplayName')
                display_version = (Get-OptionalPropertyValue $_ 'DisplayVersion')
                install_location = (Get-OptionalPropertyValue $_ 'InstallLocation')
                uninstall_string_sha256 = (Get-TextSHA256 $uninstallString)
            }
        })
    }
    $connectors = @()
    foreach ($candidate in @(
        [ordered]@{ name = 'codex-config'; path = (Join-Path $HOME '.codex\config.toml') },
        [ordered]@{ name = 'claude-config'; path = (Join-Path $HOME '.claude.json') }
    )) {
        $present = Test-Path -LiteralPath $candidate.path -PathType Leaf
        $connectors += [ordered]@{
            name = $candidate.name
            present = $present
            size_bytes = if ($present) { (Get-Item -LiteralPath $candidate.path).Length } else { 0 }
            sha256 = if ($present) { (Get-FileHash -LiteralPath $candidate.path -Algorithm SHA256).Hash.ToLowerInvariant() } else { '' }
        }
    }
    $installCandidates = @(
        [ordered]@{ label = 'local-app-data'; path = (Join-Path $env:LOCALAPPDATA 'SuperDev') },
        [ordered]@{ label = 'local-app-data-programs'; path = (Join-Path $env:LOCALAPPDATA 'Programs\SuperDev') },
        [ordered]@{ label = 'program-files'; path = (Join-Path $env:ProgramFiles 'SuperDev') }
    )
    if (-not [string]::IsNullOrWhiteSpace(${env:ProgramFiles(x86)})) {
        $installCandidates += [ordered]@{ label = 'program-files-x86'; path = (Join-Path ${env:ProgramFiles(x86)} 'SuperDev') }
    }
    $installPaths = @($installCandidates | ForEach-Object {
        $present = Test-Path -LiteralPath $_.path -PathType Container
        # 安装目录只比 present 会漏掉“目录仍在但二进制已漂移”；逐文件身份才能覆盖预装与卸载残留。
        [ordered]@{ label = $_.label; path = $_.path; present = $present; files = if ($present) { @(Get-StateFiles $_.path) } else { @() } }
    } | Sort-Object label)
    $statePresent = Test-Path -LiteralPath $UserStateRoot -PathType Container
    return [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.machine-baseline'
        captured_at_utc = [DateTime]::UtcNow.ToString('o')
        windows_platform = $script:platformFacts
        superdev_processes = $processes
        listening_port_57017 = $ports
        uninstall_entries = @($uninstall | Sort-Object scope, key, display_name, display_version)
        install_paths = $installPaths
        connector_files = @($connectors | Sort-Object name)
        user_state = [ordered]@{ present = $statePresent; files = @(Get-StateFiles $UserStateRoot) }
    }
}

$destination = ''

try {
    Write-PreparationEvent 'info' 'prepare' 'started'
    Assert-WindowsX64
    Write-PreparationEvent 'info' 'platform_gate' 'succeeded' @{ windows_build = $script:platformFacts.current_build; windows_display_version = $script:platformFacts.display_version; windows_ubr = $script:platformFacts.ubr; installed_kb_count = @($script:platformFacts.installed_kbs).Count; support_scope = $script:platformFacts.support_scope; esu_evidence_status = $script:platformFacts.esu_evidence_status }

    $stamp = [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ')
    $destination = Join-Path $BackupRoot "$stamp-$Lane"
    if (Test-Path -LiteralPath $destination) { throw "Backup destination already exists: $destination" }
    New-Item -ItemType Directory -Force -Path $destination | Out-Null

    $source = Join-Path $HOME '.superdev'
    # campaign ID 必须在安装与 driver 之前冻结；即使包或安装器门禁失败，Cleanup 仍有稳定身份可恢复并记录 FAIL。
    $campaignId = New-CampaignId
    Write-PreparationEvent 'info' 'baseline_capture' 'started'
    $baseline = Get-MachineFacts $source
    if (@($baseline.superdev_processes).Count -ne 0) { throw 'Close SuperDev Desktop and stop all SuperDev sidecars before pre-install preparation.' }
    if (@($baseline.listening_port_57017).Count -ne 0) { throw 'Port 57017 must have no listener before installer lifecycle preparation.' }
    if (@($baseline.uninstall_entries).Count -ne 0) { throw 'Existing SuperDev uninstall registrations must be removed before installer lifecycle preparation.' }
    if (@($baseline.install_paths | Where-Object { [bool]$_.present }).Count -ne 0) { throw 'Existing SuperDev install directories must be removed before installer lifecycle preparation.' }
    # 先把完整基线持久化，再移动用户状态；中途失败时操作者仍有可机械核对的恢复依据。
    $baselineJson = $baseline | ConvertTo-Json -Depth 12
    $baselinePath = Join-Path $destination 'baseline.json'
    [System.IO.File]::WriteAllText($baselinePath, $baselineJson, [System.Text.UTF8Encoding]::new($false))
    # manifest 同时锁定整份基线和六类比较输入，使 cleanup 报告不能自行声明另一套 expected hash。
    $persistedBaseline = Get-Content -LiteralPath $baselinePath -Raw | ConvertFrom-Json
    $baselineSha256 = (Get-FileHash -LiteralPath $baselinePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $baselineCategorySha256 = Get-BaselineCategoryHashes $persistedBaseline
    Write-PreparationEvent 'info' 'baseline_capture' 'succeeded' @{ process_count = @($baseline.superdev_processes).Count; state_file_count = @($baseline.user_state.files).Count }

    $preparingManifest = [ordered]@{ schema_version = 1; kind = 'superdev.windows-validation.prepared-backup'; status = 'preparing'; created_at_utc = [DateTime]::UtcNow.ToString('o'); source = $source; lane = $Lane; campaign_id = $campaignId; baseline_sha256 = $baselineSha256; baseline_category_sha256 = $baselineCategorySha256 } | ConvertTo-Json -Depth 5
    [System.IO.File]::WriteAllText((Join-Path $destination 'backup-manifest.json'), $preparingManifest, [System.Text.UTF8Encoding]::new($false))
	$stateFiles = @($baseline.user_state.files)
	$stateFilesJson = ConvertTo-Json -InputObject @($stateFiles) -Depth 8
	[System.IO.File]::WriteAllText((Join-Path $destination 'state-files.json'), $stateFilesJson, [System.Text.UTF8Encoding]::new($false))

	# A 只校验并摘要安装前稳定字段；fresh Host ID 与 governance 由产品 bootstrap 后的 B 完整校验。
	# 门禁必须在 Move-Item 之前完成；失败只留下 preparing backup 和只读证据，
	# 原用户状态仍位于原处，且 installer lifecycle 没有机会开始。
	$preinstallInput = $RuntimeInput
	if ([string]::IsNullOrWhiteSpace($preinstallInput)) {
		$preinstallInput = Join-Path (Split-Path -Parent $PSScriptRoot) 'runtime-input.json'
	}
	if (-not (Test-Path -LiteralPath $preinstallInput -PathType Leaf)) {
		throw "Runtime input missing: $preinstallInput. Copy manifest\runtime-input.example.json outside the immutable package and fill it first."
	}
	$driver = Join-Path $PSScriptRoot 'bin\superdev-windows-validation.exe'
	if (-not (Test-Path -LiteralPath $driver -PathType Leaf)) { throw 'Windows validation driver is missing.' }
	Write-PreparationEvent 'info' 'environment_preinstall' 'started' @{ campaign_id = $campaignId }
	& $driver --collect-environment-preinstall --package-root $PSScriptRoot --input $preinstallInput --prepared-backup $destination --campaign-id $campaignId --preinstall-lane $Lane
	$preinstallExit = $LASTEXITCODE
	if ($preinstallExit -ne 0) { throw "Pre-install environment gate exited with code $preinstallExit; user state was not moved." }
	Write-PreparationEvent 'info' 'environment_preinstall' 'succeeded' @{ campaign_id = $campaignId }

	Write-PreparationEvent 'info' 'user_state_isolation' 'started' @{ state_present = [bool]$baseline.user_state.present }
    # lane 期间不得在原用户数据上运行；移动而非复制可同时保证隔离和最终逐文件恢复身份。
    if (Test-Path -LiteralPath $source -PathType Container) {
        Move-Item -LiteralPath $source -Destination (Join-Path $destination '.superdev')
    } else {
        New-Item -ItemType File -Path (Join-Path $destination 'NO_SUPERDEV_STATE') | Out-Null
    }
    Write-PreparationEvent 'info' 'user_state_isolation' 'succeeded' @{ state_file_count = @($stateFiles).Count }

    $manifest = [ordered]@{ schema_version = 1; kind = 'superdev.windows-validation.prepared-backup'; status = 'ready'; created_at_utc = [DateTime]::UtcNow.ToString('o'); source = $source; lane = $Lane; campaign_id = $campaignId; baseline_sha256 = $baselineSha256; baseline_category_sha256 = $baselineCategorySha256; state_file_count = @($stateFiles).Count } | ConvertTo-Json -Depth 5
    [System.IO.File]::WriteAllText((Join-Path $destination 'backup-manifest.json'), $manifest, [System.Text.UTF8Encoding]::new($false))
    Write-PreparationEvent 'info' 'prepare' 'succeeded' @{ backup_directory = $destination; campaign_id = $campaignId; state_file_count = @($stateFiles).Count }
    exit 0
} catch {
    Write-PreparationEvent 'error' 'prepare' 'failed' @{ error = $_.Exception.Message; recovery_directory = $destination }
    exit 1
}
