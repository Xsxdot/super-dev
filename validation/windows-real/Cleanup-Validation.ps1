# Cleanup-Validation.ps1 removes one explicitly identified local campaign and restores its prepared state backup.
#
# Responsibilities:
#   - remove only a validated direct child campaign directory;
#   - require, restore and hash-verify the exact pre-install state backup;
#   - persist a final cleanup record beside the campaign report and prepared backup;
#   - keep results unless the operator explicitly requests their removal.
#
# Boundaries:
#   - this script does not stop SuperDev processes or perform broad wildcard deletion;
#   - remote Linux cleanup must already be evidenced by the remote-pipeline scenario;
#   - restoration is refused while Desktop or Agent processes are running.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^w10x64-[0-9a-f]{7}-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{6}$')]
    [string]$CampaignId,
    [string]$CampaignRoot = 'C:\SuperDevValidation\campaigns',
    [string]$ResultsRoot = 'C:\SuperDevValidation\results',
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$BackupDirectory,
    [switch]$RestoreUserState,
    [switch]$RemoveResults
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Write-CleanupEvent {
    param([string]$Level, [string]$Stage, [string]$Outcome, [hashtable]$Fields = @{})
    $record = [ordered]@{ timestamp = [DateTime]::UtcNow.ToString('o'); level = $Level; component = 'windows-validation-cleanup'; stage = $Stage; outcome = $Outcome; campaign_id = $CampaignId }
    foreach ($key in $Fields.Keys) { $record[$key] = $Fields[$key] }
    # 直接写控制台而不是返回 pipeline 对象，保证 report path 等函数返回值不会被结构化日志污染。
    [Console]::Out.WriteLine(($record | ConvertTo-Json -Compress))
}

