@echo off
setlocal

set "ONESYNC_DATA=%APPDATA%\OneSync"
if not exist "%ONESYNC_DATA%\logs" mkdir "%ONESYNC_DATA%\logs"
start "" "%ONESYNC_DATA%\logs"
