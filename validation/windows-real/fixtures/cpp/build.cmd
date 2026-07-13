@rem C++ fixture Windows build wrapper.
@rem Responsibilities: initialize frozen VS tools and build Debug x64 with clang-cl, CMake and Ninja.
@rem Boundary: this wrapper never downloads toolchains, invokes Bash, or reports a Windows runtime verdict.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"cpp","stage":"toolchain"}
call "%ROOT%toolchain.cmd"
if errorlevel 1 goto :failed_toolchain
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed_preflight
echo {"level":"info","event":"fixture_build_stage_started","provider":"cpp","stage":"cmake_configure","command_summary":"cmake Ninja Debug clang-cl"}
cmake -S "%ROOT%" -B "%ROOT%build" -G Ninja -DCMAKE_BUILD_TYPE=Debug -DCMAKE_CXX_COMPILER=clang-cl
if errorlevel 1 goto :failed_configure
echo {"level":"info","event":"fixture_build_stage_started","provider":"cpp","stage":"cmake_build","command_summary":"cmake --build fixture-local-build --config Debug"}
cmake --build "%ROOT%build" --config Debug
if errorlevel 1 goto :failed_build
echo {"level":"info","event":"fixture_build_succeeded","provider":"cpp","binary":"build/superdev-windows-cpp-fixture.exe","debug_symbols":"pdb-full"}
exit /b 0
:failed_toolchain
echo {"level":"error","event":"fixture_build_failed","provider":"cpp","stage":"toolchain","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_preflight
echo {"level":"error","event":"fixture_build_failed","provider":"cpp","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_configure
echo {"level":"error","event":"fixture_build_failed","provider":"cpp","stage":"cmake_configure","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_build
echo {"level":"error","event":"fixture_build_failed","provider":"cpp","stage":"cmake_build","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
