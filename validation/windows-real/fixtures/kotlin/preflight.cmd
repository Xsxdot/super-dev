@rem Kotlin fixture Windows prerequisite check.
@rem Responsibilities: verify JDK/Kotlin compiler separately from the experimental external JVM DAP wrapper.
@rem Boundary: this script does not download Gradle, invent an adapter, or report synthetic debug success.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "MODE=%~1"
if "%MODE%"=="" set "MODE=runtime"
echo {"level":"info","event":"fixture_preflight_started","provider":"kotlin","mode":"%MODE%"}
where.exe java >nul 2>nul
if errorlevel 1 goto :missing_java
where.exe javac >nul 2>nul
if errorlevel 1 goto :missing_javac
where.exe kotlinc >nul 2>nul
if errorlevel 1 goto :missing_kotlinc
java -version 2>&1 | findstr.exe /c:"21.0.11" >nul
if errorlevel 1 goto :wrong_jdk
kotlinc -version 2>&1 | findstr.exe /c:"2.4.0" >nul
if errorlevel 1 goto :wrong_kotlinc
if /I not "%MODE%"=="debug" goto :runtime_ready
if not defined SUPERDEV_JVM_ADAPTER_COMMAND goto :missing_adapter
if not exist "%SUPERDEV_JVM_ADAPTER_COMMAND%" goto :missing_adapter
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"kotlin","mode":"debug","adapter":"external_wrapper"}
exit /b 0
:runtime_ready
echo {"level":"info","event":"fixture_preflight_succeeded","provider":"kotlin","mode":"runtime","jdk":"21.0.11+10","kotlin":"2.4.0"}
exit /b 0
:missing_java
echo {"level":"error","event":"fixture_preflight_failed","provider":"kotlin","mode":"%MODE%","code":"dependency_missing","dependency":"java","remediation":"Install frozen Temurin JDK 21.0.11+10 x64 and restart Desktop Agent"} 1>&2
exit /b 10
:missing_javac
echo {"level":"error","event":"fixture_preflight_failed","provider":"kotlin","mode":"%MODE%","code":"dependency_missing","dependency":"javac","remediation":"Install the full frozen JDK rather than a JRE"} 1>&2
exit /b 10
:missing_kotlinc
echo {"level":"error","event":"fixture_preflight_failed","provider":"kotlin","mode":"%MODE%","code":"dependency_missing","dependency":"kotlinc","remediation":"Install frozen Kotlin 2.4.0 and restart Desktop Agent"} 1>&2
exit /b 10
:wrong_jdk
echo {"level":"error","event":"fixture_preflight_failed","provider":"kotlin","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"JDK","expected":"21.0.11+10"} 1>&2
exit /b 10
:wrong_kotlinc
echo {"level":"error","event":"fixture_preflight_failed","provider":"kotlin","mode":"%MODE%","code":"dependency_version_mismatch","dependency":"Kotlin","expected":"2.4.0"} 1>&2
exit /b 10
:missing_adapter
echo {"level":"error","event":"fixture_preflight_blocked","provider":"kotlin","mode":"debug","code":"known_experimental_capability_gap","dependency":"project-approved JVM DAP wrapper","remediation":"Provide a frozen wrapper implementing command plus port; do not substitute bare vscode-java-debug"} 1>&2
exit /b 20
