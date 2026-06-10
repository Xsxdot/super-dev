: << 'CMDBLOCK'
@echo off
REM SuperDev SessionStart hook 的跨平台 polyglot 包装器。
REM
REM 边界与设计：
REM   - Windows 下 cmd.exe 执行 batch 段，找到并调用 bash 运行实际脚本。
REM   - Unix 下 shell 把本文件当脚本解释（: 是 bash 的 no-op），走文件末尾的 exec。
REM   - hook 脚本用无扩展名文件名（session-start 而非 .sh），避免 Claude Code
REM     在 Windows 上对含 .sh 的命令自动加 bash 前缀造成干扰。
REM   - 找不到 bash 时静默退出 0：宁可少一次上下文注入，也不要让 agent 启动失败。
REM
REM 用法：run-hook.cmd <script-name> [args...]

if "%~1"=="" (
    echo run-hook.cmd: missing script name >&2
    exit /b 1
)

set "HOOK_DIR=%~dp0"

REM 优先尝试标准位置的 Git for Windows bash
if exist "C:\Program Files\Git\bin\bash.exe" (
    "C:\Program Files\Git\bin\bash.exe" "%HOOK_DIR%%~1" %2 %3 %4 %5 %6 %7 %8 %9
    exit /b %ERRORLEVEL%
)
if exist "C:\Program Files (x86)\Git\bin\bash.exe" (
    "C:\Program Files (x86)\Git\bin\bash.exe" "%HOOK_DIR%%~1" %2 %3 %4 %5 %6 %7 %8 %9
    exit /b %ERRORLEVEL%
)

REM 再尝试 PATH 上的 bash（用户自装 Git Bash、MSYS2、Cygwin 等）
where bash >nul 2>nul
if %ERRORLEVEL% equ 0 (
    bash "%HOOK_DIR%%~1" %2 %3 %4 %5 %6 %7 %8 %9
    exit /b %ERRORLEVEL%
)

REM 没有 bash —— 静默退出，不报错（少了上下文注入，但 agent 仍可正常工作）
exit /b 0
CMDBLOCK

# Unix：直接运行指定的脚本
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPT_NAME="$1"
shift
exec bash "${SCRIPT_DIR}/${SCRIPT_NAME}" "$@"
