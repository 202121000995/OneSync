@echo off
setlocal

rem Edit SOURCE_HOSTS before running. Include the source LAN IP used by target computers.
set SOURCE_HOSTS=192.168.1.10,localhost,127.0.0.1

if not exist certs mkdir certs

onesync-cert.exe -hosts "%SOURCE_HOSTS%" -cert certs\source.crt -key certs\source.key

echo.
echo Generated certs\source.crt and certs\source.key
echo Copy certs\source.crt to the target computer. Do not copy certs\source.key.
