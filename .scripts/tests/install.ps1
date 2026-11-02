# Run with powershell.exe or pwsh. Each case runs the installer in a child
# process because checksum failures deliberately call exit.
param(
    [string]$Scenario,
    [string]$FixtureRoot,
    [string]$Installer = (Join-Path $PSScriptRoot "../install.ps1")
)

$ErrorActionPreference = "Stop"

if ($Scenario) {
    $env:LOCALAPPDATA = $FixtureRoot
    $env:PROCESSOR_ARCHITECTURE = "AMD64"
    $assetName = "sqlgo_1.2.3_windows_amd64.zip"
    $archive = [System.Text.Encoding]::UTF8.GetBytes("fixture archive")
    $hasher = [System.Security.Cryptography.SHA256]::Create()
    try {
        $hash = [BitConverter]::ToString($hasher.ComputeHash($archive)).Replace("-", "").ToLowerInvariant()
    } finally {
        $hasher.Dispose()
    }

    # Include an unrelated entry so selecting the first hash cannot pass.
    $checksums = "$('0' * 64)  other.zip`n$hash  $assetName`n"
    switch ($Scenario) {
        "text" { }
        "bytes" { }
        "crlf-binary" { $checksums = "$($hash.ToUpperInvariant()) *$assetName`r`n" }
        "missing" { $checksums = "$hash  other.zip`n" }
        "malformed" { $checksums = "xyz  $assetName`n" }
        "mismatch" { $checksums = "$('0' * 64)  $assetName`n" }
        default { throw "Unknown scenario: $Scenario" }
    }

    function Invoke-RestMethod {
        param($Uri)
        return [pscustomobject]@{
            tag_name = "v1.2.3"
            assets = @(
                [pscustomobject]@{ name = $assetName; browser_download_url = "https://fixture/archive" },
                [pscustomobject]@{ name = "checksums.txt"; browser_download_url = "https://fixture/checksums" }
            )
        }
    }

    function Invoke-WebRequest {
        param($Uri, $OutFile, [switch]$UseBasicParsing)
        if (-not $UseBasicParsing) { throw "Installer requests must use basic parsing" }
        if ($Uri -eq "https://fixture/archive" -and $OutFile) {
            $nativePath = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($OutFile)
            [System.IO.File]::WriteAllBytes($nativePath, $archive)
        } elseif ($Uri -eq "https://fixture/checksums") {
            if ($Scenario -eq "text") {
                return [pscustomobject]@{ Content = $checksums }
            }
            return [pscustomobject]@{ Content = [System.Text.Encoding]::UTF8.GetBytes($checksums) }
        } else {
            throw "Unexpected request: $Uri"
        }
    }

    function Expand-Archive {
        param($Path, $DestinationPath, [switch]$Force)
        # Stop after real checksum verification, before installation or PATH edits.
        throw "CHECKSUM_ACCEPTED"
    }

    try {
        & $Installer
        throw "Installer unexpectedly returned"
    } catch {
        if ($_.Exception.Message -eq "CHECKSUM_ACCEPTED") { exit 0 }
        throw
    }
}

$shell = (Get-Process -Id $PID).Path
$cases = @(
    @{ Name = "text"; Code = 0; Message = "Checksum verified" },
    @{ Name = "bytes"; Code = 0; Message = "Checksum verified" },
    @{ Name = "crlf-binary"; Code = 0; Message = "Checksum verified" },
    @{ Name = "missing"; Code = 1; Message = "No checksum found" },
    @{ Name = "malformed"; Code = 1; Message = "No checksum found" },
    @{ Name = "mismatch"; Code = 1; Message = "Checksum mismatch!" }
)

foreach ($case in $cases) {
    $root = Join-Path ([System.IO.Path]::GetTempPath()) ([guid]::NewGuid().ToString())
    $existing = Join-Path $root "Programs/sqlgo/sqlgo.exe"
    New-Item -ItemType Directory -Path (Split-Path $existing) -Force | Out-Null
    [System.IO.File]::WriteAllText($existing, "existing installation")
    try {
        $output = & $shell -NoProfile -File $PSCommandPath -Scenario $case.Name -FixtureRoot $root -Installer $Installer 2>&1
        $code = $LASTEXITCODE
        if ($code -ne $case.Code -or ($output -join "`n") -notmatch [regex]::Escape($case.Message)) {
            throw "$($case.Name): expected exit $($case.Code) and '$($case.Message)', got exit $code`n$($output -join "`n")"
        }
        if ([System.IO.File]::ReadAllText($existing) -ne "existing installation") {
            throw "$($case.Name): changed the existing installation"
        }
        Write-Host "PASS $($case.Name)"
    } finally {
        Remove-Item $root -Recurse -Force
    }
}

exit 0