function Remove-ExactCampaignChild {
    param([string]$Root, [string]$Identity)
    $resolvedRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    $target = [System.IO.Path]::GetFullPath((Join-Path $resolvedRoot $Identity))
    # 递归删除只允许命中受控根目录的直接子项；规范化后再比对可阻断 ..、绝对路径和身份替换。
    if ((Split-Path -Parent $target).TrimEnd('\') -ne $resolvedRoot) { throw "Refusing non-child cleanup target: $target" }
    if ((Split-Path -Leaf $target) -ne $Identity) { throw "Refusing mismatched cleanup identity: $target" }
    if (Test-Path -LiteralPath $target -PathType Container) {
        Remove-Item -LiteralPath $target -Recurse -Force
    }
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

function Get-OptionalPropertyValue {
    param([object]$Object, [string]$Name)
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) { return '' }
    return [string]$property.Value
}

function Get-MachineFacts {
    param([string]$UserStateRoot)

    $processes = @(Get-Process -Name 'SuperDev', 'superdev-agent', 'superdev-mcp', 'superdev-sample' -ErrorAction SilentlyContinue | ForEach-Object {
        [ordered]@{ name = $_.ProcessName; id = $_.Id }
    } | Sort-Object name, id)
    # 端口探测不可静默降级为空；无法观测时 cleanup 必须 FAIL，而不是把未知状态当作无残留。
    $ports = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { $_.LocalPort -eq 57017 } | ForEach-Object {
        [ordered]@{ address = $_.LocalAddress; port = $_.LocalPort; owning_process_id = $_.OwningProcess }
    } | Sort-Object address, port, owning_process_id)
    $uninstall = @()
    $registryRoots = @(
        [ordered]@{ scope = 'HKCU'; path = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*' },
        [ordered]@{ scope = 'HKLM'; path = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*' },
        [ordered]@{ scope = 'HKLM-WOW6432'; path = 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*' }
    )
    foreach ($registryRoot in $registryRoots) {
        $scope = $registryRoot.scope
        $uninstall += @(Get-ItemProperty -Path $registryRoot.path -ErrorAction SilentlyContinue | Where-Object { (Get-OptionalPropertyValue $_ 'DisplayName') -like '*SuperDev*' } | ForEach-Object {
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
        # 安装目录按文件身份比较，避免残留 sidecar 或被替换二进制在 present=true 时被误判为恢复。
        [ordered]@{ label = $_.label; path = $_.path; present = $present; files = if ($present) { @(Get-StateFiles $_.path) } else { @() } }
    } | Sort-Object label)
    $statePresent = Test-Path -LiteralPath $UserStateRoot -PathType Container
    return [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.machine-baseline'
        captured_at_utc = [DateTime]::UtcNow.ToString('o')
        superdev_processes = $processes
        listening_port_57017 = $ports
        uninstall_entries = @($uninstall | Sort-Object scope, key, display_name, display_version)
        install_paths = $installPaths
        connector_files = @($connectors | Sort-Object name)
        user_state = [ordered]@{ present = $statePresent; files = @(Get-StateFiles $UserStateRoot) }
    }
}

function ConvertTo-ComparableJson {
    param([object]$Value)
    return ($Value | ConvertTo-Json -Depth 20 -Compress)
}

function Compare-StateFiles {
    param([object[]]$Expected, [object[]]$Actual)
    $expectedByPath = @{}
    $actualByPath = @{}
    foreach ($file in @($Expected)) { $expectedByPath[[string]$file.path] = $file }
    foreach ($file in @($Actual)) { $actualByPath[[string]$file.path] = $file }
    $missing = @()
    $extra = @()
    $changed = @()
    foreach ($path in @($expectedByPath.Keys | Sort-Object)) {
        $expectedFile = $expectedByPath[$path]
        if (-not $actualByPath.ContainsKey($path)) {
            $missing += [ordered]@{ path = $path; expected_size_bytes = $expectedFile.size_bytes; expected_sha256 = $expectedFile.sha256 }
            continue
        }
        $actualFile = $actualByPath[$path]
        if ($expectedFile.size_bytes -ne $actualFile.size_bytes -or $expectedFile.sha256 -cne $actualFile.sha256) {
            # 只记录相对路径、大小和摘要，既能定位恢复漂移，又不会把用户文件内容写入验证证据。
            $changed += [ordered]@{
                path = $path
                expected_size_bytes = $expectedFile.size_bytes
                actual_size_bytes = $actualFile.size_bytes
                expected_sha256 = $expectedFile.sha256
                actual_sha256 = $actualFile.sha256
            }
        }
    }
    foreach ($path in @($actualByPath.Keys | Sort-Object)) {
        if (-not $expectedByPath.ContainsKey($path)) {
            $actualFile = $actualByPath[$path]
            $extra += [ordered]@{ path = $path; actual_size_bytes = $actualFile.size_bytes; actual_sha256 = $actualFile.sha256 }
        }
    }
    return [ordered]@{ missing = $missing; extra = $extra; changed = $changed }
}

function Compare-BaselineCategory {
    param([string]$Name, [object]$Expected, [object]$Actual)
    $expectedJson = ConvertTo-ComparableJson $Expected
    $actualJson = ConvertTo-ComparableJson $Actual
    $result = [ordered]@{
        category = $Name
        status = if ($expectedJson -ceq $actualJson) { 'PASS' } else { 'FAIL' }
        expected_sha256 = Get-TextSHA256 $expectedJson
        actual_sha256 = Get-TextSHA256 $actualJson
    }
    if ($Name -eq 'user_state') {
        $result['file_differences'] = Compare-StateFiles -Expected @($Expected.files) -Actual @($Actual.files)
    }
    return $result
}

function Get-RequiredPropertyValue {
    param([object]$Object, [string]$Name)
    if ($Object -is [System.Collections.IDictionary]) {
        if (-not $Object.Contains($Name)) { throw "Object is missing category $Name." }
        return $Object[$Name]
    }
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property) { throw "Object is missing category $Name." }
    return $property.Value
}

function Compare-MachineBaseline {
    param([object]$Expected, [object]$Actual)
    if ($Expected.schema_version -ne 1 -or $Expected.kind -ne 'superdev.windows-validation.machine-baseline') {
        throw 'baseline.json identity is invalid.'
    }
    $categories = @('superdev_processes', 'listening_port_57017', 'install_paths', 'uninstall_entries', 'connector_files', 'user_state')
    $checks = @()
    foreach ($category in $categories) {
        $expectedValue = Get-RequiredPropertyValue $Expected $category
        $actualValue = Get-RequiredPropertyValue $Actual $category
        $checks += Compare-BaselineCategory $category $expectedValue $actualValue
    }
    $failed = @($checks | Where-Object { $_.status -ne 'PASS' })
    return [ordered]@{ status = if ($failed.Count -eq 0) { 'PASS' } else { 'FAIL' }; checks = $checks }
}

function Ensure-CampaignReport {
    param([string]$ResultDirectory, [string]$Backup)
    $reportPath = Join-Path $ResultDirectory 'campaign-report.json'
    if (Test-Path -LiteralPath $reportPath -PathType Leaf) { return }
    $backupManifestPath = Join-Path $Backup 'backup-manifest.json'
    if (-not (Test-Path -LiteralPath $backupManifestPath -PathType Leaf)) { throw 'Cannot synthesize pre-driver report without backup-manifest.json.' }
    $prepared = Get-Content -LiteralPath $backupManifestPath -Raw | ConvertFrom-Json
    if ($prepared.kind -ne 'superdev.windows-validation.prepared-backup' -or $prepared.campaign_id -ne $CampaignId) {
        throw 'Cannot synthesize pre-driver report from a different backup identity.'
    }
    $frozenPath = Join-Path $PSScriptRoot 'manifest\frozen-build.json'
    $frozen = Get-Content -LiteralPath $frozenPath -Raw | ConvertFrom-Json
    $failureReason = 'Validation driver was not reached; inspect Run-Validation preflight events.'
    $runFailurePath = Join-Path $Backup 'run-failure.json'
    if (Test-Path -LiteralPath $runFailurePath -PathType Leaf) {
        $runFailure = Get-Content -LiteralPath $runFailurePath -Raw | ConvertFrom-Json
        if ($runFailure.kind -eq 'superdev.windows-validation.pre-driver-failure' -and $runFailure.campaign_id -eq $CampaignId) {
            $failureReason = [string]$runFailure.error
        }
    }
    $providerRows = @($frozen.source_surface.language_runtime_providers.names | ForEach-Object {
        [ordered]@{ provider = [string]$_; runtime_verdict = 'BLOCKED'; debug_verdict = 'BLOCKED'; reason = $failureReason }
    })
    $toolRows = @()
    $scenarioRows = @()
    foreach ($scenarioPath in @(Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot 'scenarios') -Filter '*.json' -File | Sort-Object Name)) {
        $scenario = Get-Content -LiteralPath $scenarioPath.FullName -Raw | ConvertFrom-Json
        $stepRows = @()
        foreach ($step in @($scenario.steps)) {
            $stepRow = [ordered]@{ step_id = [string]$step.id; tool = [string]$step.tool; coverage = [string]$step.coverage; verdict = 'BLOCKED'; error = $failureReason }
            $stepRows += $stepRow
            if ($step.coverage -eq 'primary') {
                $toolRows += [ordered]@{ tool = [string]$step.tool; scenario_id = [string]$scenario.id; step_id = [string]$step.id; verdict = 'BLOCKED'; error = $failureReason }
            }
        }
        $scenarioRows += [ordered]@{ id = [string]$scenario.id; title = [string]$scenario.title; verdict = 'BLOCKED'; steps = $stepRows; cleanup = @() }
    }
    # driver 前也必须保留冻结的 75 行唯一归属；不调用工具，但每行明确 BLOCKED，而不是空数组或伪 PASS。
    if ($providerRows.Count -ne 7 -or $toolRows.Count -ne 75 -or @($toolRows | Group-Object tool | Where-Object { $_.Count -ne 1 }).Count -ne 0) {
        throw 'Cannot synthesize pre-driver report because the frozen 75-tool assignment has drifted.'
    }
    # driver 前失败也必须形成正常 FAIL 报告；provider/tool 只记录未执行 BLOCKED，绝不伪造 Windows 结论。
    $report = [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.campaign-report'
        campaign_id = $CampaignId
        status = 'FAIL'
        functional_status = 'FAIL'
        failure_stage = 'pre_driver_preflight'
        failure_reason = $failureReason
        build_commit = [string]$frozen.build.git_commit
        product_version = [string]$frozen.build.product_version
        target = 'Windows 10 x64'
        lane = [string]$prepared.lane
        runtime_attestation = [ordered]@{ verdict = 'FAIL' }
        installer_checks = @()
        scenarios = $scenarioRows
        providers = $providerRows
        tool_rows = $toolRows
        sections = [ordered]@{}
        cleanup = [ordered]@{ status = 'PENDING'; reason = $failureReason }
        known_anomalies = @($frozen.known_baseline_exceptions)
        started_at_utc = [string]$prepared.created_at_utc
        finished_at_utc = [DateTime]::UtcNow.ToString('o')
    }
    New-Item -ItemType Directory -Force -Path $ResultDirectory | Out-Null
    $json = $report | ConvertTo-Json -Depth 40
    [System.IO.File]::WriteAllText($reportPath, $json, [System.Text.UTF8Encoding]::new($false))
    Write-CleanupEvent 'info' 'pre_driver_report' 'created' @{ campaign_report = $reportPath }
}

function Write-CleanupReport {
    param([string]$ResultDirectory, [string]$Backup, [object]$Report)
    Write-CleanupEvent 'info' 'cleanup_report' 'write_started' @{ status = $Report.status }
    $json = $Report | ConvertTo-Json -Depth 10
    if (Test-Path -LiteralPath $Backup -PathType Container) {
        $backupReportPath = Join-Path $Backup "cleanup-$CampaignId.json"
        [System.IO.File]::WriteAllText($backupReportPath, $json, [System.Text.UTF8Encoding]::new($false))
    }
    if (-not (Test-Path -LiteralPath $ResultDirectory -PathType Container)) { throw 'Selected campaign result directory is unavailable for cleanup finalization.' }
    $resultReportPath = Join-Path $ResultDirectory 'cleanup-report.json'
    [System.IO.File]::WriteAllText($resultReportPath, $json, [System.Text.UTF8Encoding]::new($false))
    # Go finalizer 只接受 campaign 直属 cleanup-report，禁止用 backup 任意路径改写其他 campaign。
    Write-CleanupEvent 'info' 'cleanup_report' 'write_succeeded' @{ status = $Report.status; cleanup_report = $resultReportPath }
    return $resultReportPath
}

function Invoke-CleanupFinalizer {
    param([string]$ReportPath)
    $driver = Join-Path $PSScriptRoot 'bin\superdev-windows-validation.exe'
    if (-not (Test-Path -LiteralPath $driver -PathType Leaf)) { throw "Packaged cleanup finalizer is missing: $driver" }
    Write-CleanupEvent 'info' 'cleanup_finalizer' 'started' @{ cleanup_report = $ReportPath }
    & $driver --finalize-cleanup --results-root $ResultsRoot --campaign-id $CampaignId --cleanup-report $ReportPath
    $finalizerExit = $LASTEXITCODE
    if ($finalizerExit -ne 0) { throw "Cleanup finalizer exited with code $finalizerExit." }
    Write-CleanupEvent 'info' 'cleanup_finalizer' 'succeeded' @{ cleanup_report = $ReportPath }
}

$resultDirectory = Join-Path ([System.IO.Path]::GetFullPath($ResultsRoot).TrimEnd('\')) $CampaignId
$baselineComparison = $null
$quarantine = ''

try {
    Write-CleanupEvent 'info' 'cleanup' 'started'
    if (-not $RestoreUserState) { throw '-RestoreUserState is required; cleanup cannot PASS without restoring and comparing the prepared baseline.' }
    $manifest = Join-Path $BackupDirectory 'backup-manifest.json'
    $baselinePath = Join-Path $BackupDirectory 'baseline.json'
    $stateFilesPath = Join-Path $BackupDirectory 'state-files.json'
    if (-not (Test-Path -LiteralPath $manifest -PathType Leaf)) { throw 'Selected backup has no backup-manifest.json.' }
    if (-not (Test-Path -LiteralPath $baselinePath -PathType Leaf)) { throw 'Selected backup has no baseline.json.' }
    if (-not (Test-Path -LiteralPath $stateFilesPath -PathType Leaf)) { throw 'Selected backup has no state-files.json.' }
    $backupManifest = Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json
    if ($backupManifest.kind -ne 'superdev.windows-validation.prepared-backup' -or $backupManifest.status -ne 'ready' -or $backupManifest.campaign_id -ne $CampaignId) {
        throw 'Selected backup was not completed for this exact campaign by Prepare-Validation.ps1.'
    }
    $baseline = Get-Content -LiteralPath $baselinePath -Raw | ConvertFrom-Json
    if ($baseline.schema_version -ne 1 -or $baseline.kind -ne 'superdev.windows-validation.machine-baseline') {
        throw 'Selected baseline.json identity is invalid.'
    }
    $target = Join-Path $HOME '.superdev'
    if ([System.IO.Path]::GetFullPath([string]$backupManifest.source) -ne [System.IO.Path]::GetFullPath($target)) {
        throw 'Prepared backup belongs to a different user-state path.'
    }
    $stateFilesMetadata = @(Get-Content -LiteralPath $stateFilesPath -Raw | ConvertFrom-Json)
    if ((ConvertTo-ComparableJson $stateFilesMetadata) -cne (ConvertTo-ComparableJson @($baseline.user_state.files))) {
        throw 'state-files.json does not match baseline.json user_state metadata.'
    }
    Write-CleanupEvent 'info' 'prepared_baseline' 'verified' @{ backup_directory = $BackupDirectory; lane = $backupManifest.lane }

    $running = @(Get-Process -Name 'SuperDev', 'superdev-agent', 'superdev-mcp', 'superdev-sample' -ErrorAction SilentlyContinue)
    if ($running.Count -ne 0) { throw 'Close SuperDev Desktop and stop all SuperDev sidecars before cleanup/restoration.' }
    Write-CleanupEvent 'info' 'campaign_workspace' 'started'
    Remove-ExactCampaignChild $CampaignRoot $CampaignId
    Write-CleanupEvent 'info' 'campaign_workspace' 'succeeded'

    if (Test-Path -LiteralPath $target) {
        # 当前 .superdev 属于本次隔离 lane；先 quarantine 可在恢复失败时保留现场，只有全量基线通过后才精确删除。
        $quarantine = "$target.validation-quarantine-$([DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ'))"
        Write-CleanupEvent 'info' 'current_state_quarantine' 'started' @{ quarantine = $quarantine }
        Move-Item -LiteralPath $target -Destination $quarantine
        Write-CleanupEvent 'info' 'current_state_quarantine' 'succeeded' @{ quarantine = $quarantine }
    }
    $backupState = Join-Path $BackupDirectory '.superdev'
    $noStateMarker = Join-Path $BackupDirectory 'NO_SUPERDEV_STATE'
    Write-CleanupEvent 'info' 'user_state_restore' 'started' @{ expected_present = [bool]$baseline.user_state.present }
    if ([bool]$baseline.user_state.present) {
        if (-not (Test-Path -LiteralPath $backupState -PathType Container) -or (Test-Path -LiteralPath $noStateMarker)) {
            throw 'Prepared baseline expected .superdev, but the backup state is incomplete or contradictory.'
        }
        Copy-Item -LiteralPath $backupState -Destination $target -Recurse
    } else {
        if (-not (Test-Path -LiteralPath $noStateMarker -PathType Leaf) -or (Test-Path -LiteralPath $backupState)) {
            throw 'Prepared baseline expected no .superdev state, but the backup markers are incomplete or contradictory.'
        }
    }
    Write-CleanupEvent 'info' 'user_state_restore' 'succeeded' @{ restored_file_count = @($baseline.user_state.files).Count }

    Write-CleanupEvent 'info' 'baseline_precheck' 'started'
    $currentFacts = Get-MachineFacts $target
    $baselineComparison = Compare-MachineBaseline $baseline $currentFacts
    if ($baselineComparison.status -ne 'PASS') {
        $drift = @($baselineComparison.checks | Where-Object { $_.status -ne 'PASS' } | ForEach-Object { $_.category }) -join ','
        throw "Post-cleanup baseline drift detected: $drift"
    }
    Write-CleanupEvent 'info' 'baseline_precheck' 'succeeded' @{ category_count = @($baselineComparison.checks).Count }

    if (-not [string]::IsNullOrWhiteSpace($quarantine)) {
        Write-CleanupEvent 'info' 'validation_state_quarantine' 'removal_started' @{ quarantine = $quarantine }
        Remove-Item -LiteralPath $quarantine -Recurse -Force
        if (Test-Path -LiteralPath $quarantine) { throw 'Validation-state quarantine still exists after exact removal.' }
        Write-CleanupEvent 'info' 'validation_state_quarantine' 'removed' @{ quarantine = $quarantine }
        $quarantine = ''
    }

    # quarantine 删除也是 cleanup 的状态变化；删除后重新采集，确保报告反映真正的最终机器状态而非中间快照。
    Write-CleanupEvent 'info' 'baseline_comparison' 'started'
    $finalFacts = Get-MachineFacts $target
    $baselineComparison = Compare-MachineBaseline $baseline $finalFacts
    if ($baselineComparison.status -ne 'PASS') {
        $drift = @($baselineComparison.checks | Where-Object { $_.status -ne 'PASS' } | ForEach-Object { $_.category }) -join ','
        throw "Final baseline drift detected: $drift"
    }
    Write-CleanupEvent 'info' 'baseline_comparison' 'succeeded' @{ category_count = @($baselineComparison.checks).Count }

    $cleanupReport = [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.cleanup-report'
        campaign_id = $CampaignId
        status = 'PASS'
        campaign_workspace_removed = $true
        campaign_results_removed = [bool]$RemoveResults
        user_state_restored = $true
        restored_state_file_count = @($baseline.user_state.files).Count
        baseline_comparison = $baselineComparison
        validation_state_quarantine_removed = $true
        finished_at_utc = [DateTime]::UtcNow.ToString('o')
    }
    Ensure-CampaignReport $resultDirectory $BackupDirectory
    $cleanupReportPath = Write-CleanupReport $resultDirectory $BackupDirectory $cleanupReport
    Invoke-CleanupFinalizer $cleanupReportPath
    if ($RemoveResults) {
        # 聚合 JSON/Markdown 必须先由 finalizer 固化；删除失败会进入 FAIL 路径，结果目录仍可用于回写。
        Write-CleanupEvent 'info' 'campaign_results' 'started'
        Remove-ExactCampaignChild $ResultsRoot $CampaignId
        if (Test-Path -LiteralPath $resultDirectory) { throw 'Campaign result directory still exists after exact removal.' }
        Write-CleanupEvent 'info' 'campaign_results' 'succeeded'
    }
    Write-CleanupEvent 'info' 'cleanup' 'succeeded'
    exit 0
} catch {
    $failureMessage = $_.Exception.Message
    $failureReport = [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.cleanup-report'
        campaign_id = $CampaignId
        status = 'FAIL'
        error = $failureMessage
        baseline_comparison = $baselineComparison
        recovery_quarantine = $quarantine
        finished_at_utc = [DateTime]::UtcNow.ToString('o')
    }
    try {
        Ensure-CampaignReport $resultDirectory $BackupDirectory
        $failureReportPath = Write-CleanupReport $resultDirectory $BackupDirectory $failureReport
        Invoke-CleanupFinalizer $failureReportPath
    } catch {
        Write-CleanupEvent 'error' 'cleanup_failure_finalize' 'failed' @{ error = $_.Exception.Message }
    }
    Write-CleanupEvent 'error' 'cleanup' 'failed' @{ error = $failureMessage; recovery_quarantine = $quarantine }
    exit 1
}
