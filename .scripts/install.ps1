# sqlgo Installer for Windows
# Usage: irm https://raw.githubusercontent.com/Nulifyer/sqlgo/main/.scripts/install.ps1 | iex

$ErrorActionPreference = "Stop"

$repo = "Nulifyer/sqlgo"
$installDir = "$env:LOCALAPPDATA\Programs\sqlgo"
$dataDir = "$env:LOCALAPPDATA\sqlgo"
$exe = "$installDir\sqlgo.exe"

# Detect architecture
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default {
        Write-Host "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" -ForegroundColor Red
        exit 1
    }
}

Write-Host "Installing sqlgo for windows/${arch}..." -ForegroundColor Cyan

# 1. Get latest release
Write-Host "  Fetching latest release..."
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$tag = $release.tag_name
$version = $tag.TrimStart("v")
$assetName = "sqlgo_${version}_windows_${arch}.zip"
$checksumName = "checksums.txt"
$asset = $release.assets | Where-Object { $_.name -eq $assetName }
$checksumAsset = $release.assets | Where-Object { $_.name -eq $checksumName }
if (-not $asset) {
    Write-Host "  ERROR: No Windows archive found in release $tag ($assetName)" -ForegroundColor Red
    exit 1
}

# 2. Download archive
Write-Host "  Downloading $tag..."
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$zipFile = "$installDir\sqlgo-update.zip"
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipFile

# 3. Verify checksum
if ($checksumAsset) {
    Write-Host "  Verifying checksum..."
    $checksums = (Invoke-WebRequest -Uri $checksumAsset.browser_download_url).Content
    $expectedLine = ($checksums -split "`n") | Where-Object { $_ -like "*$assetName*" } | Select-Object -First 1
    if ($expectedLine) {
        $expectedHash = ($expectedLine -split "\s+")[0]
        $actualHash = (Get-FileHash -Path $zipFile -Algorithm SHA256).Hash.ToLower()
        if ($actualHash -ne $expectedHash) {
            Remove-Item $zipFile -Force
            Write-Host "  ERROR: Checksum mismatch!" -ForegroundColor Red
            Write-Host "    Expected: $expectedHash" -ForegroundColor Red
            Write-Host "    Got:      $actualHash" -ForegroundColor Red
            exit 1
        }
        Write-Host "  Checksum verified" -ForegroundColor Green
    }
}

# 4. Extract
$extractDir = "$installDir\sqlgo-extract"
if (Test-Path $extractDir) { Remove-Item $extractDir -Recurse -Force }
Expand-Archive -Path $zipFile -DestinationPath $extractDir -Force
Remove-Item $zipFile -Force

$newExe = "$extractDir\sqlgo.exe"
if (-not (Test-Path $newExe)) {
    Write-Host "  ERROR: sqlgo.exe not found in archive" -ForegroundColor Red
    Remove-Item $extractDir -Recurse -Force
    exit 1
}

# 5. Replace binary (handle running exe)
if (Test-Path $exe) {
    $oldFile = "$exe.old"
    if (Test-Path $oldFile) { Remove-Item $oldFile -Force }
    try {
        Rename-Item $exe $oldFile -Force
    } catch {
        Write-Host "  WARNING: Could not rename existing binary. Close any running sqlgo first." -ForegroundColor Yellow
    }
}
Move-Item $newExe $exe -Force
Remove-Item $extractDir -Recurse -Force
Write-Host "  Installed to $exe" -ForegroundColor Green

# 6. Add to PATH if not already there
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$userPath;$installDir", "User")
    $env:PATH = "$env:PATH;$installDir"
    Write-Host "  Added to PATH" -ForegroundColor Green
} else {
    Write-Host "  Already in PATH" -ForegroundColor Green
}

# 7. Migrate legacy data dir (~/.sqlgo) into %LocalAppData%\sqlgo
$legacyDir = Join-Path $env:USERPROFILE ".sqlgo"
$legacyDb = Join-Path $legacyDir "sqlgo.db"
$newDb = Join-Path $dataDir "sqlgo.db"
if ((Test-Path $legacyDb) -and (-not (Test-Path $newDb))) {
    Write-Host "  Migrating $legacyDir -> $dataDir" -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
    foreach ($f in @("sqlgo.db", "sqlgo.db-wal", "sqlgo.db-shm")) {
        $src = Join-Path $legacyDir $f
        if (Test-Path $src) { Move-Item $src (Join-Path $dataDir $f) -Force }
    }
    if ((Test-Path $legacyDir) -and (-not (Get-ChildItem $legacyDir -Force))) {
        Remove-Item $legacyDir -Force
    }
}

Write-Host "`nsqlgo $tag installed!" -ForegroundColor Cyan
Write-Host "Open a new terminal and run 'sqlgo'." -ForegroundColor Cyan
