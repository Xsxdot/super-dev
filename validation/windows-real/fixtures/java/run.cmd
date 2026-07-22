@rem Java fixture Windows run wrapper.
@rem Responsibilities: launch the compiled JVM class with the required jdk.httpserver module and safe standalone defaults.
@rem Boundary: the wrapper never logs fixture credentials or changes global Java configuration.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
if not exist "%ROOT%build\classes\superdev\fixture\FixtureServer.class" goto :not_built
if not defined FIXTURE_PORT set "FIXTURE_PORT=18173"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"java","stage":"exec","port":%FIXTURE_PORT%}
java --add-modules jdk.httpserver -cp "%ROOT%build\classes" superdev.fixture.FixtureServer
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"java","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"java","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
:not_built
echo {"level":"error","event":"fixture_run_failed","provider":"java","stage":"preflight","code":"fixture_not_built","remediation":"Run build.cmd first"} 1>&2
exit /b 10
