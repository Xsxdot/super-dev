@echo off
rem Responsibility: locate the target-native runner and forward arguments/stdin.
rem Boundary: do not infer the target, parse summaries, or mutate the foundation.
set "BUNDLE_ROOT=%~dp0"
rem A quoted trailing backslash escapes the closing quote in Windows native argv parsing.
set "BUNDLE_ROOT=%BUNDLE_ROOT:~0,-1%"
"%BUNDLE_ROOT%\bin\runtime-validation.exe" --bundle-root "%BUNDLE_ROOT%" --credential-stdin %*
exit /b %ERRORLEVEL%
