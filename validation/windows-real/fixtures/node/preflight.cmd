@rem Node fixture Windows prerequisite check.
@rem Responsibilities: separate runtime/package-manager checks from packaged js-debug checks and return stable exit categories.
@rem Boundary: this script never installs dependencies, starts an adapter, or writes credentials.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"

echo {"level":"info","event":"fixture_preflight_started","provider":"node","mode":"%MODE%"}
where.exe node >nul 2>nul
if errorlevel 1 goto :missing_node
where.exe npm >nul 2>nul
if errorlevel 1 goto :missing_npm
for /f "delims=" %%V in ('node --version') do set "NODE_VERSION=%%V"
for /f "delims=" %%V in ('npm --version') do set "NPM_VERSION=%%V"
if /I not "%NODE_VERSION%"=="v24.18.0" goto :wrong_node_version
if /I not "%NPM_VERSION%"=="11.16.0" goto :wrong_npm_version

if /I not "%MODE%"=="debug" goto :runtime_ready
if not defined SUPERDEV_AGENT_DATA_DIR goto :missing_agent_data
if not exist "%SUPERDEV_AGENT_DATA_DIR%\js-debug\src\dapDebugServer.js" goto :missing_adapter
if not exist "%SUPERDEV_AGENT_DATA_DIR%\js-debug\.superdev-version" goto :missing_adapter_version
findstr.exe /x /c:"1.117.0" "%SUPERDEV_AGENT_DATA_DIR%\js-debug\.superdev-version" >nul
if errorlevel 1 goto :wrong_adapter_version
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"node","mode":"debug","node_version":"%NODE_VERSION%","npm_version":"%NPM_VERSION%","adapter_version":"1.117.0"}
exit /b 0

:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"node","mode":"runtime","node_version":"%NODE_VERSION%","npm_version":"%NPM_VERSION%"}
exit /b 0
:missing_node
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"%MODE%","code":"dependency_missing","dependency":"node","remediation":"Install frozen Node.js 24.18.0 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:missing_npm
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"%MODE%","code":"dependency_missing","dependency":"npm","remediation":"Repair the frozen Node.js MSI installation"} 1>&2
exit /b 10
:wrong_node_version
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"node","expected":"v24.18.0","actual":"%NODE_VERSION%"} 1>&2
exit /b 10
:wrong_npm_version
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"npm","expected":"11.16.0","actual":"%NPM_VERSION%"} 1>&2
exit /b 10
:missing_agent_data
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"debug","code":"prerequisite_context_missing","dependency":"SUPERDEV_AGENT_DATA_DIR","remediation":"Set the installed Agent data directory before packaged adapter preflight"} 1>&2
exit /b 10
:missing_adapter
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"debug","code":"product_packaging_defect","dependency":"js-debug dapDebugServer.js","remediation":"Reinstall the frozen SuperDev build and preserve this evidence"} 1>&2
exit /b 30
:missing_adapter_version
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"debug","code":"product_packaging_defect","dependency":"js-debug version marker","remediation":"Reinstall the frozen SuperDev build and preserve this evidence"} 1>&2
exit /b 30
:wrong_adapter_version
echo {"level":"error","event":"fixture_preflight_failed","provider":"node","mode":"debug","code":"build_identity_mismatch","dependency":"js-debug","expected":"1.117.0"} 1>&2
exit /b 30
