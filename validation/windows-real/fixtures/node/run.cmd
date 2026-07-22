@rem Node fixture Windows run wrapper.
@rem Responsibilities: start the fixture with safe standalone defaults when the campaign did not inject values.
@rem Boundary: defaults are fixture-only test values and are never emitted to logs or evidence.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
if not defined FIXTURE_PORT set "FIXTURE_PORT=18171"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"node","stage":"exec","port":%FIXTURE_PORT%}
node "%ROOT%src\server.js"
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"node","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"node","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
