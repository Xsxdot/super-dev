@rem Rust fixture Windows prerequisite check.
@rem Responsibilities: verify frozen MSVC Rust toolchain separately from lldb-dap's exact product listen contract.
@rem Boundary: this script never installs rustup targets, changes the default toolchain, or rewrites adapter arguments.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_preflight_started","provider":"rust","mode":"%MODE%"}
call "%ROOT%toolchain.cmd"
if errorlevel 1 exit /b %ERRORLEVEL%
where.exe cl >nul 2>nul
if errorlevel 1 goto :missing_msvc
where.exe rustup >nul 2>nul
if errorlevel 1 goto :missing_rustup
where.exe rustc >nul 2>nul
if errorlevel 1 goto :missing_rustc
where.exe cargo >nul 2>nul
if errorlevel 1 goto :missing_cargo
rustc --version | findstr.exe /b /c:"rustc 1.97.0" >nul
if errorlevel 1 goto :wrong_rust
rustup target list --installed | findstr.exe /x /c:"x86_64-pc-windows-msvc" >nul
if errorlevel 1 goto :missing_target
if /I not "%MODE%"=="debug" goto :runtime_ready
where.exe lldb-dap >nul 2>nul
if errorlevel 1 goto :missing_lldb
lldb-dap --version >nul 2>nul
if errorlevel 1 goto :bad_lldb
lldb-dap --version 2>&1 | findstr.exe /c:"22.1.3" >nul
if errorlevel 1 goto :wrong_lldb
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"rust","mode":"debug","adapter":"lldb-dap","adapter_version":"22.1.3"}
exit /b 0
:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"rust","mode":"runtime","rust":"1.97.0","target":"x86_64-pc-windows-msvc"}
exit /b 0
:missing_rustup
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_missing","dependency":"rustup","remediation":"Install frozen Rust 1.97.0 MSVC toolchain"} 1>&2
exit /b 10
:missing_msvc
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_missing","dependency":"MSVC v143 linker environment","remediation":"Install frozen VS 2022 Build Tools 17.14 and expose it to Desktop Agent"} 1>&2
exit /b 10
:missing_rustc
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_missing","dependency":"rustc","remediation":"Repair the frozen Rust toolchain"} 1>&2
exit /b 10
:missing_cargo
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_missing","dependency":"cargo","remediation":"Repair the frozen Rust toolchain"} 1>&2
exit /b 10
:missing_target
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_missing","dependency":"x86_64-pc-windows-msvc target","remediation":"Install the frozen MSVC host toolchain and target before restarting Desktop Agent"} 1>&2
exit /b 10
:wrong_rust
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"rustc","expected":"1.97.0"} 1>&2
exit /b 10
:missing_lldb
echo {"level":"error","event":"fixture_preflight_blocked","provider":"rust","mode":"debug","code":"dependency_missing","dependency":"lldb-dap 22.1.3","remediation":"Install frozen LLVM 22.1.3 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:bad_lldb
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"debug","code":"adapter_command_failed","dependency":"lldb-dap","remediation":"Run the exact listen URI handshake and preserve evidence"} 1>&2
exit /b 10
:wrong_lldb
echo {"level":"error","event":"fixture_preflight_failed","provider":"rust","mode":"debug","code":"dependency_version_mismatch","dependency":"lldb-dap","expected":"22.1.3"} 1>&2
exit /b 10
