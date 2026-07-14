# uninstall-agent.windows.test.ps1 validates the Windows manual uninstall contract.
#
# Responsibilities:
#   - Run uninstall-agent.ps1 against an isolated ProgramData fixture.
#   - Stub Scheduled Task and process commands to prove retention, purge, and idempotency.
#
# Boundaries:
#   - Never accesses the machine's real SuperDev Scheduled Task or ProgramData tree.
#   - Runs only on Windows PowerShell/PowerShell Core CI lanes.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent (Split-Path -Parent $PSCommandPath)
$Script = Join-Path $Root 'scripts\uninstall-agent.ps1'
$Fixture = Join-Path ([System.IO.Path]::GetTempPath()) ("superdev-uninstall-" + [guid]::NewGuid())
$env:SUPERDEV_UNINSTALL_TESTING = '1'
$env:SUPERDEV_UNINSTALL_FIXTURE_ROOT = $Fixture
$script:TaskExists = $true
$script:TaskAction = 'C:\ProgramData\SuperDev\Agent\superdev-agent.exe'
$script:CommandLog = [System.Collections.Generic.List[string]]::new()
$script:FailTaskStop = $false

function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}

function Get-ScheduledTask {
    param([string]$TaskName, [object]$ErrorAction)
    $script:CommandLog.Add("Get-ScheduledTask $TaskName")
    if (-not $script:TaskExists) { return $null }
    return [pscustomobject]@{ Actions = @([pscustomobject]@{ Execute = $script:TaskAction }) }
}

function Stop-ScheduledTask {
    param([string]$TaskName, [object]$ErrorAction)
    $script:CommandLog.Add("Stop-ScheduledTask $TaskName")
    if ($script:FailTaskStop) { throw 'fixture Scheduled Task stop failure' }
}

function Unregister-ScheduledTask {
    param([string]$TaskName, [switch]$Confirm, [object]$ErrorAction)
    $script:CommandLog.Add("Unregister-ScheduledTask $TaskName")
    $script:TaskExists = $false
}

function Get-Process {
    param([string]$Name, [object]$ErrorAction)
    $script:CommandLog.Add("Get-Process $Name")
    return $null
}

try {
    $AgentRoot = Join-Path $Fixture 'C\ProgramData\SuperDev\Agent'
    New-Item -ItemType Directory -Force -Path (Join-Path $AgentRoot 'data') | Out-Null
    Set-Content -LiteralPath (Join-Path $AgentRoot 'superdev-agent.exe') -Value 'fixture'
    Set-Content -LiteralPath (Join-Path $AgentRoot 'data\security.json') -Value '{}'

    $output = & $Script
    Assert-True (($output -join "`n") -match 'level=INFO stage=detect') 'Windows output lacks detect stage'
    Assert-True (($output -join "`n") -match 'level=INFO stage=complete') 'Windows output lacks complete stage'
    Assert-True (-not (Test-Path -LiteralPath (Join-Path $AgentRoot 'superdev-agent.exe'))) 'Windows binary was not removed'
    Assert-True (Test-Path -LiteralPath (Join-Path $AgentRoot 'data\security.json')) 'Windows data was removed by default'
    Assert-True ($script:CommandLog.Contains('Stop-ScheduledTask SuperDevAgent')) 'Agent Scheduled Task was not stopped'
    Assert-True ($script:CommandLog.Contains('Unregister-ScheduledTask SuperDevAgent')) 'Agent Scheduled Task was not removed'
    Assert-True (-not (($script:CommandLog -join "`n") -match 'Docker')) 'Windows cleanup addressed Docker'

    & $Script | Out-Null
    & $Script -Purge | Out-Null
    Assert-True (-not (Test-Path -LiteralPath $AgentRoot)) 'Windows purge did not remove Agent root'

    $script:TaskExists = $true
    $script:TaskAction = 'C:\ProgramData\SuperDev\Agent\superdev-agent.exe'
    $script:FailTaskStop = $true
    $failureOutput = [System.Collections.Generic.List[string]]::new()
    $failed = $false
    try { & $Script | ForEach-Object { $failureOutput.Add([string]$_) } } catch { $failed = $true }
    Assert-True $failed 'Scheduled Task stop failure must fail the script'
    Assert-True (($failureOutput -join "`n") -match 'level=ERROR stage=windows_task') 'Scheduled Task failure lacks its cleanup stage'
    Assert-True (-not (($failureOutput -join "`n") -match 'level=ERROR stage=detect')) 'Scheduled Task failure was mislabeled as detection failure'
    $script:FailTaskStop = $false

    $script:TaskExists = $true
    $script:TaskAction = 'C:\Custom\superdev-agent.exe'
    New-Item -ItemType Directory -Force -Path $AgentRoot | Out-Null
    Set-Content -LiteralPath (Join-Path $AgentRoot 'superdev-agent.exe') -Value 'fixture'
    $failed = $false
    try { & $Script | Out-Null } catch { $failed = $true; $errorText = $_.Exception.Message }
    Assert-True $failed 'Custom Windows task layout must fail'
    Assert-True ($errorText -match 'unsupported') 'Custom Windows task failure is not explicit'
    Assert-True (Test-Path -LiteralPath (Join-Path $AgentRoot 'superdev-agent.exe')) 'Custom layout was mutated'

    Write-Output 'uninstall-agent.windows.test: all checks passed'
}
finally {
    Remove-Item Env:SUPERDEV_UNINSTALL_TESTING -ErrorAction SilentlyContinue
    Remove-Item Env:SUPERDEV_UNINSTALL_FIXTURE_ROOT -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $Fixture -Recurse -Force -ErrorAction SilentlyContinue
}
