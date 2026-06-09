@echo off
setlocal

rem Override SOURCE_HOSTS only when automatic IP detection is not enough.
rem Normal source startup does not need this script; OneSync prepares source TLS automatically.
if "%SOURCE_HOSTS%"=="" (
  for /f "usebackq delims=" %%H in (`powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0detect-source-hosts.ps1"`) do set "SOURCE_HOSTS=%%H"
)
if "%SOURCE_HOSTS%"=="" set "SOURCE_HOSTS=localhost,127.0.0.1"

if not exist certs mkdir certs

echo Source certificate hosts: %SOURCE_HOSTS%
onesync-cert.exe -hosts "%SOURCE_HOSTS%" -cert certs\source.crt -key certs\source.key

echo.
echo Generated certs\source.crt and certs\source.key
echo The generated synchronization link will carry certs\source.crt to the target.
echo Do not copy certs\source.key.
