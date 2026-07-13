@rem Kotlin fixture Windows run wrapper.
@rem Responsibilities: launch the compiled Kotlin/JVM class with required module and safe standalone defaults.
@rem Boundary: this wrapper does not log fixture credentials or modify global JVM/Kotlin configuration.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
if not exist "%ROOT%build\fixture-kotlin.jar" goto :not_built
if not defined FIXTURE_PORT set "FIXTURE_PORT=18174"
if not defined FIXTURE_CAMPAIGN_ID set "FIXTURE_CAMPAIGN_ID=standalone"
echo {"level":"info","event":"fixture_run_started","provider":"kotlin","stage":"exec","port":%FIXTURE_PORT%}
java --add-modules jdk.httpserver -cp "%ROOT%build\fixture-kotlin.jar" superdev.fixture.FixtureServerKt
set "RUN_EXIT=%ERRORLEVEL%"
if not "%RUN_EXIT%"=="0" echo {"level":"error","event":"fixture_run_failed","provider":"kotlin","stage":"exec","exit_code":"%RUN_EXIT%"} 1>&2
if "%RUN_EXIT%"=="0" echo {"level":"info","event":"fixture_run_succeeded","provider":"kotlin","stage":"exec","exit_code":"0"}
exit /b %RUN_EXIT%
:not_built
echo {"level":"error","event":"fixture_run_failed","provider":"kotlin","stage":"preflight","code":"fixture_not_built","remediation":"Run build.cmd first"} 1>&2
exit /b 10
