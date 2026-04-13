# dev-deploy.ps1 -- Build and install dev binaries to local install path
# Usage: .\.scripts\dev-deploy.ps1

$ErrorActionPreference = "Stop"

$installDir = "$env:LOCALAPPDATA\sqlgo"
$repoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

$cmds = @("sqlgo", "sqlgocheck", "sqlgoseed")

# Kill any running sqlgo processes
foreach ($name in $cmds) {
    $procs = Get-Process -Name $name -ErrorAction SilentlyContinue
    if ($procs) {
        Write-Host "Killing $name..." -ForegroundColor Yellow
        $procs | Stop-Process -Force -ErrorAction SilentlyContinue
    }
}
Start-Sleep -Milliseconds 200

# Build dev version string from git
$commitShort = git -C $repoRoot rev-parse --short HEAD
$version = "dev-$commitShort"
Write-Host "Building sqlgo $version ..." -ForegroundColor Cyan

New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Push-Location $repoRoot
try {
    foreach ($name in $cmds) {
        $outPath = "$installDir\$name.exe"
        Write-Host "  -> $name" -ForegroundColor DarkGray
        go build -ldflags "-s -w" -o $outPath "./cmd/$name"
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $name" }
    }
} finally {
    Pop-Location
}

Write-Host "Installed to $installDir ($version)" -ForegroundColor Green
Write-Host "Ensure '$installDir' is on PATH." -ForegroundColor DarkGray
