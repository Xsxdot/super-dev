@rem Python fixture Windows build wrapper.
@rem Responsibilities: create a fixture-local venv that can see the preinstalled frozen debugpy and syntax-check source.
@rem Boundary: this wrapper never downloads packages or creates a python3 executable shim; debug readiness is judged later.
@echo off
setlocal EnableExtensions DisableDelayedExpansion
set "ROOT=%~dp0"
echo {"level":"info","event":"fixture_build_started","provider":"python","stage":"preflight"}
call "%ROOT%preflight.cmd" runtime
if errorlevel 1 goto :failed_preflight
if not exist "%ROOT%.venv\Scripts\python.exe" (
  echo {"level":"info","event":"fixture_build_stage_started","provider":"python","stage":"create_venv","command_summary":"python -m venv --system-site-packages fixture-local-path"}
  python -m venv --system-site-packages "%ROOT%.venv"
  if errorlevel 1 goto :failed_venv
)
echo {"level":"info","event":"fixture_build_stage_started","provider":"python","stage":"syntax_check","command_summary":"venv-python -m py_compile src/server.py"}
"%ROOT%.venv\Scripts\python.exe" -m py_compile "%ROOT%src\server.py"
if errorlevel 1 goto :failed_compile
echo {"level":"info","event":"fixture_build_succeeded","provider":"python","entry":"src/server.py"}
exit /b 0
:failed_preflight
echo {"level":"error","event":"fixture_build_failed","provider":"python","stage":"preflight","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_venv
echo {"level":"error","event":"fixture_build_failed","provider":"python","stage":"create_venv","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
:failed_compile
echo {"level":"error","event":"fixture_build_failed","provider":"python","stage":"syntax_check","exit_code":"%ERRORLEVEL%"} 1>&2
exit /b %ERRORLEVEL%
