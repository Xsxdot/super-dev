@rem Rust fixture Visual Studio linker environment loader.
@rem Responsibilities: enter the frozen VS 2022 amd64 VCTools environment in the calling cmd process.
@rem Boundary: this script never installs Build Tools or mutates persistent machine/user environment variables.
@echo off
set "VSWHERE=%ProgramFiles(x86)%\Microsoft Visual Studio\Installer\vswhere.exe"
if not exist "%VSWHERE%" goto :missing_vswhere
for /f "usebackq tokens=*" %%I in (`"%VSWHERE%" -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath`) do set "VSINSTALL=%%I"
if not defined VSINSTALL goto :missing_vctools
for /f "usebackq tokens=*" %%I in (`"%VSWHERE%" -latest -products * -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property catalog_productDisplayVersion`) do set "VSVERSION=%%I"
echo %VSVERSION% | findstr.exe /b /c:"17.14." >nul
if errorlevel 1 goto :wrong_vctools
if not exist "%VSINSTALL%\Common7\Tools\VsDevCmd.bat" goto :missing_vctools
echo {"level":"info","event":"fixture_toolchain_initializing","provider":"rust","toolchain":"vs2022-amd64"}
call "%VSINSTALL%\Common7\Tools\VsDevCmd.bat" -arch=amd64 -host_arch=amd64 >nul
if errorlevel 1 goto :init_failed
where.exe cl >nul 2>nul
if errorlevel 1 goto :init_failed
echo {"level":"info","event":"fixture_toolchain_initialized","provider":"rust","toolchain":"vs2022-amd64"}
exit /b 0
:missing_vswhere
echo {"level":"error","event":"fixture_toolchain_failed","provider":"rust","code":"dependency_missing","dependency":"vswhere","remediation":"Install frozen VS 2022 Build Tools 17.14 with VCTools workload"} 1>&2
exit /b 10
:missing_vctools
echo {"level":"error","event":"fixture_toolchain_failed","provider":"rust","code":"dependency_missing","dependency":"MSVC v143 amd64 tools","remediation":"Repair frozen VS 2022 Build Tools 17.14 offline layout"} 1>&2
exit /b 10
:wrong_vctools
echo {"level":"error","event":"fixture_toolchain_failed","provider":"rust","code":"dependency_version_mismatch","dependency":"VS 2022 Build Tools","expected":"17.14.x","actual":"%VSVERSION%"} 1>&2
exit /b 10
:init_failed
echo {"level":"error","event":"fixture_toolchain_failed","provider":"rust","code":"toolchain_initialization_failed","dependency":"VsDevCmd amd64","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b 10
