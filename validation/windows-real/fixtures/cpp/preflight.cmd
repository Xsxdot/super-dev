@rem C++ fixture Windows prerequisite check.
@rem Responsibilities: verify MSVC/clang-cl/CMake/Ninja separately from lldb-dap's exact product listen contract.
@rem Boundary: this script never installs tools, rewrites adapter URI, or reports a synthetic debug pass.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
echo {"level":"info","event":"fixture_preflight_started","provider":"cpp","mode":"%MODE%"}
call "%ROOT%toolchain.cmd"
if errorlevel 1 exit /b %ERRORLEVEL%
where.exe cl >nul 2>nul
if errorlevel 1 goto :missing_cl
where.exe clang-cl >nul 2>nul
if errorlevel 1 goto :missing_clang
where.exe cmake >nul 2>nul
if errorlevel 1 goto :missing_cmake
where.exe ninja >nul 2>nul
if errorlevel 1 goto :missing_ninja
clang-cl --version 2>&1 | findstr.exe /c:"22.1.3" >nul
if errorlevel 1 goto :wrong_clang
cmake --version 2>&1 | findstr.exe /b /c:"cmake version 4.4.0" >nul
if errorlevel 1 goto :wrong_cmake
ninja --version 2>&1 | findstr.exe /x /c:"1.13.2" >nul
if errorlevel 1 goto :wrong_ninja
if /I not "%MODE%"=="debug" goto :runtime_ready
where.exe lldb-dap >nul 2>nul
if errorlevel 1 goto :missing_lldb
lldb-dap --version >nul 2>nul
if errorlevel 1 goto :bad_lldb
lldb-dap --version 2>&1 | findstr.exe /c:"22.1.3" >nul
if errorlevel 1 goto :wrong_lldb
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"cpp","mode":"debug","adapter":"lldb-dap","adapter_version":"22.1.3"}
exit /b 0
:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"cpp","mode":"runtime","msvc":"17.14","cmake":"4.4.0","ninja":"1.13.2","llvm":"22.1.3"}
exit /b 0
:missing_cl
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_missing","dependency":"MSVC cl","remediation":"Install frozen VS 2022 Build Tools 17.14 VCTools workload"} 1>&2
exit /b 10
:missing_clang
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_missing","dependency":"clang-cl","remediation":"Install frozen LLVM 22.1.3 x64"} 1>&2
exit /b 10
:missing_cmake
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_missing","dependency":"cmake","remediation":"Install frozen CMake 4.4.0 x64"} 1>&2
exit /b 10
:missing_ninja
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_missing","dependency":"ninja","remediation":"Install frozen Ninja 1.13.2"} 1>&2
exit /b 10
:wrong_clang
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"clang-cl","expected":"22.1.3"} 1>&2
exit /b 10
:wrong_cmake
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"cmake","expected":"4.4.0"} 1>&2
exit /b 10
:wrong_ninja
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"ninja","expected":"1.13.2"} 1>&2
exit /b 10
:missing_lldb
echo {"level":"error","event":"fixture_preflight_blocked","provider":"cpp","mode":"debug","code":"dependency_missing","dependency":"lldb-dap 22.1.3","remediation":"Install frozen LLVM 22.1.3 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:bad_lldb
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"debug","code":"adapter_command_failed","dependency":"lldb-dap","remediation":"Run the exact listen URI handshake and preserve evidence"} 1>&2
exit /b 10
:wrong_lldb
echo {"level":"error","event":"fixture_preflight_failed","provider":"cpp","mode":"debug","code":"dependency_version_mismatch","dependency":"lldb-dap","expected":"22.1.3"} 1>&2
exit /b 10
