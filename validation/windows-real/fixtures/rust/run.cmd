@rem Rust fixture Windows run wrapper.
@rem Responsibilities: launch the MSVC debug binary with safe standalone defaults.
@rem Boundary: this wrapper never logs fixture credentials or falls back to a non-Windows artifact.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
set "BINARY=%ROOT%target\x86_64-pc-windows-msvc\debug\superdev-windows-rust-fixture.exe"
if not exist "%BINARY%" goto :not_built
if not defined FIXTURE_PORT set "FIXTURE_PORT=18175"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"rust","stage":"exec","port":%FIXTURE_PORT%}
"%BINARY%"
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"rust","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"rust","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
:not_built
echo {"level":"error","event":"fixture_run_failed","provider":"rust","stage":"preflight","code":"fixture_not_built","remediation":"Run build.cmd first"} 1>&2
exit /b 10
