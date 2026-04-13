# sqlgo Uninstaller for Windows
# Usage: irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.ps1 | iex

$ErrorActionPreference = "Stop"

$installDir = "$env:LOCALAPPDATA\sqlgo"

Write-Host "Uninstalling sqlgo..." -ForegroundColor Cyan

# 1. Kill running sqlgo
$procs = Get-Process -Name "sqlgo" -ErrorAction SilentlyContinue
if ($procs) {
    $procs | Stop-Process -Force -ErrorAction SilentlyContinue
}

# 2. Remove from PATH
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -like "*$installDir*") {
    $newPath = ($userPath -split ";" | Where-Object { $_ -ne $installDir }) -join ";"
    [Environment]::SetEnvironmentVariable("PATH", $newPath, "User")
    Write-Host "  Removed from PATH" -ForegroundColor Green
}

# 3. Remove install directory
if (Test-Path $installDir) {
    Remove-Item $installDir -Recurse -Force
    Write-Host "  Removed $installDir" -ForegroundColor Green
}

Write-Host "`nsqlgo uninstalled." -ForegroundColor Cyan
