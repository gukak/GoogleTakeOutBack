@echo off
REM Self-update wrapper: invokes the embedded updater shipped with the binary.
setlocal
set "DIR=%~dp0"
"%DIR%TakeOutBack.bat" update %*
endlocal
