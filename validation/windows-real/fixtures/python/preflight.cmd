@rem Python fixture Windows prerequisite check.
@rem Responsibilities: distinguish runtime/debugpy readiness from the frozen product python3 adapter command contract.
@rem Boundary: this script never creates a python3 shim, installs packages, or converts a product defect into PASS.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_preflight_started","provider":"python","mode":"%MODE%"}
where.exe python >nul 2>nul
if errorlevel 1 goto :missing_python
where.exe py >nul 2>nul
if errorlevel 1 echo {"level":"warning","event":"fixture_preflight_observation","provider":"python","mode":"%MODE%","code":"optional_launcher_missing","dependency":"py launcher"}
python -m pip --version >nul 2>nul
if errorlevel 1 goto :missing_pip
python -c "import sys; assert sys.version_info[:3] == (3, 14, 6)" >nul 2>nul
if errorlevel 1 goto :wrong_python
if /I not "%MODE%"=="debug" goto :runtime_ready
if not exist "%ROOT%.venv\Scripts\python.exe" goto :missing_venv
"%ROOT%.venv\Scripts\python.exe" -c "import debugpy; assert debugpy.__version__ == '1.8.21'" >nul 2>nul
if errorlevel 1 goto :missing_debugpy
where.exe python3 >nul 2>nul
if errorlevel 1 goto :python3_contract
python3 -c "import debugpy; assert debugpy.__version__ == '1.8.21'" >nul 2>nul
if errorlevel 1 goto :python3_contract
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"python","mode":"debug","debugpy_version":"1.8.21"}
exit /b 0
:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"python","mode":"runtime"}
exit /b 0
:missing_python
echo {"level":"error","event":"fixture_preflight_failed","provider":"python","mode":"%MODE%","code":"dependency_missing","dependency":"python","remediation":"Install frozen CPython 3.14.6 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:missing_pip
echo {"level":"error","event":"fixture_preflight_failed","provider":"python","mode":"%MODE%","code":"dependency_missing","dependency":"pip","remediation":"Repair the frozen CPython installation"} 1>&2
exit /b 10
:wrong_python
echo {"level":"error","event":"fixture_preflight_failed","provider":"python","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"CPython","expected":"3.14.6"} 1>&2
exit /b 10
:missing_venv
echo {"level":"error","event":"fixture_preflight_failed","provider":"python","mode":"debug","code":"dependency_missing","dependency":"fixture .venv","remediation":"Run build.cmd before debug preflight"} 1>&2
exit /b 10
:missing_debugpy
echo {"level":"error","event":"fixture_preflight_blocked","provider":"python","mode":"debug","code":"dependency_missing","dependency":"debugpy 1.8.21","remediation":"Install frozen debugpy 1.8.21 into the validated CPython environment before rerunning this lane"} 1>&2
exit /b 10
:python3_contract
echo {"level":"error","event":"fixture_preflight_failed","provider":"python","mode":"debug","code":"product_adapter_contract_defect","dependency":"python3 plus debugpy 1.8.21","remediation":"Preserve evidence; do not create a python3.exe shim"} 1>&2
exit /b 30
