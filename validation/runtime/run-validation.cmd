@echo off
rem 职责：只定位已解压 bundle 中的 target-native runner，并透传调用参数/stdin。
rem 边界：不推断 target、不解析 summary、不修改 foundation。
set "BUNDLE_ROOT=%~dp0"
"%BUNDLE_ROOT%bin\runtime-validation.exe" --bundle-root "%BUNDLE_ROOT%" --credential-stdin %*
exit /b %ERRORLEVEL%
