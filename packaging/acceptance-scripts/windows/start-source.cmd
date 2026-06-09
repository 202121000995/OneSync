@echo off
setlocal

rem Run this on the source computer after make-source-cert.cmd.
if not exist data\source mkdir data\source
if not exist logs mkdir logs

onesync.exe ^
  -cert certs\source.crt ^
  -key certs\source.key ^
  -ca certs\source.crt ^
  -data-dir data\source ^
  -log-file logs\source.log ^
  -sync-interval 10s
