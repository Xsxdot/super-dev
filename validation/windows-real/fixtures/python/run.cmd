@rem Python fixture Windows run wrapper.
@rem Responsibilities: start the isolated fixture interpreter with safe standalone defaults.
@rem Boundary: fixture credentials are never echoed and the wrapper does not alter global PATH or Python installs.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
if not exist "%ROOT%.venv\Scripts\python.exe" goto :not_built
if not defined FIXTURE_PORT set "FIXTURE_PORT=18172"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"python","stage":"exec","port":%FIXTURE_PORT%}
"%ROOT%.venv\Scripts\python.exe" "%ROOT%src\server.py"
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"python","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"python","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
:not_built
echo {"level":"error","event":"fixture_run_failed","provider":"python","stage":"preflight","code":"fixture_not_built","remediation":"Run build.cmd first"} 1>&2
exit /b 10
