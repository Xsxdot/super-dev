@rem C++ fixture Windows run wrapper.
@rem Responsibilities: launch the Debug x64 binary with safe standalone defaults.
@rem Boundary: this wrapper never logs fixture credentials or falls back to a non-Windows artifact.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
set "BINARY=%ROOT%build\superdev-windows-cpp-fixture.exe"
if not exist "%BINARY%" goto :not_built
if not defined FIXTURE_PORT set "FIXTURE_PORT=18176"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"cpp","stage":"exec","port":%FIXTURE_PORT%}
"%BINARY%"
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"cpp","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"cpp","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
:not_built
echo {"level":"error","event":"fixture_run_failed","provider":"cpp","stage":"preflight","code":"fixture_not_built","remediation":"Run build.cmd first"} 1>&2
exit /b 10
