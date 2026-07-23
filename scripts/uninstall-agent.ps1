# uninstall-agent.ps1 manually removes the supported Windows SuperDev Agent installation.
#
# Responsibilities:
#   - Validate and remove the SuperDevAgent Scheduled Task and canonical Agent binary.
#   - Preserve Agent data and logs unless -Purge is explicitly supplied.
#   - Treat already-absent Agent-owned resources as a successful retry.
#
# Boundaries:
#   - Does not remove Controller configuration, Hosts, Docker resources, or unrelated tasks.
#   - Does not accept custom install paths or terminate processes outside the supported task layout.
[CmdletBinding()]
param(
    [switch]$Purge
)

$ErrorActionPreference = 'Stop'
$FixtureRoot = $env:SUPERDEV_UNINSTALL_FIXTURE_ROOT
if ($FixtureRoot -and $env:SUPERDEV_UNINSTALL_TESTING -ne '1') {
    throw 'level=ERROR stage=detect message="fixture root is test-only"'
}

$CanonicalAgentRoot = 'C:\ProgramData\SuperDev\Agent'
# String join, not Join-Path: contract tests stub this comparison on non-Windows pwsh lanes where drive C: does not exist.
$CanonicalBinary = $CanonicalAgentRoot + '\superdev-agent.exe'
$AgentRoot = if ($FixtureRoot) {
    Join-Path $FixtureRoot 'C\ProgramData\SuperDev\Agent'
} else {
    $CanonicalAgentRoot
}
$Binary = Join-Path $AgentRoot 'superdev-agent.exe'
$script:FailureStage = 'detect'
$script:FailureLogged = $false

function Write-UninstallEvent {
    param(
        [Parameter(Mandatory)][ValidateSet('INFO', 'ERROR')][string]$Level,
        [Parameter(Mandatory)][string]$Stage,
        [Parameter(Mandatory)][string]$Message
    )
    $safeMessage = $Message.Replace("`r", ' ').Replace("`n", ' ').Replace('"', "'")
    Write-Output ('level={0} stage={1} message="{2}"' -f $Level, $Stage, $safeMessage)
}

function Invoke-UninstallAction {
    param(
        [Parameter(Mandatory)][string]$Stage,
        [Parameter(Mandatory)][string]$Action,
        [Parameter(Mandatory)][scriptblock]$Operation
    )
    $script:FailureStage = $Stage
    Write-UninstallEvent -Level INFO -Stage $Stage -Message "starting $Action"
    try {
        & $Operation
    }
    catch {
        Write-UninstallEvent -Level ERROR -Stage $Stage -Message "$Action failed: $($_.Exception.Message)"
        $script:FailureLogged = $true
        throw
    }
    Write-UninstallEvent -Level INFO -Stage $Stage -Message "completed $Action"
}

function Test-SupportedTaskAction {
    param([Parameter(Mandatory)][object]$Task)
    $actions = @($Task.Actions)
    if ($actions.Count -ne 1) { return $false }
    $execute = [Environment]::ExpandEnvironmentVariables([string]$actions[0].Execute).Trim().Trim('"')
    return $execute.Equals($CanonicalBinary, [StringComparison]::OrdinalIgnoreCase)
}

try {
    Write-UninstallEvent -Level INFO -Stage detect -Message 'starting Windows Agent layout detection'
    $task = Get-ScheduledTask -TaskName 'SuperDevAgent' -ErrorAction SilentlyContinue
    if ($null -ne $task -and -not (Test-SupportedTaskAction -Task $task)) {
        throw 'unsupported custom SuperDevAgent task action; no resources were changed'
    }

    if ($null -ne $task) {
        Invoke-UninstallAction -Stage windows_task -Action 'stop Agent Scheduled Task' -Operation {
            Stop-ScheduledTask -TaskName 'SuperDevAgent' -ErrorAction Stop
        }
        Invoke-UninstallAction -Stage windows_task -Action 'remove Agent Scheduled Task' -Operation {
            Unregister-ScheduledTask -TaskName 'SuperDevAgent' -Confirm:$false -ErrorAction Stop
        }
    }

    # A remaining process may come from an unsupported custom launcher. Failing is safer than killing it by name.
    $script:FailureStage = 'windows_process'
    $process = Get-Process -Name 'superdev-agent' -ErrorAction SilentlyContinue
    if ($null -ne $process) {
        throw 'superdev-agent is still running; stop the owning custom launcher before retrying'
    }

    if (Test-Path -LiteralPath $Binary -PathType Leaf) {
        Invoke-UninstallAction -Stage windows_binary -Action 'remove Agent binary' -Operation {
            Remove-Item -LiteralPath $Binary -Force
        }
    }

    if ($Purge -and (Test-Path -LiteralPath $AgentRoot)) {
        Invoke-UninstallAction -Stage purge -Action 'remove Agent data and logs' -Operation {
            Remove-Item -LiteralPath $AgentRoot -Recurse -Force
        }
    }

    Write-UninstallEvent -Level INFO -Stage complete -Message "Agent uninstall completed; purge=$($Purge.IsPresent.ToString().ToLowerInvariant())"
}
catch {
    if (-not $script:FailureLogged) {
        Write-UninstallEvent -Level ERROR -Stage $script:FailureStage -Message $_.Exception.Message
    }
    throw
}
