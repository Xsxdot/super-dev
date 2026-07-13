@rem Go fixture Windows prerequisite check.
@rem Responsibilities: verify the real Go runtime and Delve adapter before provider execution.
@rem Boundary: this script never installs tools, mutates PATH, or converts a missing adapter into PASS.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
echo {"level":"info","event":"fixture_preflight_started","provider":"go","mode":"%MODE%"}
where.exe go >nul 2>nul
if errorlevel 1 goto :missing_go
go version >nul 2>nul
if errorlevel 1 goto :bad_go
if /I not "%MODE%"=="debug" goto :ready
where.exe dlv >nul 2>nul
if errorlevel 1 goto :missing_dlv
dlv version >nul 2>nul
if errorlevel 1 goto :bad_dlv
:ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"go","mode":"%MODE%"}
exit /b 0
:missing_go
echo {"level":"error","event":"fixture_preflight_blocked","provider":"go","mode":"%MODE%","code":"dependency_missing","dependency":"go","remediation":"Install Go 1.22 or newer x64 and restart Desktop Agent"} 1>&2
exit /b 10
:bad_go
echo {"level":"error","event":"fixture_preflight_failed","provider":"go","mode":"%MODE%","code":"dependency_command_failed","dependency":"go"} 1>&2
exit /b 30
:missing_dlv
echo {"level":"error","event":"fixture_preflight_blocked","provider":"go","mode":"debug","code":"dependency_missing","dependency":"dlv","remediation":"Install Delve on the Windows validation machine and restart Desktop Agent"} 1>&2
exit /b 10
:bad_dlv
echo {"level":"error","event":"fixture_preflight_failed","provider":"go","mode":"debug","code":"adapter_command_failed","dependency":"dlv"} 1>&2
exit /b 30
