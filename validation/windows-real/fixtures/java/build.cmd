@rem Java fixture Windows build wrapper.
@rem Responsibilities: preflight the frozen JDK and compile a debug-symbol-rich class tree with a repeatable direct javac entry.
@rem Boundary: this wrapper does not require Maven, network access, Bash, or repository state outside this fixture.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"java","stage":"preflight"}
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed_preflight
if not exist "%ROOT%build\classes" mkdir "%ROOT%build\classes"
echo {"level":"info","event":"fixture_build_stage_started","provider":"java","stage":"javac","command_summary":"javac --release 21 --add-modules jdk.httpserver -g"}
javac --release 21 --add-modules jdk.httpserver -encoding UTF-8 -g -d "%ROOT%build\classes" "%ROOT%src\superdev\fixture\FixtureServer.java"
if errorlevel 1 goto :failed_compile
echo {"level":"info","event":"fixture_build_succeeded","provider":"java","entry":"superdev.fixture.FixtureServer","debug_symbols":"full"}
exit /b 0
:failed_preflight
echo {"level":"error","event":"fixture_build_failed","provider":"java","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_compile
echo {"level":"error","event":"fixture_build_failed","provider":"java","stage":"javac","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
