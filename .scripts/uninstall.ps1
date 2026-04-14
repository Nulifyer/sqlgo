# sqlgo Uninstaller for Windows
# Usage:
#   irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.ps1 | iex
#   & ([scriptblock]::Create((irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/uninstall.ps1))) -Purge

param(
    [switch]$Purge
)

$ErrorActionPreference = "Stop"

$installDir = "$env:LOCALAPPDATA\Programs\sqlgo"
$dataDir = "$env:LOCALAPPDATA\sqlgo"
$legacyDir = Join-Path $env:USERPROFILE ".sqlgo"

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

# 3. Remove install directory (binary only)
if (Test-Path $installDir) {
    Remove-Item $installDir -Recurse -Force
    Write-Host "  Removed $installDir" -ForegroundColor Green
}

# 4. User data: purge only when asked
if ($Purge) {
    foreach ($d in @($dataDir, $legacyDir)) {
        if (Test-Path $d) {
            Remove-Item $d -Recurse -Force
            Write-Host "  Purged $d" -ForegroundColor Green
        }
    }
} else {
    $remaining = @($dataDir, $legacyDir) | Where-Object { Test-Path $_ }
    if ($remaining.Count -gt 0) {
        Write-Host ""
        Write-Host "User data preserved. Re-run with -Purge to delete:" -ForegroundColor Yellow
        foreach ($d in $remaining) { Write-Host "  $d" -ForegroundColor Yellow }
    }
}

Write-Host "`nsqlgo uninstalled." -ForegroundColor Cyan
