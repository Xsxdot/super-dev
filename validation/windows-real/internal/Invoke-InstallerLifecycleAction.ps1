# Invoke-InstallerLifecycleAction.ps1 执行 package driver 选择的固定安装器动作。
#
# 职责：
#   - 校验 driver-owned request/action 身份与所有目标文件 size/SHA-256；
#   - 从真实动作前持有 prepared backup 的 OS 排他活动锁直到观察与 result 写入完成；
#   - 对 install/start/stop/uninstall 执行唯一固定命令并观察文件、进程、57017 与卸载注册项；
#   - 把真实 attempted、时间、结果和 observed state 写回 driver 临时 result。
#
# 边界：
#   - 仅由 package-integrity 校验后的 Go driver 调用，不是公开入口；
#   - 不接受任意命令、参数、事实或输出路径；
#   - install/uninstall 子进程可以请求 UAC，helper、start 与 stop 始终保持标准用户权限。
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$RequestPath,
    [Parameter(Mandatory = $true)]
    [ValidateNotNullOrEmpty()]
    [string]$OutputPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$OutputEncoding = [Console]::OutputEncoding
$script:startedAt = ''
$script:request = $null
$script:command = $null
$script:activeLock = $null
$processExitCode = 1

function Write-ResultJson {
    param([string]$Path, [object]$Value)
    $json = ConvertTo-Json -InputObject $Value -Depth 24 -Compress
    [System.IO.File]::WriteAllText([System.IO.Path]::GetFullPath($Path), $json + [Environment]::NewLine, [System.Text.UTF8Encoding]::new($false))
}

