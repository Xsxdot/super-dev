@rem Node fixture Windows build wrapper.
@rem Responsibilities: validate the frozen Node/npm runtime and syntax-check the fixture from its own directory.
@rem Boundary: this script does not install packages, invoke Bash, or claim a Windows functional verdict.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"node","stage":"preflight"}
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed
pushd "%ROOT%" >nul
echo {"level":"info","event":"fixture_build_stage_started","provider":"node","stage":"syntax_check","command_summary":"npm run build --silent"}
call npm run build --silent
set "BUILD_EXIT=%ERRORLEVEL%"
popd >nul
if not "%BUILD_EXIT%"=="0" goto :failed_build
echo {"level":"info","event":"fixture_build_succeeded","provider":"node","entry":"src/server.js"}
exit /b 0
:failed
echo {"level":"error","event":"fixture_build_failed","provider":"node","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_build
echo {"level":"error","event":"fixture_build_failed","provider":"node","stage":"syntax_check","exit_code":"%BUILD_EXIT%"} 1>&2
exit /b %BUILD_EXIT%
