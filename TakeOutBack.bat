@echo off
REM TakeOutBack launcher for Windows.
REM Resolves the project root from the script's location and invokes the local
REM portable binary. No installation, no PATH, no admin rights required.
setlocal EnableDelayedExpansion
set "DIR=%~dp0"
set "BIN=%DIR%TakeOutBack\tools\windows\takeoutback.exe"

if not exist "%BIN%" (
    echo takeoutback: binary not found: %BIN% >&2
    exit /b 1
)

"%BIN%" --root "%DIR%" %*
endlocal
