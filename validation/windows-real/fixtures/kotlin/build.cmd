@rem Kotlin fixture Windows build wrapper.
@rem Responsibilities: preflight the frozen JDK/Kotlin toolchain and build an executable debug-symbol-rich jar.
@rem Boundary: this direct wrapper requires no Bash, network, global Gradle installation, or repository clone.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"kotlin","stage":"preflight"}
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed_preflight
if not exist "%ROOT%build" mkdir "%ROOT%build"
echo {"level":"info","event":"fixture_build_stage_started","provider":"kotlin","stage":"kotlinc","command_summary":"kotlinc jvm-target 21 include-runtime"}
kotlinc -J--add-modules=jdk.httpserver "%ROOT%src\FixtureServer.kt" -jvm-target 21 -include-runtime -d "%ROOT%build\fixture-kotlin.jar"
if errorlevel 1 goto :failed_compile
echo {"level":"info","event":"fixture_build_succeeded","provider":"kotlin","entry":"superdev.fixture.FixtureServerKt","debug_symbols":"full"}
exit /b 0
:failed_preflight
echo {"level":"error","event":"fixture_build_failed","provider":"kotlin","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_compile
echo {"level":"error","event":"fixture_build_failed","provider":"kotlin","stage":"kotlinc","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
