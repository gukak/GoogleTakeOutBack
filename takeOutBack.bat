@echo off
REM takeOutBack launcher for Windows.
REM Resolves the project root from the script's location and invokes the local
REM portable binary. No installation, no PATH, no admin rights required.
setlocal
set "DIR=%~dp0"
set "BIN=%DIR%TakeOutBack\tools\windows\takeoutback.exe"
set "NEXT=%BIN%.next"

REM If a staged update exists, replace the current binary before launching.
if exist "%NEXT%" (
    if exist "%BIN%" move /Y "%BIN%" "%BIN%.old" >nul 2>&1
    move /Y "%NEXT%" "%BIN%" >nul 2>&1
    del "%BIN%.old" >nul 2>&1
)

if not exist "%BIN%" (
    echo takeoutback: binary not found: %BIN% >&2
    exit /b 1
)

REM Append "." to the directory so the trailing backslash is not followed by a
REM closing quote ("F:\\" would escape the quote and corrupt the path).
"%BIN%" --root "%DIR%." %*
endlocal
