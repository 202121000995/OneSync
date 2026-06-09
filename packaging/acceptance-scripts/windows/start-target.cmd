@echo off
setlocal

if not exist data\target mkdir data\target
if not exist logs mkdir logs

onesync.exe ^
  -data-dir data\target ^
  -log-file logs\target.log ^
  -sync-interval 10s
