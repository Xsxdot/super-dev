REM Go fixture run wrapper.
REM Responsibility: start the already-built fixture with inherited campaign-only environment.
REM Boundary: does not background the process or invent credentials.
@echo off
setlocal
echo {"level":"info","fixture":"go","stage":"run","event":"started"}
build\go-fixture.exe
set exit_code=%errorlevel%
if not "%exit_code%"=="0" echo {"level":"error","fixture":"go","stage":"run","event":"failed","exit_code":%exit_code%} 1>&2
exit /b %exit_code%