function Get-FileIdentity {
    param([string]$Root, [string]$Path)
    $rootPath = [System.IO.Path]::GetFullPath($Root).TrimEnd('\')
    $fullPath = [System.IO.Path]::GetFullPath($Path)
    if (-not $fullPath.StartsWith($rootPath + '\', [System.StringComparison]::OrdinalIgnoreCase) -and
        -not [string]::Equals($fullPath, $rootPath, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw 'identity_outside_root'
    }
    $file = Get-Item -LiteralPath $fullPath -ErrorAction Stop
    if ($file.PSIsContainer) { throw 'identity_not_file' }
    $relative = $file.FullName.Substring($rootPath.Length).TrimStart('\').Replace('\', '/')
    return [ordered]@{
        path = $relative
        size_bytes = [int64]$file.Length
        sha256 = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256 -ErrorAction Stop).Hash.ToLowerInvariant()
    }
}

function Assert-IdentityEqual {
    param([object]$Actual, [object]$Expected)
    if (-not [string]::Equals([string]$Actual.path, [string]$Expected.path, [System.StringComparison]::OrdinalIgnoreCase) -or
        [int64]$Actual.size_bytes -ne [int64]$Expected.size_bytes -or
        -not [string]::Equals([string]$Actual.sha256, [string]$Expected.sha256, [System.StringComparison]::OrdinalIgnoreCase)) {
        throw 'file_identity_mismatch'
    }
}

function Get-TextSha256 {
    param([string]$Value)
    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = $algorithm.ComputeHash([System.Text.UTF8Encoding]::new($false).GetBytes($Value))
        return ([BitConverter]::ToString($hash)).Replace('-', '').ToLowerInvariant()
    } finally {
        $algorithm.Dispose()
    }
}

function Test-ProcessElevated {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-StockMSIExecPath {
    # helper 本身由 System32 的 stock Windows PowerShell 启动；从 PSHOME 反解
    # System32，避免 PATH 中同名 msiexec.exe 改写固定 installer 动作。
    $path = [System.IO.Path]::GetFullPath((Join-Path $PSHOME '..\..\msiexec.exe'))
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw 'msiexec_missing' }
    return $path
}

function Get-OptionalPropertyValue {
    param([object]$Object, [string]$Name)
    $property = $Object.PSObject.Properties[$Name]
    if ($null -eq $property -or $null -eq $property.Value) { return '' }
    return [string]$property.Value
}

function Assert-Windows10ClientX64StandardUser {
    if ($env:OS -ne 'Windows_NT') { throw 'platform' }
    if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -ne 'X64') { throw 'platform' }
    $windows = Get-ItemProperty -LiteralPath 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion' -ErrorAction Stop
    if ($null -eq $windows.PSObject.Properties['DisplayVersion'] -or $null -eq $windows.PSObject.Properties['UBR']) { throw 'platform' }
    $productName = ([string]$windows.ProductName).Trim()
    $ubrValue = 0
    $hasValidUBR = [int]::TryParse([string]$windows.UBR, [ref]$ubrValue) -and $ubrValue -gt 0
    $isWindows10Product = $productName -eq 'Windows 10' -or $productName.StartsWith('Windows 10 ', [StringComparison]::Ordinal)
    if (-not $isWindows10Product -or [string]$windows.InstallationType -ne 'Client' -or
        [string]$windows.DisplayVersion -ne '22H2' -or [string]$windows.CurrentBuildNumber -ne '19045' -or -not $hasValidUBR) { throw 'platform' }
    if ([string]$PSVersionTable.PSEdition -ne 'Desktop' -or [int]$PSVersionTable.PSVersion.Major -ne 5 -or [int]$PSVersionTable.PSVersion.Minor -ne 1) { throw 'powershell_51' }
    if (Test-ProcessElevated) { throw 'elevated_helper' }
}

function Get-UninstallExecutable {
    param([string]$UninstallString)
    if ($UninstallString -match '^\s*"([^"]+)"') { return [string]$Matches[1] }
    if ($UninstallString -match '^\s*([^\s]+)') { return [string]$Matches[1] }
    return ''
}

function Get-SuperDevUninstallEntries {
    $entries = @()
    foreach ($root in @(
        [ordered]@{ scope = 'HKCU'; path = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall' },
        [ordered]@{ scope = 'HKLM'; path = 'HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall' },
        [ordered]@{ scope = 'HKLM32'; path = 'HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall' }
    )) {
        if (-not (Test-Path -LiteralPath $root.path)) { continue }
        foreach ($key in @(Get-ChildItem -LiteralPath $root.path -ErrorAction Stop)) {
            $value = Get-ItemProperty -LiteralPath $key.PSPath -ErrorAction Stop
            $displayName = Get-OptionalPropertyValue $value 'DisplayName'
            if ($displayName -ne 'SuperDev') { continue }
            $uninstallString = Get-OptionalPropertyValue $value 'UninstallString'
            $installLocation = Get-OptionalPropertyValue $value 'InstallLocation'
            if (-not [string]::IsNullOrWhiteSpace($installLocation)) { $installLocation = [System.IO.Path]::GetFullPath($installLocation) }
            $entries += [ordered]@{
                scope = [string]$root.scope
                key = [string]$key.PSChildName
                display_name = $displayName
                display_version = Get-OptionalPropertyValue $value 'DisplayVersion'
                install_location = $installLocation
                uninstall_executable = Get-UninstallExecutable $uninstallString
                uninstall_string_sha256 = Get-TextSha256 $uninstallString
            }
        }
    }
    return @($entries | Sort-Object scope, key)
}

function Get-BoundUninstallEntries {
    param([object[]]$Entries, [object]$Request)
    $versionMatches = @($Entries | Where-Object {
        [string]$_.display_version -eq [string]$Request.product_version
    })
    if ([string]$Request.format -eq 'msi') { return @($versionMatches) }
    $installRoot = [System.IO.Path]::GetFullPath([string]$Request.install_directory)
    return @($versionMatches | Where-Object {
        [string]::Equals([string]$_.install_location, $installRoot, [System.StringComparison]::OrdinalIgnoreCase)
    })
}

function Get-InstalledFiles {
    param([string]$InstallRoot)
    $product = @()
    $desktop = Join-Path $InstallRoot 'SuperDev.exe'
    if (Test-Path -LiteralPath $desktop -PathType Leaf) { $product = @((Get-FileIdentity $InstallRoot $desktop)) }
    $sidecars = @()
    if (Test-Path -LiteralPath $InstallRoot -PathType Container) {
        $sidecars = @(Get-ChildItem -LiteralPath $InstallRoot -Recurse -File -Filter '*.exe' -ErrorAction Stop | Where-Object {
            $_.Name -match '^(?i:superdev-(agent|mcp|sample).*)\.exe$'
        } | ForEach-Object { Get-FileIdentity $InstallRoot $_.FullName } | Sort-Object path)
    }
    return [ordered]@{ product = $product; sidecars = $sidecars }
}

function Test-SidecarFamilies {
    param([object[]]$Files)
    $found = @{ agent = $false; mcp = $false; sample = $false }
    foreach ($file in @($Files)) {
        $name = [System.IO.Path]::GetFileName([string]$file.path).ToLowerInvariant()
        foreach ($family in @('agent', 'mcp', 'sample')) {
            if ($name.StartsWith("superdev-$family") -and $name.EndsWith('.exe')) { $found[$family] = $true }
        }
    }
    return ($found.agent -and $found.mcp -and $found.sample)
}

function Get-BoundProcesses {
    param([string]$InstallRoot, [int[]]$MainDesktopProcessIds = @())
    $rootPath = [System.IO.Path]::GetFullPath($InstallRoot).TrimEnd('\')
    $processes = @()
    foreach ($process in @(Get-CimInstance -ClassName Win32_Process -ErrorAction Stop)) {
        $path = [string]$process.ExecutablePath
        if ([string]::IsNullOrWhiteSpace($path)) { continue }
        $fullPath = [System.IO.Path]::GetFullPath($path)
        if (-not $fullPath.StartsWith($rootPath + '\', [System.StringComparison]::OrdinalIgnoreCase)) { continue }
        $name = [System.IO.Path]::GetFileName($fullPath).ToLowerInvariant()
        $role = 'sidecar'
        if ($name -eq 'superdev.exe') {
            $role = if (@($MainDesktopProcessIds) -contains [int]$process.ProcessId) { 'desktop' } else { 'desktop_child' }
        }
        elseif ($name.StartsWith('superdev-agent') -and $name.EndsWith('.exe')) { $role = 'agent' }
        elseif (-not (($name.StartsWith('superdev-mcp') -or $name.StartsWith('superdev-sample')) -and $name.EndsWith('.exe'))) { continue }
        $processes += [ordered]@{
            role = $role
            process_id = [int]$process.ProcessId
            parent_process_id = [int]$process.ParentProcessId
            executable = Get-FileIdentity $rootPath $fullPath
        }
    }
    return @($processes | Sort-Object process_id)
}

function Get-Port57017 {
    $connections = @(Get-NetTCPConnection -State Listen -ErrorAction Stop | Where-Object { [int]$_.LocalPort -eq 57017 })
    if ($connections.Count -eq 0) { return [ordered]@{ port = 57017; listening = $false } }
    if ($connections.Count -ne 1) { throw 'multiple_agent_listeners' }
    return [ordered]@{ port = 57017; listening = $true; owning_process_id = [int]$connections[0].OwningProcess }
}

function Assert-CleanInstallerState {
    param([object]$Request)
    # Prepare 固化的 baseline 是身份锚点；动作前再观察一次当前状态，防止
    # Prepare 后出现的旧安装/监听/进程被本次 install 误认成动作产物。
    if (Test-Path -LiteralPath ([string]$Request.install_directory)) { throw 'install_root_not_clean' }
    if (@(Get-SuperDevUninstallEntries).Count -ne 0) { throw 'uninstall_state_not_clean' }
    $port = Get-Port57017
    if ([bool]$port.listening) { throw 'agent_listener_not_clean' }
    $processes = @(Get-CimInstance -ClassName Win32_Process -ErrorAction Stop | Where-Object {
        [string]$_.Name -match '^(?i:superdev(?:-(?:agent|mcp|sample).*)?)\.exe$'
    })
    if ($processes.Count -ne 0) { throw 'product_process_state_not_clean' }
}

function Assert-InstalledFileSet {
    param([object]$Request)
    $installRoot = [System.IO.Path]::GetFullPath([string]$Request.install_directory)
    if (-not (Test-Path -LiteralPath $installRoot -PathType Container)) { throw 'bound_install_root_missing' }
    if (@($Request.installed_files).Count -lt 4) { throw 'bound_installed_files_missing' }
    foreach ($expected in @($Request.installed_files)) {
        $relative = ([string]$expected.path).Replace('/', '\')
        $actual = Get-FileIdentity $installRoot (Join-Path $installRoot $relative)
        Assert-IdentityEqual $actual $expected
    }
}

function Assert-NoBoundRuntime {
    param([object]$Request)
    if (@(Get-BoundProcesses ([string]$Request.install_directory)).Count -ne 0) { throw 'bound_product_process_still_running' }
    $port = Get-Port57017
    if ([bool]$port.listening) { throw 'bound_agent_listener_still_running' }
}

function Assert-StartPrecondition {
    param([object]$Request)
    # start 只允许从已验证 install 的静止状态进入；上次 start 若已生效但结果丢失，
    # 进程或监听状态会让重入在任何 Start-Process 前只读拒绝。
    Assert-InstalledFileSet $Request
    Assert-NoBoundRuntime $Request
}

function Assert-CurrentUninstallBinding {
    param([object]$Request, [object[]]$Entries)
    if ($null -eq $Request.uninstall_entry -or @($Entries).Count -ne 1) { throw 'bound_uninstall_registration_missing' }
    $expected = $Request.uninstall_entry
    $actual = $Entries[0]
    foreach ($name in @('scope', 'key', 'display_name', 'display_version', 'install_location', 'uninstall_executable', 'uninstall_string_sha256')) {
        if (-not [string]::Equals([string]$actual.$name, [string]$expected.$name, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw 'bound_uninstall_registration_drift'
        }
    }
    if ([string]$Request.format -eq 'msi') {
        if ([string]$actual.key -notmatch '^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$' -or
            [System.IO.Path]::GetFileName([string]$actual.uninstall_executable) -ine 'msiexec.exe') {
            throw 'bound_msi_uninstall_registration_invalid'
        }
    } else {
        $registered = [System.IO.Path]::GetFullPath([string]$actual.uninstall_executable)
        $expectedPath = [System.IO.Path]::GetFullPath([string]$Request.uninstaller_path)
        if (-not [string]::Equals($registered, $expectedPath, [System.StringComparison]::OrdinalIgnoreCase)) {
            throw 'bound_nsis_uninstall_registration_invalid'
        }
    }
}

function Assert-UninstallPrecondition {
    param([object]$Request, [object[]]$Entries)
    # uninstall 必须仍处于已验证 stop 的静止状态，并保留 install fact 绑定的完整
    # 文件与官方卸载注册；部分卸载或身份漂移不会触发第二次 UAC 副作用。
    Assert-InstalledFileSet $Request
    Assert-NoBoundRuntime $Request
    Assert-CurrentUninstallBinding $Request $Entries
}

function New-Checks {
    param([string]$Action, [hashtable]$Values)
    switch ($Action) {
        'install' { return @(
            [ordered]@{ name = 'installer_exit_success'; matched = [bool]$Values.exit },
            [ordered]@{ name = 'installed_product_present'; matched = [bool]$Values.product },
            [ordered]@{ name = 'installed_sidecars_present'; matched = [bool]$Values.sidecars },
            [ordered]@{ name = 'uninstall_identity_present'; matched = [bool]$Values.uninstall }
        ) }
        'start' { return @(
            [ordered]@{ name = 'desktop_process_started'; matched = [bool]$Values.desktop },
            [ordered]@{ name = 'agent_listener_owned'; matched = [bool]$Values.listener },
            [ordered]@{ name = 'installed_process_identities_match'; matched = [bool]$Values.identities }
        ) }
        'stop' { return @(
            [ordered]@{ name = 'desktop_process_stopped'; matched = [bool]$Values.desktop },
            [ordered]@{ name = 'agent_listener_stopped'; matched = [bool]$Values.listener },
            [ordered]@{ name = 'installed_processes_stopped'; matched = [bool]$Values.processes }
        ) }
        'uninstall' { return @(
            [ordered]@{ name = 'uninstaller_exit_success'; matched = [bool]$Values.exit },
            [ordered]@{ name = 'uninstall_identity_absent'; matched = [bool]$Values.uninstall },
            [ordered]@{ name = 'installed_files_absent'; matched = [bool]$Values.files }
        ) }
    }
    throw 'unsupported_action'
}

function Test-ChecksPass {
    param([object[]]$Checks)
    return (@($Checks | Where-Object { -not [bool]$_.matched }).Count -eq 0)
}

function Assert-RequestFileIdentities {
    param([object]$Request)
    $installerRoot = [System.IO.Path]::GetDirectoryName([System.IO.Path]::GetFullPath([string]$Request.installer_path))
    $actualInstaller = Get-FileIdentity $installerRoot ([string]$Request.installer_path)
    Assert-IdentityEqual $actualInstaller $Request.artifact
    if ([string]$Request.action -eq 'start') {
        $desktop = @($Request.installed_files | Where-Object { [System.IO.Path]::GetFileName([string]$_.path) -ieq 'SuperDev.exe' })
        if ($desktop.Count -ne 1) { throw 'desktop_identity_missing' }
        $actualDesktop = Get-FileIdentity ([string]$Request.install_directory) ([string]$Request.desktop_path)
        Assert-IdentityEqual $actualDesktop $desktop[0]
    }
    if ([string]$Request.action -eq 'uninstall' -and [string]$Request.format -eq 'nsis') {
        $actualUninstaller = Get-FileIdentity ([string]$Request.install_directory) ([string]$Request.uninstaller_path)
        Assert-IdentityEqual $actualUninstaller $Request.uninstaller_identity
    }
}

function New-Result {
    param([bool]$Succeeded, [string]$FailureCode, [object]$Command, [object]$Observation)
    $started = [DateTimeOffset]::Parse($script:startedAt, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::AssumeUniversal)
    $finished = [DateTimeOffset]::UtcNow
    if ($finished -le $started) { $finished = $started.AddTicks(1) }
    return [ordered]@{
        schema_version = 1
        kind = 'superdev.windows-validation.installer-lifecycle-executor-result'
        action = [string]$script:request.action
        attempted = $true
        succeeded = $Succeeded
        started_at_utc = $script:startedAt
        finished_at_utc = $finished.ToString('o')
        failure_code = $FailureCode
        command = $Command
        observation = $Observation
    }
}

function Invoke-Install {
    param([object]$Request)
    $exitCode = -1
    $invocationFailed = $false
    $executable = if ([string]$Request.format -eq 'msi') { 'msiexec.exe' } else { [string]$Request.artifact.path }
    $script:command = [ordered]@{ operation = 'install'; method = 'start_process_wait_elevated'; executable = $executable; target = $Request.artifact; product_code = ''; process_ids = @(); exit_code = $exitCode }
    Assert-RequestFileIdentities $Request
    Assert-CleanInstallerState $Request
    $filePath = [string]$Request.installer_path
    if ([string]$Request.format -eq 'msi') {
        $arguments = '/i "{0}" /qn /norestart INSTALLDIR="{1}"' -f [string]$Request.installer_path, [string]$Request.install_directory
        $filePath = Get-StockMSIExecPath
    } else {
        $arguments = '/S /D={0}' -f [string]$Request.install_directory
    }
    $script:startedAt = [DateTime]::UtcNow.ToString('o')
    try {
        $process = Start-Process -FilePath $filePath -ArgumentList $arguments -Verb RunAs -Wait -PassThru
        $exitCode = [int]$process.ExitCode
    } catch { $invocationFailed = $true }
    $files = Get-InstalledFiles ([string]$Request.install_directory)
    $entries = @(Get-SuperDevUninstallEntries)
    $matchingEntries = @(Get-BoundUninstallEntries -Entries $entries -Request $Request)
    if ([string]$Request.format -eq 'msi' -and $matchingEntries.Count -eq 1 -and [string]$matchingEntries[0].key -match '^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$') {
        $script:command.product_code = [string]$matchingEntries[0].key
    }
    $uninstaller = $null
    if ([string]$Request.format -eq 'nsis') {
        $uninstallerPath = Join-Path ([string]$Request.install_directory) 'uninstall.exe'
        if (Test-Path -LiteralPath $uninstallerPath -PathType Leaf) { $uninstaller = Get-FileIdentity ([string]$Request.install_directory) $uninstallerPath }
    }
    $uninstallIdentityMatched = $false
    if ($matchingEntries.Count -eq 1 -and [string]$Request.format -eq 'msi') {
        # 默认 Tauri WiX 不保证写 ARPINSTALLLOCATION。空值不削弱 ProductCode/msiexec
        # 主身份；一旦存在则必须与本次 hash-verified 安装根完全一致。
        $registeredLocation = [string]$matchingEntries[0].install_location
        $locationMatched = [string]::IsNullOrWhiteSpace($registeredLocation)
        if (-not $locationMatched) {
            $locationMatched = [string]::Equals($registeredLocation, [System.IO.Path]::GetFullPath([string]$Request.install_directory), [System.StringComparison]::OrdinalIgnoreCase)
        }
        $uninstallIdentityMatched = (-not [string]::IsNullOrWhiteSpace([string]$script:command.product_code) -and
            [System.IO.Path]::GetFileName([string]$matchingEntries[0].uninstall_executable) -ieq 'msiexec.exe' -and $locationMatched)
    } elseif ($matchingEntries.Count -eq 1 -and $null -ne $uninstaller) {
        $expectedUninstaller = Join-Path ([string]$Request.install_directory) ([string]$uninstaller.path)
        $uninstallIdentityMatched = [string]::Equals([System.IO.Path]::GetFullPath([string]$matchingEntries[0].uninstall_executable), [System.IO.Path]::GetFullPath($expectedUninstaller), [System.StringComparison]::OrdinalIgnoreCase)
    }
    $checks = New-Checks 'install' @{
        exit = ($exitCode -eq 0)
        product = ($files.product.Count -eq 1)
        sidecars = (Test-SidecarFamilies $files.sidecars)
        uninstall = $uninstallIdentityMatched
    }
    $script:command.exit_code = $exitCode
    $succeeded = Test-ChecksPass $checks
    $failureCode = if ($succeeded) { '' } elseif ($invocationFailed) { 'installer_invocation_failed' } else { 'install_observation_failed' }
    return New-Result $succeeded $failureCode $script:command ([ordered]@{
        checks = $checks
        install_path_present = (Test-Path -LiteralPath ([string]$Request.install_directory) -PathType Container)
        product_files = @($files.product)
        sidecar_files = @($files.sidecars)
        uninstaller_file = $uninstaller
        uninstall_entries = @($matchingEntries)
        processes = @()
        remaining_bound_process_ids = @()
    })
}

function Invoke-Start {
    param([object]$Request)
    $desktopIdentity = @($Request.installed_files | Where-Object { [System.IO.Path]::GetFileName([string]$_.path) -ieq 'SuperDev.exe' })[0]
    $script:command = [ordered]@{ operation = 'start'; method = 'start_process'; executable = 'SuperDev.exe'; target = $desktopIdentity; product_code = ''; process_ids = @(); exit_code = $null }
    Assert-RequestFileIdentities $Request
    Assert-StartPrecondition $Request
    $script:startedAt = [DateTime]::UtcNow.ToString('o')
    $invocationFailed = $false
    try {
        $desktop = Start-Process -FilePath ([string]$Request.desktop_path) -PassThru
        $script:command.process_ids = @([int]$desktop.Id)
    } catch { $invocationFailed = $true }
    $processes = @()
    $port = [ordered]@{ port = 57017; listening = $false }
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        $processes = @(Get-BoundProcesses ([string]$Request.install_directory) @($script:command.process_ids))
        $port = Get-Port57017
        $desktopFound = @($processes | Where-Object { $_.role -eq 'desktop' -and @($script:command.process_ids) -contains [int]$_.process_id }).Count -eq 1
        $ownerFound = $port.listening -and @($processes | Where-Object { $_.role -eq 'agent' -and [int]$_.process_id -eq [int]$port.owning_process_id }).Count -eq 1
        if ($desktopFound -and $ownerFound) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    $checks = New-Checks 'start' @{ desktop = $desktopFound; listener = $ownerFound; identities = ($desktopFound -and $ownerFound) }
    $succeeded = Test-ChecksPass $checks
    $failureCode = if ($succeeded) { '' } elseif ($invocationFailed) { 'desktop_invocation_failed' } else { 'start_observation_failed' }
    return New-Result $succeeded $failureCode $script:command ([ordered]@{ checks = $checks; processes = $processes; port_57017 = $port; remaining_bound_process_ids = @() })
}

function Invoke-Stop {
    param([object]$Request)
    $desktopIdentity = @($Request.installed_files | Where-Object { [System.IO.Path]::GetFileName([string]$_.path) -ieq 'SuperDev.exe' })[0]
    $targetIds = @($Request.start_process_ids | ForEach-Object { [int]$_ } | Sort-Object -Unique)
    if ($targetIds.Count -eq 0) { throw 'stop_target_missing' }
    foreach ($targetId in $targetIds) {
        $target = Get-CimInstance -ClassName Win32_Process -Filter ("ProcessId={0}" -f $targetId) -ErrorAction Stop
        $actual = Get-FileIdentity ([string]$Request.install_directory) ([string]$target.ExecutablePath)
        Assert-IdentityEqual $actual $desktopIdentity
    }
    $script:command = [ordered]@{ operation = 'stop'; method = 'close_main_window'; executable = 'SuperDev.exe'; target = $desktopIdentity; product_code = ''; process_ids = $targetIds; exit_code = $null }
    $script:startedAt = [DateTime]::UtcNow.ToString('o')
    $closeFailed = $false
    foreach ($targetId in $targetIds) {
        try {
            $process = Get-Process -Id $targetId -ErrorAction Stop
            if (-not $process.CloseMainWindow()) { $closeFailed = $true }
        } catch { $closeFailed = $true }
    }
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        $remaining = @(Get-BoundProcesses ([string]$Request.install_directory))
        $port = Get-Port57017
        if ($remaining.Count -eq 0 -and -not $port.listening) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    $remainingIds = @($remaining | ForEach-Object { [int]$_.process_id } | Sort-Object -Unique)
    $targetsStopped = @($targetIds | Where-Object { $remainingIds -contains $_ }).Count -eq 0
    # 仅“后来已退出”不能反推本次 CloseMainWindow 成功发出；命令结果与最终状态必须同时成立。
    $checks = New-Checks 'stop' @{ desktop = ($targetsStopped -and -not $closeFailed); listener = (-not $port.listening); processes = ($remainingIds.Count -eq 0) }
    $succeeded = Test-ChecksPass $checks
    $failureCode = if ($succeeded) { '' } elseif ($closeFailed) { 'desktop_close_failed' } else { 'stop_observation_failed' }
    return New-Result $succeeded $failureCode $script:command ([ordered]@{ checks = $checks; processes = @($remaining); port_57017 = $port; remaining_bound_process_ids = $remainingIds })
}

function Invoke-Uninstall {
    param([object]$Request)
    Assert-RequestFileIdentities $Request
    $beforeEntries = @(Get-BoundUninstallEntries -Entries @(Get-SuperDevUninstallEntries) -Request $Request)
    Assert-UninstallPrecondition $Request $beforeEntries
    $productCode = ''
    $target = $Request.artifact
    $executable = 'msiexec.exe'
    if ([string]$Request.format -eq 'msi' -and $beforeEntries.Count -eq 1) { $productCode = [string]$beforeEntries[0].key }
    if ([string]$Request.format -eq 'nsis') { $target = $Request.uninstaller_identity; $executable = [string]$Request.uninstaller_identity.path }
    $script:command = [ordered]@{ operation = 'uninstall'; method = 'start_process_wait_elevated'; executable = [System.IO.Path]::GetFileName($executable); target = $target; product_code = $productCode; process_ids = @(); exit_code = -1 }
    $filePath = ''
    if ([string]$Request.format -eq 'msi') {
        $arguments = '/x "{0}" /qn /norestart' -f [string]$Request.installer_path
        $filePath = Get-StockMSIExecPath
    } else {
        $filePath = [string]$Request.uninstaller_path
        $arguments = '/S'
    }
    $script:startedAt = [DateTime]::UtcNow.ToString('o')
    $invocationFailed = $false
    $exitCode = -1
    try {
        $process = Start-Process -FilePath $filePath -ArgumentList $arguments -Verb RunAs -Wait -PassThru
        $exitCode = [int]$process.ExitCode
    } catch { $invocationFailed = $true }
    $script:command.exit_code = $exitCode
    $afterEntries = @()
    $installPresent = $true
    $deadline = [DateTime]::UtcNow.AddSeconds(60)
    do {
        # NSIS 主进程退出后可能仍在完成自删除；只观察绑定 entry 与安装根，
        # 有界等待二者真实消失，超时后保留未通过事实。
        $afterEntries = @(Get-BoundUninstallEntries -Entries @(Get-SuperDevUninstallEntries) -Request $Request)
        $installPresent = Test-Path -LiteralPath ([string]$Request.install_directory) -PathType Container
        if ($afterEntries.Count -eq 0 -and -not $installPresent) { break }
        Start-Sleep -Milliseconds 500
    } while ([DateTime]::UtcNow -lt $deadline)
    $checks = New-Checks 'uninstall' @{ exit = ($exitCode -eq 0); uninstall = ($afterEntries.Count -eq 0); files = (-not $installPresent) }
    $succeeded = Test-ChecksPass $checks
    $failureCode = if ($succeeded) { '' } elseif ($invocationFailed) { 'uninstaller_invocation_failed' } else { 'uninstall_observation_failed' }
    return New-Result $succeeded $failureCode $script:command ([ordered]@{
        checks = $checks; install_path_present = $installPresent; product_files = @(); sidecar_files = @();
        uninstall_entries = @($afterEntries); processes = @(); remaining_bound_process_ids = @()
    })
}

try {
    Assert-Windows10ClientX64StandardUser
    $requestFullPath = [System.IO.Path]::GetFullPath($RequestPath)
    $outputFullPath = [System.IO.Path]::GetFullPath($OutputPath)
    if ([System.IO.Path]::GetDirectoryName($requestFullPath) -ne [System.IO.Path]::GetDirectoryName($outputFullPath)) { throw 'request_directory' }
    if ([System.IO.Path]::GetFileName($requestFullPath) -ne 'request.json' -or [System.IO.Path]::GetFileName($outputFullPath) -ne 'result.json') { throw 'request_identity' }
    $script:request = Get-Content -LiteralPath $requestFullPath -Raw -Encoding UTF8 | ConvertFrom-Json
    if ([int]$script:request.schema_version -ne 1 -or [string]$script:request.kind -ne 'superdev.windows-validation.installer-lifecycle-executor-request') { throw 'request_identity' }
    if ([string]$script:request.action -notin @('install', 'start', 'stop', 'uninstall')) { throw 'request_action' }
    if ([string]$script:request.format -ne [string]$script:request.binding.format -or
        [string]$script:request.product_version -ne [string]$script:request.binding.product_version -or
        [string]$script:request.install_directory -ne [string]$script:request.binding.install_directory -or
        [string]$script:request.artifact.sha256 -ne [string]$script:request.binding.artifact.sha256) { throw 'request_binding' }
    if ([string]::IsNullOrWhiteSpace([string]$script:request.prepared_backup_directory) -or
        [string]::IsNullOrWhiteSpace([string]$script:request.active_lock_path)) { throw 'request_binding' }
    $preparedBackupDirectory = [System.IO.Path]::GetFullPath([string]$script:request.prepared_backup_directory)
    $activeLockPath = [System.IO.Path]::GetFullPath([string]$script:request.active_lock_path)
    $expectedActiveLockPath = [System.IO.Path]::GetFullPath((Join-Path $preparedBackupDirectory '.installer-lifecycle.active.lock'))
    if (-not [string]::Equals($activeLockPath, $expectedActiveLockPath, [System.StringComparison]::OrdinalIgnoreCase)) { throw 'request_binding' }
    # FileShare.None 让 retry driver 的同路径探测在 helper/UAC 动作结束前 fail closed。
    # 该文件不携带 attempt、恢复或 verdict 含义；排他 handle 本身才表示当前动作仍在进行。
    $script:activeLock = [System.IO.File]::Open($activeLockPath, [System.IO.FileMode]::OpenOrCreate, [System.IO.FileAccess]::ReadWrite, [System.IO.FileShare]::None)
    $result = switch ([string]$script:request.action) {
        'install' { Invoke-Install $script:request }
        'start' { Invoke-Start $script:request }
        'stop' { Invoke-Stop $script:request }
        'uninstall' { Invoke-Uninstall $script:request }
        default { throw 'unsupported_action' }
    }
    Write-ResultJson $outputFullPath $result
    if ([bool]$result.succeeded) { $processExitCode = 0 }
} catch {
    # 只有已经进入真实动作的分支才写 attempted=true 的失败 result；前置校验失败不伪造尝试。
    if (-not [string]::IsNullOrWhiteSpace($script:startedAt) -and -not (Test-Path -LiteralPath $OutputPath -PathType Leaf)) {
        $fallbackChecks = New-Checks ([string]$script:request.action) @{
            exit = $false; product = $false; sidecars = $false; uninstall = $false;
            desktop = $false; listener = $false; identities = $false; processes = $false; files = $false
        }
        if ($null -eq $script:command) {
            $script:command = [ordered]@{ operation = [string]$script:request.action; method = ''; executable = ''; target = $script:request.artifact; product_code = ''; process_ids = @(); exit_code = $null }
        }
        $failureCode = switch ([string]$script:request.action) {
            'install' { 'installer_invocation_failed' }
            'start' { 'desktop_invocation_failed' }
            'stop' { 'desktop_close_failed' }
            'uninstall' { 'uninstaller_invocation_failed' }
        }
        $fallback = New-Result $false $failureCode $script:command ([ordered]@{ checks = $fallbackChecks; processes = @(); remaining_bound_process_ids = @() })
        try { Write-ResultJson $OutputPath $fallback } catch { }
    }
    $processExitCode = 1
} finally {
    if ($null -ne $script:activeLock) {
        $script:activeLock.Dispose()
        $script:activeLock = $null
    }
}
exit $processExitCode
