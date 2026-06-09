@echo off
setlocal

rem Run this on the source computer. OneSync loads a source TLS certificate automatically.
if not exist data\source mkdir data\source
if not exist logs mkdir logs

onesync.exe ^
  -data-dir data\source ^
  -log-file logs\source.log ^
  -sync-interval 10s
