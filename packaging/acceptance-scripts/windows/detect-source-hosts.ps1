$ErrorActionPreference = "SilentlyContinue"

$privateAddressPattern = '(?<!\d)(10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})(?!\d)'
$text = (ipconfig) -join "`n"
$addresses = [regex]::Matches($text, $privateAddressPattern) |
  ForEach-Object { $_.Value } |
  Where-Object { $_ -ne "127.0.0.1" } |
  Select-Object -Unique

$hosts = @($addresses) + @("localhost", "127.0.0.1")
($hosts | Where-Object { $_ } | Select-Object -Unique) -join ","
