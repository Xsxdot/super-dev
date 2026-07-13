REM Go fixture build wrapper.
REM Responsibility: build a debuggable Windows executable inside the disposable campaign workspace.
REM Boundary: does not download Go, mutate SuperDev state, or invoke Unix tooling.
@echo off
setlocal
echo {"level":"info","fixture":"go","stage":"build","event":"started"}
if not exist build mkdir build
go build -gcflags "all=-N -l" -o build\go-fixture.exe .
if errorlevel 1 (
  echo {"level":"error","fixture":"go","stage":"build","event":"failed"} 1>&2
  exit /b 1
)
echo {"level":"info","fixture":"go","stage":"build","event":"completed","artifact":"build\\go-fixture.exe"}
