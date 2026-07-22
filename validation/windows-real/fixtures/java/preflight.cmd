@rem Java fixture Windows prerequisite check.
@rem Responsibilities: verify JDK runtime/build tools separately from the experimental external JVM DAP wrapper.
@rem Boundary: this script does not invent, download, or simulate a JVM debug adapter.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
echo {"level":"info","event":"fixture_preflight_started","provider":"java","mode":"%MODE%"}
where.exe java >nul 2>nul
if errorlevel 1 goto :missing_java
where.exe javac >nul 2>nul
if errorlevel 1 goto :missing_javac
java -version 2>&1 | findstr.exe /c:"21.0.11" >nul
if errorlevel 1 goto :wrong_jdk
javac -version 2>&1 | findstr.exe /b /c:"javac 21.0.11" >nul
if errorlevel 1 goto :wrong_jdk
if /I not "%MODE%"=="debug" goto :runtime_ready
if not defined SUPERDEV_JVM_ADAPTER_COMMAND goto :missing_adapter
if not exist "%SUPERDEV_JVM_ADAPTER_COMMAND%" goto :missing_adapter
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"java","mode":"debug","adapter":"external_wrapper"}
exit /b 0
:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"java","mode":"runtime","jdk":"21.0.11+10"}
exit /b 0
:missing_java
echo {"level":"error","event":"fixture_preflight_failed","provider":"java","mode":"%MODE%","code":"dependency_missing","dependency":"java","remediation":"Install frozen Temurin JDK 21.0.11+10 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:missing_javac
echo {"level":"error","event":"fixture_preflight_failed","provider":"java","mode":"%MODE%","code":"dependency_missing","dependency":"javac","remediation":"Install the full frozen JDK rather than a JRE"} 1>&2
exit /b 10
:wrong_jdk
echo {"level":"error","event":"fixture_preflight_failed","provider":"java","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"JDK","expected":"21.0.11+10"} 1>&2
exit /b 10
:missing_adapter
echo {"level":"error","event":"fixture_preflight_blocked","provider":"java","mode":"debug","code":"known_experimental_capability_gap","dependency":"project-approved JVM DAP wrapper","remediation":"Provide a frozen wrapper implementing command plus port; do not substitute bare vscode-java-debug"} 1>&2
exit /b 20
