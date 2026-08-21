@echo off
REM takeOutBack launcher for Windows.
REM Resolves the project root from the script's location, applies any staged
REM update and invokes the local portable binary. No installation, no PATH,
REM no admin rights required.
setlocal EnableDelayedExpansion
set "DIR=%~dp0"
set "UPDATE_DIR=%DIR%TakeOutBack\.update"
set "BIN=%DIR%TakeOutBack\tools\windows\takeoutback.exe"

REM Apply a staged update created by 'takeoutback update'. The .update
REM directory mirrors the project layout: TakeOutBack\... plus root-level files.
if exist "%UPDATE_DIR%\pending" (
    echo Applying staged update...
    if exist "%UPDATE_DIR%\TakeOutBack" (
        xcopy /E /I /Y "%UPDATE_DIR%\TakeOutBack\*" "%DIR%TakeOutBack\" >nul 2>&1
    )
    for %%F in (takeOutBack.bat takeOutBack.sh README.md CHANGELOG.md) do (
        if exist "%UPDATE_DIR%\%%F" (
            copy /Y "%UPDATE_DIR%\%%F" "%DIR%%%~nxF" >nul 2>&1
        )
    )
    rmdir /S /Q "%UPDATE_DIR%" >nul 2>&1
    echo Update applied.
)

REM Legacy .next file support: remove after one migration cycle.
set "NEXT=%BIN%.next"
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
REM closing quote ("F:\" would escape the quote and corrupt the path).
"%BIN%" --root "%DIR%." %*
endlocal
