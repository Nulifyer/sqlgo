$ErrorActionPreference = "Stop"

$workspace = Split-Path -Parent $PSScriptRoot
$os = (& go env GOOS).Trim()
$arch = (& go env GOARCH).Trim()
$outDir = Join-Path $workspace "dist"
$ext = ""
$ldflags = "-s -w"

if ($os -eq "windows") {
    $ext = ".exe"
    $ldflags = "-linkmode=internal -s -w"
}

$outFile = Join-Path $outDir ("sqlgo-" + $os + "-" + $arch + $ext)

New-Item -ItemType Directory -Force -Path $outDir | Out-Null

Push-Location $workspace
try {
    & go build -trimpath "-ldflags=$ldflags" -o $outFile .\cmd\sqlgo
    Write-Host "Built $outFile"
}
finally {
    Pop-Location
}
