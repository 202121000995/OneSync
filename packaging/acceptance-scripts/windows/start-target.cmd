@echo off
setlocal

rem Copy certs\source.crt from the source computer before running this script.
if not exist data\target mkdir data\target
if not exist logs mkdir logs

onesync.exe ^
  -ca certs\source.crt ^
  -data-dir data\target ^
  -log-file logs\target.log ^
  -sync-interval 10s
