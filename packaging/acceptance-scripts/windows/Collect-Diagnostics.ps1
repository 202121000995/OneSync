$ErrorActionPreference = "Stop"

$timestamp = Get-Date -Format "yyyyMMdd-HHmmss"
$output = Join-Path $PSScriptRoot "onesync-diagnostics-$timestamp.zip"

Invoke-WebRequest -Uri "http://127.0.0.1:8765/api/diagnostics.zip" -OutFile $output
Write-Host "OneSync diagnostics package written to:"
Write-Host $output
