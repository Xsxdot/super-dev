@rem Rust fixture Windows build wrapper.
@rem Responsibilities: preflight the frozen MSVC target and run cargo build --locked with development debug info.
@rem Boundary: this wrapper never downloads toolchains, invokes Bash, or reports a Windows runtime verdict.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"rust","stage":"preflight"}
call "%ROOT%toolchain.cmd"
if errorlevel 1 goto :failed_toolchain
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed_preflight
pushd "%ROOT%" >nul
echo {"level":"info","event":"fixture_build_stage_started","provider":"rust","stage":"cargo_build_locked","command_summary":"cargo build --locked --target x86_64-pc-windows-msvc"}
cargo build --locked --target x86_64-pc-windows-msvc
set "BUILD_EXIT=%ERRORLEVEL%"
popd >nul
if not "%BUILD_EXIT%"=="0" goto :failed_build
echo {"level":"info","event":"fixture_build_succeeded","provider":"rust","target":"x86_64-pc-windows-msvc","debug_symbols":"full"}
exit /b 0
:failed_toolchain
echo {"level":"error","event":"fixture_build_failed","provider":"rust","stage":"toolchain","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_preflight
echo {"level":"error","event":"fixture_build_failed","provider":"rust","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_build
echo {"level":"error","event":"fixture_build_failed","provider":"rust","stage":"cargo_build_locked","exit_code":"%BUILD_EXIT%"} 1>&2
exit /b %BUILD_EXIT%
